package cli

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/store"
)

// update replaces the running binary with a release from GitHub: fetch the release, pick the
// archive for this OS/arch, verify its sha256 against checksums.txt, swap the file in place.
// Homebrew and distro-package installs are handed to their package manager instead, so the
// manager's own records stay right.

// executable is os.Executable; tests point it at a scratch binary.
var executable = os.Executable

type release struct {
	Tag    string `json:"tag_name"`
	URL    string `json:"html_url"`
	Assets []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func (r release) asset(name string) string {
	for _, a := range r.Assets {
		if a.Name == name {
			return a.URL
		}
	}
	return ""
}

func runUpdate(rest []string, out io.Writer) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	check := fs.Bool("check", false, "report whether a newer release exists, change nothing")
	yes := fs.Bool("yes", false, "install without asking")
	want := fs.String("version", "", "install this tag (v1.2.3) instead of the latest release; also allows a downgrade")
	asJSON := fs.Bool("json", false, "")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	exe, err := executable()
	if err != nil {
		return err
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	rel, err := fetchRelease(ctx, *want)
	if err != nil {
		return err
	}
	cur, latest := strings.TrimPrefix(Version, "v"), strings.TrimPrefix(rel.Tag, "v")
	method := installMethod(exe)
	newer := !devBuild() && semverLess(cur, latest)
	res := map[string]any{"current": Version, "latest": rel.Tag, "newer": newer, "method": method, "path": exe, "url": rel.URL}

	switch {
	case *check:
		return emit(out, *asJSON, res, func() {
			if newer {
				fmt.Fprintf(out, "update available: %s -> %s (%s)\n  run: %s\n", Version, rel.Tag, rel.URL, updateHint(method))
			} else if devBuild() {
				fmt.Fprintf(out, "dev build; latest release is %s\n", rel.Tag)
			} else {
				fmt.Fprintf(out, "up to date: %s is the latest release\n", Version)
			}
		})
	case devBuild() && *want == "":
		return fmt.Errorf("dev build at %s: rebuild with make install, or pass --version %s to install that release here", exe, rel.Tag)
	case !newer && *want == "":
		return emit(out, *asJSON, res, func() { fmt.Fprintf(out, "up to date: %s is the latest release\n", Version) })
	}

	switch method {
	case "brew":
		if !*yes && !confirm(out, fmt.Sprintf("Upgrade the Homebrew cask to %s?", rel.Tag)) {
			return errors.New("aborted")
		}
		cmd := exec.CommandContext(ctx, "brew", "upgrade", "--cask", "scoutjobs")
		cmd.Stdout, cmd.Stderr = out, out
		if err := cmd.Run(); err != nil {
			return err
		}
		fmt.Fprintln(out, dashboardAfterUpdate())
		return nil
	case "package":
		return emit(out, *asJSON, res, func() {
			fmt.Fprintf(out, "%s was installed by a package manager; %s is at %s\n", exe, rel.Tag, rel.URL)
			fmt.Fprintf(out, "  deb: curl -fsSLO %s && sudo dpkg -i scoutjobs_linux_%s.deb\n", rel.asset("scoutjobs_linux_"+runtime.GOARCH+".deb"), runtime.GOARCH)
			fmt.Fprintf(out, "  rpm: sudo rpm -U %s\n", rel.asset("scoutjobs_linux_"+runtime.GOARCH+".rpm"))
		})
	}

	name := archiveName()
	assetURL := rel.asset(name)
	if assetURL == "" {
		return fmt.Errorf("release %s has no asset %s (see %s)", rel.Tag, name, rel.URL)
	}
	if !*yes && !confirm(out, fmt.Sprintf("Install %s over %s?", rel.Tag, exe)) {
		return errors.New("aborted")
	}
	archive, err := download(ctx, assetURL)
	if err != nil {
		return err
	}
	if sumsURL := rel.asset("checksums.txt"); sumsURL != "" {
		sums, err := download(ctx, sumsURL)
		if err != nil {
			return fmt.Errorf("checksums.txt: %w", err)
		}
		if err := verifyChecksum(sums, name, archive); err != nil {
			return err
		}
	} else {
		return fmt.Errorf("release %s has no checksums.txt: refusing an unverified binary (nothing was installed)", rel.Tag)
	}
	bin, err := extractBinary(name, archive)
	if err != nil {
		return err
	}
	if err := replaceBinary(exe, bin); err != nil {
		return err
	}
	res["installed"] = rel.Tag
	return emit(out, *asJSON, res, func() {
		fmt.Fprintf(out, "updated %s: %s -> %s\n", exe, Version, rel.Tag)
		fmt.Fprintln(out, dashboardAfterUpdate())
	})
}

func updateHint(method string) string {
	switch method {
	case "brew":
		return "brew upgrade --cask scoutjobs  (or scoutjobs update)"
	case "package":
		return "download the deb/rpm/apk from the release page"
	}
	return "scoutjobs update"
}

// installMethod guesses who owns the binary from where it lives.
func installMethod(exe string) string {
	p := filepath.ToSlash(exe)
	switch {
	case strings.Contains(p, "/Caskroom/") || strings.Contains(p, "/Cellar/") || strings.Contains(p, "/homebrew/") || strings.Contains(p, "/linuxbrew/"):
		return "brew"
	case runtime.GOOS == "linux" && (strings.HasPrefix(p, "/usr/bin/") || strings.HasPrefix(p, "/usr/sbin/")):
		return "package"
	}
	return "binary"
}

func fetchRelease(ctx context.Context, tag string) (release, error) {
	u := releaseURL
	if tag != "" {
		if !strings.HasPrefix(tag, "v") {
			tag = "v" + tag
		}
		u = strings.TrimSuffix(releaseURL, "/latest") + "/tags/" + tag
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "scoutjobs/"+Version)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return release{}, fmt.Errorf("could not reach GitHub: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound && tag != "" {
		return release{}, fmt.Errorf("no published release %s: the tag may exist while its binaries are still being built, so try again in a few minutes (nothing was installed)", tag)
	}
	if resp.StatusCode != http.StatusOK {
		return release{}, fmt.Errorf("GitHub answered %s", resp.Status)
	}
	var rel release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil || rel.Tag == "" {
		return release{}, errors.New("unexpected answer from GitHub")
	}
	return rel, nil
}

func download(ctx context.Context, u string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("User-Agent", "scoutjobs/"+Version)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", u, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 200<<20))
}

func archiveName() string {
	if runtime.GOOS == "windows" {
		return "scoutjobs_windows_" + runtime.GOARCH + ".zip"
	}
	return fmt.Sprintf("scoutjobs_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
}

// verifyChecksum finds "<sha256>  <name>" in goreleaser's checksums.txt and compares.
func verifyChecksum(sums []byte, name string, archive []byte) error {
	sum := sha256.Sum256(archive)
	got := hex.EncodeToString(sum[:])
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && f[1] == name {
			if f[0] == got {
				return nil
			}
			return fmt.Errorf("checksum mismatch for %s: want %s, got %s", name, f[0], got)
		}
	}
	return fmt.Errorf("%s not listed in checksums.txt", name)
}

// extractBinary returns the scoutjobs executable out of the release archive.
func extractBinary(name string, archive []byte) ([]byte, error) {
	want := "scoutjobs"
	if strings.HasSuffix(name, ".zip") {
		want = "scoutjobs.exe"
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if filepath.Base(f.Name) == want {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(io.LimitReader(rc, 200<<20))
			}
		}
		return nil, fmt.Errorf("%s not in %s", want, name)
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg && filepath.Base(h.Name) == want {
			return io.ReadAll(io.LimitReader(tr, 200<<20))
		}
	}
	return nil, fmt.Errorf("%s not in %s", want, name)
}

