package cli

import "errors"

// Windows cannot replace a running process; the dashboard keeps its version until restarted.
const canReexec = false

func reexec(string) error { return errors.New("restart the dashboard to use the new version") }
