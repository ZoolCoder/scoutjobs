package apply

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/store"
)

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// folded compares folder names whatever separator an older version or an agent used:
// "dauch-manufacturing-engineer" and "dauch_manufacturing-engineer" are the same job.
func folded(name string) string { return nonAlnum.ReplaceAllString(strings.ToLower(name), "") }

// Merge is what reconciling one job's folders did.
type Merge struct {
	Moved     []string `json:"moved,omitempty"`     // legacy/name moved into the job folder
	Conflicts []string `json:"conflicts,omitempty"` // same name, different content: both kept, nothing overwritten
}

// Legacy lists the other folders under applications/ that hold files of j under another spelling.
func Legacy(s *store.Store, j store.Job) []string {
	dir := Dir(s, j)
	want := folded(filepath.Base(dir))
	entries, err := os.ReadDir(filepath.Dir(dir))
	if err != nil {
		return nil
	}
	// a folder that is another job's own folder is never taken, even when the names fold alike
	owned := map[string]bool{}
	if jobs, err := s.ListJobs(store.JobFilter{}); err == nil {
		for _, o := range jobs {
			owned[filepath.Base(Dir(s, o))] = true
		}
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && !owned[e.Name()] && folded(e.Name()) == want {
			out = append(out, filepath.Join(filepath.Dir(dir), e.Name()))
		}
	}
	return out
}

// Reconcile moves the files of legacy folders into the job folder. A file the job folder lacks is
// moved; an identical copy is dropped; a different one stays where it is and is reported, so a newer
// draft is never overwritten. An emptied legacy folder is removed.
func Reconcile(s *store.Store, j store.Job) (Merge, error) {
	var m Merge
	legacy := Legacy(s, j)
	if len(legacy) == 0 {
		return m, nil
	}
	dir := Dir(s, j)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return m, err
	}
	var errs []error
	for _, old := range legacy {
		entries, err := os.ReadDir(old)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			src, dst := filepath.Join(old, e.Name()), filepath.Join(dir, e.Name())
			label := filepath.Base(old) + "/" + e.Name()
			if _, err := os.Stat(dst); errors.Is(err, os.ErrNotExist) {
				if err := os.Rename(src, dst); err != nil {
					errs = append(errs, err)
					continue
				}
				m.Moved = append(m.Moved, label)
				continue
			}
			if same(src, dst) {
				_ = os.Remove(src)
				continue
			}
			m.Conflicts = append(m.Conflicts, label)
		}
		_ = os.Remove(old) // only succeeds once empty
	}
	if len(m.Moved) > 0 {
		_ = s.AddEvent(j.ID, "files:merged", strings.Join(m.Moved, ", "))
	}
	if len(m.Conflicts) > 0 {
		_ = s.AddEvent(j.ID, "files:conflict", "kept both, check by hand: "+strings.Join(m.Conflicts, ", "))
	}
	return m, errors.Join(errs...)
}

func same(a, b string) bool {
	x, err1 := os.ReadFile(a)
	y, err2 := os.ReadFile(b)
	return err1 == nil && err2 == nil && bytes.Equal(x, y)
}

// ReconcileAll merges the legacy folders of every job, for upgrades; it returns how many files moved.
func ReconcileAll(s *store.Store) (int, error) {
	jobs, err := s.ListJobs(store.JobFilter{})
	if err != nil {
		return 0, err
	}
	n := 0
	var errs []error
	for _, j := range jobs {
		m, err := Reconcile(s, j)
		n += len(m.Moved)
		if err != nil {
			errs = append(errs, fmt.Errorf("#%d: %w", j.ID, err))
		}
	}
	return n, errors.Join(errs...)
}

// Agent results: the agent writes these small files and ScoutJobs validates and records them, so a
// step does not depend on the agent remembering to call a command.
const (
	ReviewResult = "review-result.json"
	ATSResult    = "ats-result.json"
)

// ReviewVerdict is the draft reviewer's result.
type ReviewVerdict struct {
	Verdict string `json:"verdict"` // send | fix
	Notes   string `json:"notes"`
}

// ATSVerdict is the CV check of the tailored resume against the posting.
type ATSVerdict struct {
	Score   int64    `json:"score"`
	ATS     int64    `json:"ats"`
	Summary string   `json:"summary"`
	Gaps    []string `json:"gaps"`
}

// RecordReview logs review-result.json as the review event when it is newer than the last one logged.
func RecordReview(s *store.Store, j store.Job, events []store.Event) (bool, error) {
	dir := Dir(s, j)
	var v ReviewVerdict
	at, err := readResult(dir, ReviewResult, &v)
	if err != nil || at <= lastAt(events, "review:") {
		return false, err
	}
	if v.Verdict != "send" && v.Verdict != "fix" {
		return false, fmt.Errorf("%s: verdict must be send or fix, got %q", ReviewResult, v.Verdict)
	}
	return true, s.AddEvent(j.ID, "review:"+v.Verdict, v.Notes)
}

// RecordATS logs ats-result.json as the CV check of the job when it is newer than the last one logged.
func RecordATS(s *store.Store, j store.Job, events []store.Event) (bool, error) {
	dir := Dir(s, j)
	var v ATSVerdict
	at, err := readResult(dir, ATSResult, &v)
	if err != nil || at <= lastAt(events, "cv_check") {
		return false, err
	}
	if v.Score < 0 || v.Score > 100 || v.ATS < 0 || v.ATS > 100 {
		return false, fmt.Errorf("%s: score and ats must be 0-100", ATSResult)
	}
	file := "resume.pdf"
	if !exists(filepath.Join(dir, file)) {
		file = "resume.md"
	}
	_, err = s.AddCVCheck(store.CVCheck{File: file, Score: v.Score, ATSScore: v.ATS, Summary: v.Summary, Gaps: strings.Join(v.Gaps, ", "), JobID: j.ID})
	return err == nil, err
}

// readResult decodes an agent result; at is its modification time, comparable with event times.
func readResult(dir, name string, v any) (string, error) {
	p := filepath.Join(dir, name)
	st, err := os.Stat(p)
	if err != nil {
		return "", nil
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	return st.ModTime().UTC().Format(time.RFC3339), nil
}

func lastAt(events []store.Event, prefix string) string {
	last := ""
	for _, e := range events {
		if strings.HasPrefix(e.Kind, prefix) && e.At > last {
			last = e.At
		}
	}
	return last
}
