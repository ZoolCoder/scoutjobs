package main

import (
	"fmt"
	"os"

	"github.com/zoolcoder/scoutjobs/internal/cli"
)

func main() {
	if err := cli.Run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
