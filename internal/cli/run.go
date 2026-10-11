package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/zoolcoder/scoutjobs/internal/runner"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

// newRunner wires the job runner to this binary and config.yaml.
func newRunner(s *store.Store, cfg Config) *runner.Runner {
	exe, err := os.Executable()
	if err != nil {
		exe = "scoutjobs"
	}
	return &runner.Runner{Store: s, Exe: exe, Custom: cfg.AgentCommand, Agent: cfg.Agent, From: cfg.EmailFrom, CVSource: cfg.CVSource, CoverTemplate: cfg.CoverTemplate}
}

// job run: prepare one application end to end with an installed agent CLI. Nothing is sent.
func runJobRun(s *store.Store, rest []string, out io.Writer) error {
	fs := flag.NewFlagSet("job run", flag.ContinueOnError)
	steps := fs.String("steps", "safe", "comma-separated: fit,tailor,cover,review,export,ats,check,draft; safe = all of them")
	fs.Bool("safe", true, "every step, skipping those already done (default)")
	agent := fs.String("agent", "", "claude | codex | custom (agent_command in config.yaml); default: agent in config.yaml, else the first found")
	force := fs.Bool("force", false, "redo steps whose output already exists")
	keep := fs.Bool("keep-going", false, "go on when the draft review asks for fixes; quality gates never stop preparation, only sending")
	dry := fs.Bool("dry-run", false, "show the plan without running anything")
	asJSON := fs.Bool("json", false, "")
	pos, err := parse(fs, rest)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return fmt.Errorf("usage: scoutjobs job run <id> [--steps %s] [--agent claude|codex|custom] [--force] [--keep-going] [--dry-run]", "fit,tailor,...")
	}
	j, err := s.ResolveJob(pos[0])
	if err != nil {
		return err
	}
	list, err := runner.Parse(*steps)
	if err != nil {
		return err
	}
	rn := newRunner(s, LoadConfig(s.Home))
	run, ag, err := rn.Plan(j, runner.Request{Steps: list, Agent: *agent, Force: *force, KeepGoing: *keep, DryRun: *dry})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	echo := io.Writer(out)
	if *asJSON {
		echo = nil
	} else {
		fmt.Fprintf(out, "preparing #%d %s / %s", j.ID, j.Company, j.Title)
		if run.Agent != "" {
			fmt.Fprintf(out, " with %s", run.Agent)
		}
		fmt.Fprintln(out)
	}
	rn.Exec(ctx, run, ag, j, echo)
	snap := run.Snapshot()
	err = emit(out, *asJSON, snap, func() {
		for _, st := range snap.Steps {
			fmt.Fprintf(out, "  %-8s %-7s %s\n", st.Name, st.State, st.Detail)
		}
		if snap.State == runner.Done && !snap.DryRun {
			fmt.Fprintf(out, "ready for your review: scoutjobs job show %d; send it yourself with scoutjobs email send %d --yes\n", j.ID, j.ID)
		}
	})
	if err == nil && snap.State != runner.Done {
		err = fmt.Errorf("run %s: %s", snap.State, strings.TrimSpace(snap.Error))
	}
	return err
}
