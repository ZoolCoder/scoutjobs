package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/apply"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

// runJobFollowup lists the quiet applications with their follow-up count, or with an id records that
// the user sent one. Nothing is sent from here.
func runJobFollowup(s *store.Store, rest []string, out io.Writer) error {
	fs := flag.NewFlagSet("job followup", flag.ContinueOnError)
	note := fs.String("note", "", "what was sent, e.g. \"e-mailed the recruiter\"")
	asJSON := fs.Bool("json", false, "")
	pos, err := parse(fs, rest)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		j, err := s.ResolveJob(pos[0])
		if err != nil {
			return err
		}
		if j.Status != "applied" {
			return fmt.Errorf("#%d is %s: only applied jobs get follow-ups", j.ID, j.Status)
		}
		n, err := apply.FollowedUp(s, j, *note)
		if err != nil {
			return err
		}
		return emit(out, *asJSON, map[string]any{"job": j.ID, "follow_ups": n, "max": store.MaxFollowUps}, func() {
			fmt.Fprintf(out, "follow-up %d of %d logged for #%d %s; the quiet clock restarts\n", n, store.MaxFollowUps, j.ID, j.Company)
		})
	}
	cfg := LoadConfig(s.Home)
	applied, err := s.ListJobs(store.JobFilter{Status: "applied"})
	if err != nil {
		return err
	}
	now := time.Now()
	rows := make([]map[string]any, 0, len(applied))
	var tbl [][]string
	for _, j := range applied {
		quiet := daysQuiet(j, now)
		sent := s.FollowUps(j.ID)
		next := "done"
		switch {
		case cfg.StaleDays > 0 && quiet >= cfg.StaleDays:
			next = "sweep"
		case sent < store.MaxFollowUps && quiet >= cfg.FollowUpDays:
			next = "due"
		case sent < store.MaxFollowUps:
			next = fmt.Sprintf("in %dd", cfg.FollowUpDays-quiet)
		}
		rows = append(rows, map[string]any{"id": j.ID, "company": j.Company, "title": j.Title, "days_quiet": quiet, "follow_ups": sent, "next": next, "contact": j.Contact})
		tbl = append(tbl, []string{fmt.Sprint(j.ID), trunc(j.Company, 22), trunc(j.Title, 40), fmt.Sprint(quiet), fmt.Sprintf("%d/%d", sent, store.MaxFollowUps), next})
	}
	return emit(out, *asJSON, rows, func() {
		table(out, "ID\tCOMPANY\tTITLE\tQUIET\tSENT\tNEXT", tbl)
		fmt.Fprintf(out, "after sending one: scoutjobs job followup <id>. %d+ days quiet: scoutjobs job sweep\n", cfg.StaleDays)
	})
}

// runJobSweep closes the applications quiet for stale_days or more as no_response. Without --yes it
// only lists them; with ids it closes those alone.
func runJobSweep(s *store.Store, rest []string, out io.Writer) error {
	fs := flag.NewFlagSet("job sweep", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "close them; without it the list is shown and nothing changes")
	asJSON := fs.Bool("json", false, "")
	pos, err := parse(fs, rest)
	if err != nil {
		return err
	}
	stale, err := StaleJobs(s, LoadConfig(s.Home).StaleDays, pos)
	if err != nil {
		return err
	}
	if *yes {
		if err := s.CloseStale(stale); err != nil {
			return err
		}
	}
	rows := make([]map[string]any, 0, len(stale))
	var tbl [][]string
	now := time.Now()
	for _, j := range stale {
		rows = append(rows, map[string]any{"id": j.ID, "company": j.Company, "title": j.Title, "status": j.Status, "days_quiet": daysQuiet(j, now)})
		tbl = append(tbl, []string{fmt.Sprint(j.ID), trunc(j.Company, 22), trunc(j.Title, 40), j.Status, fmt.Sprint(daysQuiet(j, now))})
	}
	return emit(out, *asJSON, map[string]any{"closed": *yes, "jobs": rows}, func() {
		if len(stale) == 0 {
			fmt.Fprintln(out, "nothing stale")
			return
		}
		table(out, "ID\tCOMPANY\tTITLE\tSTATUS\tQUIET", tbl)
		if *yes {
			fmt.Fprintf(out, "%d marked no_response\n", len(stale))
		} else {
			fmt.Fprintln(out, "not changed: run again with --yes to mark all no_response, or name the ids to close only those")
		}
	})
}

// StaleJobs are the applied and interviewing jobs quiet for staleDays or more; ids, when given, narrow them.
func StaleJobs(s *store.Store, staleDays int, ids []string) ([]store.Job, error) {
	if staleDays <= 0 {
		return nil, errors.New("stale_days is off in config.yaml")
	}
	st, err := s.Stats(LoadConfig(s.Home).FollowUpDays, staleDays)
	if err != nil || len(ids) == 0 {
		return st.Stale, err
	}
	var out []store.Job
	for _, id := range ids {
		j, err := s.ResolveJob(id)
		if err != nil {
			return nil, err
		}
		found := false
		for _, sj := range st.Stale {
			found = found || sj.ID == j.ID
		}
		if !found {
			return nil, fmt.Errorf("#%d %s is not stale", j.ID, j.Company)
		}
		out = append(out, j)
	}
	return out, nil
}

func daysQuiet(j store.Job, now time.Time) int {
	at, err := time.Parse(time.RFC3339, j.UpdatedAt)
	if err != nil {
		return 0
	}
	return int(now.Sub(at).Hours() / 24)
}
