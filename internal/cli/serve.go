package cli

import (
	"context"
	"fmt"
	"io"
	"net"
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
// over in the same process, on the same address, so the dashboard never runs a stale version. An
// edit to config.yaml restarts it the same way, so the pages use the new settings. Either restart
// waits until no agent run is going, so an upgrade never kills a preparation half way.
func serveAndRestart(s *store.Store, addr string, restart bool, out io.Writer, cfg web.Config) error {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return portBusy(addr, s.Home, err)
	}
	fmt.Fprintf(out, "ScoutJobs %s dashboard on http://%s  (data: %s)\n", Version, addr, s.Home)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if !canReexec {
		return web.ServeListener(ctx, l, s, cfg)
	}
	exe := selfPath()
	srvCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	reason := make(chan string, 1)
	busy := func() bool { return cfg.Runner != nil && !cfg.Runner.Idle() }
	stopFor := func(why string) {
		if waitIdle(srvCtx, busy, 3*time.Second) {
			select {
			case reason <- why:
				cancel()
			default: // another restart is already on its way
			}
		}
	}
	if restart {
		go func() {
			if v, ok := watchBinary(srvCtx, exe, 3*time.Second); ok {
				stopFor(fmt.Sprintf("binary upgraded (%s -> %s)", Version, v))
			}
		}()
		go func() {
			if watchFile(srvCtx, filepath.Join(s.Home, "config.yaml"), 3*time.Second) {
				stopFor("config.yaml changed")
			}
		}()
	}
	// kill -HUP <pid> restarts on whatever binary is installed now
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)
	go func() {
		select {
		case <-hup:
			reason <- "SIGHUP"
			cancel()
		case <-srvCtx.Done():
		}
	}()
	if err := web.ServeListener(srvCtx, l, s, cfg); err != nil {
		return err
	}
	select {
	case why := <-reason:
		fmt.Fprintf(out, "%s, restarting the dashboard\n", why)
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

// watchFile polls path and reports true once it has changed and then stayed the same for one poll.
func watchFile(ctx context.Context, path string, every time.Duration) bool {
	orig, _ := os.Stat(path)
	var last os.FileInfo
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-t.C:
		}
		fi, err := os.Stat(path)
		if err != nil || orig != nil && sameFile(fi, orig) {
			last = nil
			continue
		}
		if last != nil && sameFile(fi, last) {
			return true
		}
		last = fi // changed since the previous poll: let the write finish
	}
}

// waitIdle blocks while busy reports true and says whether the context is still alive.
func waitIdle(ctx context.Context, busy func() bool, every time.Duration) bool {
	for busy() {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(every):
		}
	}
	return ctx.Err() == nil
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
