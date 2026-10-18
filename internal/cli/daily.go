package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/autopilot"
	"github.com/zoolcoder/scoutjobs/internal/runner"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

// runDaily does the chores serve does every hour, for cron, launchd or n8n when the dashboard is not running.
func runDaily(s *store.Store, cfg Config, rest []string, out io.Writer) error {
	fs := flag.NewFlagSet("daily", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	m := runner.NewManager(newRunner(s, cfg))
	rep := autopilot.Daily(context.Background(), s, autopilot.Options{FollowUpDays: cfg.FollowUpDays, Prep: prepNow(m), SyncMail: autoMail(s, cfg)}, time.Now())
	return emit(out, *asJSON, rep, func() {
		fmt.Fprintln(out, rep.Summary())
		for _, x := range rep.Searches {
			fmt.Fprintf(out, "  %s: %d found, %d added\n", x.Name, x.Found, x.Added)
			for _, e := range x.Errors {
				fmt.Fprintf(out, "    %s\n", e)
			}
		}
		for _, f := range rep.FollowUps {
			fmt.Fprintf(out, "  drafted %s (review it on the job page; nothing was sent)\n", f)
		}
		for _, c := range rep.Prep {
			fmt.Fprintf(out, "  prep pack for %s\n", c)
		}
		for _, e := range rep.Errors {
			fmt.Fprintf(out, "  error: %s\n", e)
		}
	})
}

// prepNow builds the prep pack and waits for it, since the command exits when it returns.
func prepNow(m *runner.Manager) func(store.Job) bool {
	return func(j store.Job) bool {
		run, err := m.Start(j, runner.Request{Steps: []string{"prep"}})
		if err != nil {
			return false
		}
		run.Wait()
		return true
	}
}
