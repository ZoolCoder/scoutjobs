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
		return e.Kind == "review:send", e.Kind + " " + e.Note
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

// local runs a deterministic step.
func (rn *Runner) local(j store.Job, name string) (string, error) {
	dir := apply.Dir(rn.Store, j)
	events, _ := rn.Store.Events(j.ID, 200)
	switch name {
	case "export":
		if rep := apply.Check(rn.Store, j, events, apply.CheckOptions{Export: true}); !rep.OK() {
			return "", errors.New(strings.Join(rep.Unresolved(), "; "))
		}
		dst, size, err := apply.ExportPDF(dir, "resume.md", "")
		if err != nil {
			return "", err
		}
		_ = rn.Store.AddEvent(j.ID, "cv:export", filepath.Base(dst))
		return fmt.Sprintf("%s (%d KB)", filepath.Base(dst), size/1024), nil
	case "check":
		rep := apply.Check(rn.Store, j, events, apply.CheckOptions{})
		if !rep.OK() {
			return "", errors.New(strings.Join(rep.Unresolved(), "; "))
		}
		return fmt.Sprintf("%d gates pass, %d warnings", len(rep.Gates)-rep.Warned, rep.Warned), nil
	case "draft":
		m, _, err := apply.Draft(rn.Store, j, rn.From, apply.DraftOptions{})
		if err != nil {
			return "", err
		}
		to := m.To
		if to == "" {
			to = "no recipient yet"
		}
		return "email.json to " + to + ", not sent", nil
	}
	return "", fmt.Errorf("%s is not a local step", name)
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
