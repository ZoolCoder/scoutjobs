package autopilot

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/search"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

const (
	// GoneEvent marks a check that found the posting closed; LiveEvent clears the earlier ones.
	GoneEvent = "posting:gone"
	LiveEvent = "posting:live"
	// goneChecks failed checks spread over at least goneSpan close a posting, so one bad hour on
	// an ATS cannot skip a job that is still open.
	goneChecks = 3
	goneSpan   = 48 * time.Hour
	// recheckAfter keeps a job that just failed from being checked again in the same day.
	recheckAfter = 20 * time.Hour
	// verifyPerPass bounds the requests one pass makes; the cursor rotates through the rest.
	verifyPerPass = 25
	verifyCursor  = "verify_cursor"
)

// Check is what one liveness check found.
type Check struct {
	ID      int64  `json:"id"`
	Company string `json:"company"`
	Title   string `json:"title"`
	Result  string `json:"result"` // live, gone (n of 3), closed, unchecked
	Note    string `json:"note,omitempty"`
}

// Verify checks that tracked new and ranked postings are still open on their ATS, so no agent
// spends tokens scoring or drafting a closed job. A posting is skipped only after three failed
// checks two days apart; a network error or an unsupported board counts for nothing.
func Verify(ctx context.Context, s *store.Store, o Options, ids []int64, now time.Time) ([]Check, error) {
	jobs, err := verifyQueue(s, ids)
	if err != nil {
		return nil, err
	}
	var out []Check
	for _, j := range jobs {
		gone, last := goneSince(s, j.ID)
		if len(ids) == 0 && gone > 0 && now.Sub(last) < recheckAfter {
			continue
		}
		c := Check{ID: j.ID, Company: j.Company, Title: j.Title}
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		err := search.PostingLive(cctx, j.URL, nil, o.BaseURL)
		cancel()
		switch {
		case err == nil:
			c.Result = "live"
			if gone > 0 {
				_ = s.AddEvent(j.ID, LiveEvent, "posting open again")
			}
		case errors.Is(err, search.ErrGone):
			c.Result, c.Note = markGone(s, j, gone+1, last, now)
		default:
			c.Result, c.Note = "unchecked", err.Error()
		}
		out = append(out, c)
	}
	return out, nil
}

// verifyQueue picks the jobs to check: the given ones, or the next batch after the saved cursor.
func verifyQueue(s *store.Store, ids []int64) ([]store.Job, error) {
	if len(ids) > 0 {
		var out []store.Job
		for _, id := range ids {
			j, err := s.JobByID(id)
			if err != nil {
				return nil, fmt.Errorf("job %d: %w", id, err)
			}
			if j.URL != "" {
				out = append(out, j)
			}
		}
		return out, nil
	}
	var all []store.Job
	for _, st := range []string{"new", "ranked"} {
		jobs, err := s.ListJobs(store.JobFilter{Status: st})
		if err != nil {
			return nil, err
		}
		for _, j := range jobs {
			if j.URL != "" {
				all = append(all, j)
			}
		}
	}
	slices.SortFunc(all, func(a, b store.Job) int { return cmp.Compare(a.ID, b.ID) })
	var cursor int64
	_, _ = fmt.Sscan(s.Setting(verifyCursor), &cursor)
	start := 0
	for start < len(all) && all[start].ID <= cursor {
		start++
	}
	batch := append(all[start:len(all):len(all)], all[:start]...)
	if len(batch) > verifyPerPass {
		batch = batch[:verifyPerPass]
	}
	if len(batch) > 0 {
		_ = s.SetSetting(verifyCursor, fmt.Sprint(batch[len(batch)-1].ID))
	}
	return batch, nil
}

// goneSince counts the failed checks since the posting was last seen open, and when the first was.
func goneSince(s *store.Store, id int64) (int, time.Time) {
	evs, _ := s.Events(id, 0)
	n, first := 0, time.Time{}
	for _, e := range evs { // newest first
		if e.Kind == LiveEvent {
			break
		}
		if e.Kind == GoneEvent {
			n++
			first, _ = time.Parse(time.RFC3339, e.At)
		}
	}
	return n, first
}

// markGone logs a failed check and skips the job once the failures are many and far enough apart.
func markGone(s *store.Store, j store.Job, n int, first, now time.Time) (string, string) {
	if n == 1 {
		first = now
	}
	if n >= goneChecks && now.Sub(first) >= goneSpan {
		skipped := "skipped"
		if err := s.UpdateJob(j.ID, store.JobPatch{Status: &skipped}); err != nil {
			return "gone", err.Error()
		}
		_ = s.AddEvent(j.ID, GoneEvent, "closed on the ATS; skipped")
		return "closed", "skipped after " + fmt.Sprint(n) + " failed checks"
	}
	_ = s.AddEvent(j.ID, GoneEvent, fmt.Sprintf("check %d of %d", n, goneChecks))
	return fmt.Sprintf("gone (%d of %d)", n, goneChecks), ""
}
