package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func tarGz(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	tw.Write(body)
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestUpdateReplacesBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("release archives for windows are zips; the tar path is covered on unix")
	}
	newBin := []byte("#!/bin/sh\necho scoutjobs 9.9.0\n")
	archive := tarGz(t, "scoutjobs", newBin)
	sum := sha256.Sum256(archive)
	name := archiveName()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/latest":
			fmt.Fprintf(w, `{"tag_name":"v9.9.0","html_url":"https://example.test/v9.9.0","assets":[{"name":%q,"browser_download_url":%q},{"name":"checksums.txt","browser_download_url":%q}]}`,
				name, srv.URL+"/dl/"+name, srv.URL+"/dl/checksums.txt")
		case "/releases/tags/v1.0.0":
			fmt.Fprintf(w, `{"tag_name":"v1.0.0","html_url":"https://example.test/v1.0.0","assets":[]}`)
		case "/dl/" + name:
			w.Write(archive)
		case "/dl/checksums.txt":
			fmt.Fprintf(w, "%s  other.tar.gz\n%s  %s\n", strings.Repeat("0", 64), hex.EncodeToString(sum[:]), name)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dir, _ := filepath.EvalSymlinks(t.TempDir())
	exe := filepath.Join(dir, "scoutjobs")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldURL, oldVersion, oldExe := releaseURL, Version, executable
	releaseURL, Version, executable = srv.URL+"/releases/latest", "0.1.0", func() (string, error) { return exe, nil }
	defer func() { releaseURL, Version, executable = oldURL, oldVersion, oldExe }()

	if out := run(t, "update", "--check"); !strings.Contains(out, "update available: 0.1.0 -> v9.9.0") || !strings.Contains(out, "scoutjobs update") {
		t.Errorf("check:\n%s", out)
	}
	if got, _ := os.ReadFile(exe); string(got) != "old" {
		t.Error("--check must not touch the binary")
	}

	out := run(t, "update", "--yes")
	if !strings.Contains(out, "updated "+exe+": 0.1.0 -> v9.9.0") {
		t.Errorf("update:\n%s", out)
	}
	got, err := os.ReadFile(exe)
	if err != nil || !bytes.Equal(got, newBin) {
		t.Errorf("binary not replaced: %v %q", err, got)
	}
	if st, _ := os.Stat(exe); st.Mode()&0o100 == 0 {
		t.Error("binary lost its exec bit")
	}
	if _, err := os.Stat(exe + ".new"); err == nil {
		t.Error("temp file left behind")
	}

	Version = "9.9.0"
	if out := run(t, "update", "--yes"); !strings.Contains(out, "up to date") {
		t.Errorf("same version must be a no-op:\n%s", out)
	}
	if err := Run([]string{"update", "--yes", "--version", "1.0.0"}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "no asset") {
		t.Errorf("missing asset must fail clearly: %v", err)
	}
	Version = "dev"
	if err := Run([]string{"update", "--yes"}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "dev build") {
		t.Errorf("dev build must refuse without --version: %v", err)
	}
}

func TestUpdateRejectsBadChecksum(t *testing.T) {
	archive := tarGz(t, "scoutjobs", []byte("evil"))
	name := archiveName()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			fmt.Fprintf(w, `{"tag_name":"v9.9.0","assets":[{"name":%q,"browser_download_url":%q},{"name":"checksums.txt","browser_download_url":%q}]}`, name, srv.URL+"/a", srv.URL+"/c")
		case "/a":
			w.Write(archive)
		case "/c":
			fmt.Fprintf(w, "%s  %s\n", strings.Repeat("f", 64), name)
		}
	}))
	defer srv.Close()
	exe := filepath.Join(t.TempDir(), "scoutjobs")
	os.WriteFile(exe, []byte("old"), 0o755)
	oldURL, oldVersion, oldExe := releaseURL, Version, executable
	releaseURL, Version, executable = srv.URL+"/latest", "0.1.0", func() (string, error) { return exe, nil }
	defer func() { releaseURL, Version, executable = oldURL, oldVersion, oldExe }()

	err := Run([]string{"update", "--yes"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("want checksum error, got %v", err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "old" {
		t.Error("binary replaced despite bad checksum")
	}
}

func TestSemverLess(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0.1.0", "0.2.0", true}, {"0.2.0", "0.1.0", false}, {"1.0.0", "1.0.0", false},
		{"0.9.0", "0.10.0", true}, {"v1.2.3", "1.2.4", true}, {"1.0.0-rc1", "1.0.0", true}, {"1.0.0", "1.0.0-rc1", false},
		{"0.1.0-3-gabcdef", "0.1.0", true}, {"2.0.0", "10.0.0", true},
	}
	for _, c := range cases {
		if got := semverLess(c.a, c.b); got != c.want {
			t.Errorf("semverLess(%s, %s) = %v", c.a, c.b, got)
		}
	}
}

func TestInstallMethod(t *testing.T) {
	if installMethod("/opt/homebrew/Caskroom/scoutjobs/0.1.0/scoutjobs") != "brew" {
		t.Error("cask not detected")
	}
	if installMethod(filepath.Join(os.TempDir(), "x", "scoutjobs")) != "binary" {
		t.Error("plain binary not detected")
	}
}
