package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/autopilot"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

// runJobVerify checks that tracked postings are still open, so no tokens go to a closed job.
func runJobVerify(s *store.Store, rest []string, out io.Writer) error {
	fs := flag.NewFlagSet("job verify", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "")
	pos, err := parse(fs, rest)
	if err != nil {
		return err
	}
	var ids []int64
	for _, ref := range pos {
		j, err := s.ResolveJob(ref)
		if err != nil {
			return err
		}
		ids = append(ids, j.ID)
	}
	checks, err := autopilot.Verify(context.Background(), s, autopilot.Options{}, ids, time.Now())
	if err != nil {
		return err
	}
	if checks == nil {
		checks = []autopilot.Check{}
	}
	return emit(out, *asJSON, map[string]any{"checks": checks}, func() {
		if len(checks) == 0 {
			fmt.Fprintln(out, "nothing to check: no new or ranked job with a posting URL is due")
			return
		}
		var tbl [][]string
		for _, c := range checks {
			tbl = append(tbl, []string{fmt.Sprint(c.ID), trunc(c.Company, 22), trunc(c.Title, 40), c.Result, trunc(c.Note, 60)})
		}
		table(out, "ID\tCOMPANY\tTITLE\tRESULT\tNOTE", tbl)
	})
}
