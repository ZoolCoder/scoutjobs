package runner

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/apply"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

// done reports whether a step's output exists; why describes it, or names what is missing.
func (rn *Runner) done(j store.Job, name string) (bool, string) {
	dir := apply.Dir(rn.Store, j)
	switch name {
	case "fit":
		if _, err := apply.LoadReview(dir); err == nil {
			return true, apply.ReviewName
		}
		return false, apply.ReviewName
	case "tailor":
		return exists(dir, "resume.md"), "resume.md"
	case "cover":
		return exists(dir, "cover-letter.md"), "cover-letter.md"
	case "review":
		e, ok := rn.lastEvent(j, "review:")
		if !ok || e.At < modTime(dir, "resume.md", "cover-letter.md") {
			return false, "a review verdict on the current drafts"
		}
		// a fix verdict is a finished review too: the drafts were read and the findings are on the job
		return true, e.Kind + " " + e.Note
	case "export":
		pdf := filepath.Join(dir, "resume.pdf")
		st, err := os.Stat(pdf)
		if err != nil {
			return false, "resume.pdf"
		}
		md, err := os.Stat(filepath.Join(dir, "resume.md"))
		return err != nil || !md.ModTime().After(st.ModTime()), "resume.pdf"
	case "ats":
		events, _ := rn.Store.Events(j.ID, 200)
		if k, _, _ := apply.Status(rn.Store, j, events); k.ATS != nil {
			return true, fmt.Sprintf("ATS %d, content %d", k.ATS.ATSScore, k.ATS.Score)
		}
		return false, "a CV check logged for this job"
	case "draft":
		return exists(dir, apply.DraftName), apply.DraftName
	}
	return false, ""
}

// fresh is true when the output was written during this session, so the watcher does not mark a
// step done from a stale file it is about to replace.
func (rn *Runner) fresh(j store.Job, name string, since time.Time) bool {
	dir := apply.Dir(rn.Store, j)
	stamp := since.UTC().Format(time.RFC3339)
	switch name {
	case "fit":
		return modTime(dir, apply.ReviewName) >= stamp
	case "tailor":
		return modTime(dir, "resume.md") >= stamp
	case "cover":
		return modTime(dir, "cover-letter.md") >= stamp
	case "review":
		e, ok := rn.lastEvent(j, "review:")
		return ok && e.At >= stamp
	case "ats":
		e, ok := rn.lastEvent(j, "cv_check")
		return ok && e.At >= stamp
	}
	return true
}

// failed is the reason an AI step's result stops the run although the agent finished: a fix verdict.
func (rn *Runner) failed(j store.Job, name string, since time.Time) string {
	if name != "review" {
		return ""
	}
	if e, ok := rn.lastEvent(j, "review:"); ok && e.Kind == "review:fix" && e.At >= since.UTC().Format(time.RFC3339) {
		return "the draft review asks for fixes: " + e.Note
	}
	return ""
}

// local runs a deterministic step. warn is set when it finished but found blockers: they stop
// sending, not the preparation, so the PDF and the local draft are still made.
func (rn *Runner) local(j store.Job, name string) (detail string, warn bool, err error) {
	dir := apply.Dir(rn.Store, j)
	events, _ := rn.Store.Events(j.ID, 200)
	switch name {
	case "export":
		dst, size, err := apply.ExportPDF(dir, "resume.md", "")
		if err != nil {
			return "", false, err
		}
		_ = rn.Store.AddEvent(j.ID, "cv:export", filepath.Base(dst))
		detail := fmt.Sprintf("%s (%d KB)", filepath.Base(dst), size/1024)
		if rep := apply.Check(rn.Store, j, events, apply.CheckOptions{Export: true}); !rep.OK() {
			return detail + "; sending blocked by " + strings.Join(rep.Unresolved(), "; "), true, nil
		}
		return detail, false, nil
	case "check":
		rep := apply.Check(rn.Store, j, events, apply.CheckOptions{})
		if !rep.OK() {
			return "sending blocked: " + strings.Join(rep.Unresolved(), "; "), true, nil
		}
		return fmt.Sprintf("%d gates pass, %d warnings", len(rep.Gates)-rep.Warned, rep.Warned), false, nil
	case "draft":
		m, _, err := apply.Draft(rn.Store, j, rn.From, apply.DraftOptions{})
		if err != nil {
			return "", false, err
		}
		to := m.To
		if to == "" {
			to = "no recipient yet"
		}
		return "email.json to " + to + ", not sent", false, nil
	}
	return "", false, fmt.Errorf("%s is not a local step", name)
}

// Reconcile brings a job's state up to date before a run or on request: files left in a folder an
// older version or an agent spelled differently are merged, and agent results not yet recorded are.
func (rn *Runner) Reconcile(j store.Job) []string {
	var notes []string
	m, err := apply.Reconcile(rn.Store, j)
	if len(m.Moved) > 0 {
		notes = append(notes, "merged from an older folder: "+strings.Join(m.Moved, ", "))
	}
	if len(m.Conflicts) > 0 {
		notes = append(notes, "kept both copies, compare them by hand: "+strings.Join(m.Conflicts, ", "))
	}
	if err != nil {
		notes = append(notes, "folder merge: "+err.Error())
	}
	if err := rn.record(j); err != nil {
		notes = append(notes, err.Error())
	}
	return notes
}

// record turns the agent's review and ATS result files into the review event and the CV check.
func (rn *Runner) record(j store.Job) error {
	events, _ := rn.Store.Events(j.ID, 200)
	_, err1 := apply.RecordReview(rn.Store, j, events)
	_, err2 := apply.RecordATS(rn.Store, j, events)
	return errors.Join(err1, err2)
}

func (rn *Runner) lastEvent(j store.Job, prefix string) (store.Event, bool) {
	events, _ := rn.Store.Events(j.ID, 200)
	var last store.Event
	for _, e := range events {
		if strings.HasPrefix(e.Kind, prefix) && e.At >= last.At {
			last = e
		}
	}
	return last, last.Kind != ""
}

func exists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

// modTime is the newest modification of the files, as an RFC 3339 UTC string comparable with event times.
func modTime(dir string, names ...string) string {
	var newest time.Time
	for _, n := range names {
		if st, err := os.Stat(filepath.Join(dir, n)); err == nil && st.ModTime().After(newest) {
			newest = st.ModTime()
		}
	}
	if newest.IsZero() {
		return ""
	}
	return newest.UTC().Format(time.RFC3339)
}

// Finished maps each step whose output exists to what it found, for the job page.
func (rn *Runner) Finished(j store.Job) map[string]string {
	out := map[string]string{}
	for _, s := range Steps {
		if s.Name == "check" {
			continue
		}
		if ok, why := rn.done(j, s.Name); ok {
			out[s.Name] = why
		}
	}
	return out
}
