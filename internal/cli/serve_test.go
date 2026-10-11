//go:build !windows

package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatchBinarySeesUpgrade(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "scoutjobs")
	write := func(v string) {
		tmp := bin + ".new"
		if err := os.WriteFile(tmp, []byte("#!/bin/sh\necho scoutjobs "+v+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, bin); err != nil {
			t.Fatal(err)
		}
	}
	write("0.3.0")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { time.Sleep(50 * time.Millisecond); write("0.3.1") }()
	v, ok := watchBinary(ctx, bin, 20*time.Millisecond)
	if !ok || v != "0.3.1" {
		t.Fatalf("got %q %v", v, ok)
	}

	// a replacement that does not run is ignored; the server keeps going
	if err := os.WriteFile(bin+".new", []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel2()
	go func() { time.Sleep(50 * time.Millisecond); _ = os.Rename(bin+".new", bin) }()
	if v, ok := watchBinary(ctx2, bin, 20*time.Millisecond); ok {
		t.Errorf("broken binary reported as %q", v)
	}
}

func TestDevBuild(t *testing.T) {
	defer func(v string) { Version = v }(Version)
	for v, want := range map[string]bool{"dev": true, "0.3.0-2-ga3e2185-dev": true, "0.3.0-dev": true, "0.3.0": false, "0.4.0-rc.1": false} {
		Version = v
		if devBuild() != want {
			t.Errorf("devBuild(%q) = %v", v, !want)
		}
	}
}
