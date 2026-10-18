// Package autopilot does the daily chores while nobody watches: it runs the saved searches marked
// daily, skips postings their ATS closed, drafts the follow-ups that are due, and asks for the interview prep packs. It never sends
// anything and never submits an application.
package autopilot

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/apply"
	"github.com/zoolcoder/scoutjobs/internal/match"
	"github.com/zoolcoder/scoutjobs/internal/search"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

// Settings that switch the chores on; the daily searches are switched one by one on the search page.
const (
	FollowUpsSetting = "followup_autopilot"
	PrepSetting      = "auto_prep"
)

const (
	// searchEvery is the gap between two runs of a daily search; under 24h so a fixed time of day does not drift.
	searchEvery = 20 * time.Hour
	// matchFloor is the lowest search match score the digest tracks, the "Good match" label.
	matchFloor = 45
	// maxPerSearch caps the jobs one run of a search adds, so a broad query cannot flood the pipeline.
	maxPerSearch = 20
	// prepAhead is how far ahead an interview gets its prep pack.
	prepAhead = 7 * 24 * time.Hour
	// DigestEvent marks a job a daily search added.
	DigestEvent = "digest"
)

// Options carry what the chores need from config.yaml and the server.
type Options struct {
	FollowUpDays int
	BaseURL      map[string]string // test override per search source
	// Prep queues the interview prep pack for a job and reports whether it was queued; nil means no
	// AI assistant is reachable here, so no pack is asked for.
	Prep func(store.Job) bool
}

// SearchRun is one daily search that ran.
type SearchRun struct {
	Name   string   `json:"name"`
	Found  int      `json:"found"`
	Added  int      `json:"added"`
	Errors []string `json:"errors,omitempty"`
}

// Report is what one pass did.
type Report struct {
	Searches  []SearchRun `json:"searches"`
	Added     int         `json:"added"`
	FollowUps []string    `json:"follow_ups"` // "Globex: followup"
	Prep      []string    `json:"prep"`       // companies whose prep pack was queued
	Closed    []string    `json:"closed"`     // postings skipped because the ATS closed them
	Errors    []string    `json:"errors,omitempty"`
}

// Daily runs every chore that is due now. Running it twice in a row does nothing the second time.
func Daily(ctx context.Context, s *store.Store, o Options, now time.Time) Report {
	var r Report
	r.searches(ctx, s, o, now)
	r.verify(ctx, s, o, now)
	if s.Setting(FollowUpsSetting) == "on" {
		r.followUps(s, o, now)
	}
	if s.Setting(PrepSetting) == "on" && o.Prep != nil {
		r.prep(s, o, now)
	}
	return r
}

func (r *Report) searches(ctx context.Context, s *store.Store, o Options, now time.Time) {
	saved, err := s.SavedSearches()
	if err != nil {
		r.Errors = append(r.Errors, err.Error())
		return
	}
	for _, x := range saved {
		if !x.Daily || !due(x.LastRun, now) {
			continue
		}
		run := runSearch(ctx, s, o, x, now)
		_ = s.MarkSearchRun(x.ID, now.UTC().Format(time.RFC3339))
		r.Searches = append(r.Searches, run)
		r.Added += run.Added
	}
}

func due(last string, now time.Time) bool {
	t, err := time.Parse(time.RFC3339, last)
	return err != nil || now.Sub(t) >= searchEvery
}

