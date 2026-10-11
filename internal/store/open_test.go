package store

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestOpenErrorNamesPathAndHint(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs unix permissions and a non-root user")
	}
	home := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(home, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(home, 0o700) })
	t.Setenv("SCOUTJOBS_HOME", home)
	_, err := Open()
	if err == nil {
		t.Fatal("opened a database in a read-only dir")
	}
	msg := err.Error()
	for _, want := range []string{filepath.Join(home, "scoutjobs.db"), "from SCOUTJOBS_HOME", "hint: no write permission on " + home} {
		if !strings.Contains(msg, want) {
			t.Errorf("error lacks %q:\n%s", want, msg)
		}
	}
}
