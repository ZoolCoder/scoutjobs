package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/store"
	"github.com/zoolcoder/scoutjobs/internal/web"
)

// serveAndRestart runs the dashboard. With restart, it watches its own binary: once an upgrade
// (brew, make install, scoutjobs update) replaces it, the server stops and the new binary takes
// over in the same process, on the same address, so the dashboard never runs a stale version.
func serveAndRestart(s *store.Store, addr string, restart bool, out io.Writer, cfg web.Config) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if !restart || !canReexec {
		return web.ServeContext(ctx, s, addr, cfg)
	}
	exe := selfPath()
	srvCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	upgraded := make(chan string, 1)
	go func() {
		if v, ok := watchBinary(srvCtx, exe, 3*time.Second); ok {
			upgraded <- v
			cancel()
		}
	}()
	if err := web.ServeContext(srvCtx, s, addr, cfg); err != nil {
		return err
	}
	select {
	case v := <-upgraded:
		fmt.Fprintf(out, "binary upgraded (%s -> %s), restarting the dashboard\n", Version, v)
		_ = s.Close()
		return reexec(exe)
	default:
		return nil
	}
}

// selfPath is the path the user ran, not resolved, so a brew symlink moving to a new version is seen.
func selfPath() string {
	if p := os.Args[0]; !strings.ContainsRune(p, filepath.Separator) {
		if lp, err := exec.LookPath(p); err == nil {
			if abs, err := filepath.Abs(lp); err == nil {
				return abs
			}
		}
	} else if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	exe, _ := os.Executable()
	return exe
}

// watchBinary polls the binary at path. When it changes, it waits until the file stops changing
// (a copy in progress) and the new binary answers `version`, then reports that version.
func watchBinary(ctx context.Context, path string, every time.Duration) (string, bool) {
	orig, err := os.Stat(path)
	if err != nil {
		return "", false
	}
	var last os.FileInfo
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", false
		case <-t.C:
		}
		fi, err := os.Stat(path)
		if err != nil || sameFile(fi, orig) {
			last = nil
			continue
		}
		if last == nil || !sameFile(fi, last) {
			last = fi // changed since the previous poll: let a copy finish
			continue
		}
		if v, ok := binaryVersion(ctx, path); ok {
			return v, true
		}
		orig = fi // not runnable (a broken build): keep serving, watch for the next one
		last = nil
	}
}

func sameFile(a, b os.FileInfo) bool {
	return os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

func binaryVersion(ctx context.Context, path string) (string, bool) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, path, "version").Output()
	f := strings.Fields(string(b))
	if err != nil || len(f) < 2 || f[0] != "scoutjobs" {
		return "", false
	}
	return f[1], true
}