// runSearch runs one saved search the way the search page does, and tracks the good matches that
// are not in the pipeline yet.
func runSearch(ctx context.Context, s *store.Store, o Options, x store.SavedSearch, now time.Time) SearchRun {
	run := SearchRun{Name: x.Name}
	f := search.ParseFilter(x.Filters)
	pr := match.Load(s)
	if f.Location != "" {
		pr.Places = search.SplitList(f.Location)
	}
	ranked := !pr.Empty()
	opts := search.Options{Queries: search.SplitList(x.Queries), Boards: search.SplitList(x.Boards), Remote: x.Remote,
		Off: search.SplitList(s.Setting(search.OffSetting)), Limit: 50, BaseURL: o.BaseURL}
	opts.Keep = func(p search.Posting) bool {
		if !f.Keep(p, now) {
			return false
		}
		if !ranked {
			return true
		}
		m := pr.Score(p)
		return len(m.Hard) == 0 && m.Score >= matchFloor
	}
	if ranked {
		opts.Rank = func(p search.Posting) int { return pr.Score(p).Score }
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	res := search.Run(ctx, opts)
	run.Found, run.Errors = len(res.Postings), res.Errors
	for _, p := range res.Postings {
		if run.Added == maxPerSearch {
			break
		}
		if known(s, p) {
			continue
		}
		j, created, err := s.UpsertJob(p.Job())
		if err == nil {
			err = apply.SavePosting(s, j, p.Description)
		}
		if err != nil {
			run.Errors = append(run.Errors, err.Error())
			continue
		}
		if created {
			run.Added++
			_ = s.AddEvent(j.ID, DigestEvent, x.Name)
		}
	}
	if run.Added > 0 {
		_, _ = apply.Rank(s, false)
	}
	return run
}

// known reports whether the posting is tracked already, by company and title or by its URL.
func known(s *store.Store, p search.Posting) bool {
	if _, err := s.JobByKey(store.JobKey(p.Company, p.Title)); err == nil {
		return true
	}
	_, ok := s.JobByURL(p.URL)
	return ok
}

// verify checks a batch of tracked postings and skips the ones closed for good.
func (r *Report) verify(ctx context.Context, s *store.Store, o Options, now time.Time) {
	checks, err := Verify(ctx, s, o, nil, now)
	if err != nil {
		r.Errors = append(r.Errors, err.Error())
	}
	for _, c := range checks {
		if c.Result == "closed" {
			r.Closed = append(r.Closed, c.Company+": "+c.Title)
		}
	}
}

// followUps saves the follow-up and thank-you e-mails that are due and not written yet, for the
// user to review. They are never sent.
func (r *Report) followUps(s *store.Store, o Options, now time.Time) {
	for _, st := range []string{"applied", "interview"} {
		jobs, err := s.ListJobs(store.JobFilter{Status: st})
		if err != nil {
			r.Errors = append(r.Errors, err.Error())
			return
		}
		for _, j := range jobs {
			for _, p := range apply.Proposals(s, j, o.FollowUpDays, now) {
				if p.Saved || (p.Kind != "followup" && p.Kind != "thanks") {
					continue
				}
				if err := apply.SaveProposal(s, j, p.Kind, p.Msg); err != nil {
					r.Errors = append(r.Errors, fmt.Sprintf("%s: %v", j.Company, err))
					continue
				}
				r.FollowUps = append(r.FollowUps, j.Company+": "+p.Kind)
			}
		}
	}
}

// prep asks for a prep pack for each interview in the coming week that has none yet.
func (r *Report) prep(s *store.Store, o Options, now time.Time) {
	ivs, err := s.Interviews(0, true)
	if err != nil {
		r.Errors = append(r.Errors, err.Error())
		return
	}
	horizon := now.Add(prepAhead).Format("2006-01-02T15:04")
	seen := map[int64]bool{}
	for _, iv := range ivs {
		if seen[iv.JobID] || iv.At == "" || iv.At > horizon {
			continue
		}
		seen[iv.JobID] = true
		j, err := s.JobByID(iv.JobID)
		if err != nil || hasPrep(s, j, iv) {
			continue
		}
		if o.Prep(j) {
			r.Prep = append(r.Prep, j.Company)
		}
	}
}

// hasPrep reports whether the prep sheet was written after the interview was booked.
func hasPrep(s *store.Store, j store.Job, iv store.Interview) bool {
	st, err := os.Stat(filepath.Join(apply.Dir(s, j), "interview-prep.md"))
	return err == nil && st.ModTime().UTC().Format(time.RFC3339) >= iv.CreatedAt
}

// Summary says in one line what a pass did, for the CLI and the activity log.
func (r Report) Summary() string {
	var parts []string
	if len(r.Searches) > 0 {
		parts = append(parts, fmt.Sprintf("%d daily %s, %d new %s", len(r.Searches), plural(len(r.Searches), "search", "searches"), r.Added, plural(r.Added, "job", "jobs")))
	}
	if n := len(r.FollowUps); n > 0 {
		parts = append(parts, fmt.Sprintf("%d %s drafted", n, plural(n, "follow-up", "follow-ups")))
	}
	if n := len(r.Closed); n > 0 {
		parts = append(parts, fmt.Sprintf("%d closed %s skipped", n, plural(n, "posting", "postings")))
	}
	if n := len(r.Prep); n > 0 {
		parts = append(parts, fmt.Sprintf("%d prep %s queued", n, plural(n, "pack", "packs")))
	}
	if len(parts) == 0 {
		return "nothing due"
	}
	return strings.Join(parts, "; ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
