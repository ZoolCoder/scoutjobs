//go:build !windows

package cli

import (
	"os"
	"syscall"
)

const canReexec = true

// reexec replaces this process with the binary at path: same pid, arguments and environment.
func reexec(path string) error {
	return syscall.Exec(path, os.Args, os.Environ())
}