// replaceBinary writes next to the target and renames over it, so a crash mid-way leaves the
// old binary intact. Windows cannot overwrite a running executable, so the old one is moved aside.
func replaceBinary(exe string, bin []byte) error {
	tmp := exe + ".new"
	if err := os.WriteFile(tmp, bin, 0o755); err != nil {
		return fmt.Errorf("write %s: %w (not writable? re-run with sudo, or reinstall with ./install.sh)", tmp, err)
	}
	if runtime.GOOS == "windows" {
		old := exe + ".old"
		_ = os.Remove(old)
		if err := os.Rename(exe, old); err != nil {
			_ = os.Remove(tmp)
			return err
		}
	}
	if err := os.Rename(tmp, exe); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func confirm(out io.Writer, q string) bool {
	fmt.Fprintf(out, "%s [y/N] ", q)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes"
}

// semverLess reports a < b for "1.2.3"-style versions; a pre-release suffix sorts below its release.
func semverLess(a, b string) bool {
	pa, pb := semverParts(a), semverParts(b)
	for i := 0; i < 3; i++ {
		if pa.n[i] != pb.n[i] {
			return pa.n[i] < pb.n[i]
		}
	}
	if (pa.pre == "") != (pb.pre == "") {
		return pa.pre != ""
	}
	return pa.pre < pb.pre
}

type semver struct {
	n   [3]int
	pre string
}

func semverParts(v string) semver {
	var s semver
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		s.pre, v = v[i+1:], v[:i]
	}
	for i, p := range strings.SplitN(v, ".", 3) {
		s.n[i], _ = strconv.Atoi(p)
	}
	return s
}

// dashboardAfterUpdate says what happens to a dashboard on the configured address.
func dashboardAfterUpdate() string {
	addr := LoadConfig(store.Home()).DashboardAddr
	d := probeDashboard(addr)
	switch {
	case d.Health != nil:
		return fmt.Sprintf("dashboard on http://%s (pid %d) restarts on the new version within a few seconds; open tabs offer a reload", addr, d.Health.PID)
	case d.ScoutJobs:
		return fmt.Sprintf("the dashboard on http://%s predates auto-restart: pkill -f \"scoutjobs serve\" && scoutjobs serve", addr)
	}
	return "a dashboard started later runs the new version; running ones restart on it by themselves"
}
