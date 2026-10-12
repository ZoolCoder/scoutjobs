package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/apply"
	"github.com/zoolcoder/scoutjobs/internal/search"
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
		if rn.wordTemplate() {
			return rn.coverBuilt(dir)
		}
		return exists(dir, "cover-letter.md"), "cover-letter.md"
	case "review":
		e, ok := rn.lastEvent(j, "review:")
		if !ok {
			return false, "a review verdict on the current drafts"
		}
		if changed, known := apply.ReviewChanges(dir); known && len(changed) > 0 {
			return false, "changed since the review: " + strings.Join(changed, ", ")
		} else if !known && e.At < modTime(dir, "resume.md", "cover-letter.md") {
			return false, "a review verdict on the current drafts"
		}
		// a fix verdict is a finished review too: the drafts were read and the findings are on the job
		return true, e.Kind + " " + e.Note
	case "export":
		return rn.exported(dir)
	case "ats":
		events, _ := rn.Store.Events(j.ID, 200)
		k, _, _ := apply.Status(rn.Store, j, events)
		if k.ATS == nil {
			return false, "a CV check logged for this job"
		}
		if changed, _ := apply.ATSChanges(dir); len(changed) > 0 {
			return false, "changed since the score: " + strings.Join(changed, ", ")
		}
		return true, fmt.Sprintf("ATS %d, content %d", k.ATS.ATSScore, k.ATS.Score)
	case "draft":
		return exists(dir, apply.DraftName), apply.DraftName
	case "prep":
		return rn.prepDone(j, dir)
	}
	return false, ""
}

// PrepName is the prep sheet the prep step writes in the application folder.
const PrepName = "interview-prep.md"

// prepItems are the checklist keys a finished prep pack covers.
var prepItems = []string{"company", "posting", "questions"}

// prepDone reports whether the prep sheet was written for the job's next open interview.
func (rn *Runner) prepDone(j store.Job, dir string) (bool, string) {
	st, err := os.Stat(filepath.Join(dir, PrepName))
	if err != nil {
		return false, PrepName
	}
	iv, ok := nextInterview(rn.Store, j.ID)
	if ok && st.ModTime().UTC().Format(time.RFC3339) < iv.CreatedAt {
		return false, PrepName + " for the " + iv.Stage + " interview"
	}
	return true, PrepName
}

// nextInterview is the job's earliest interview still to come, else its latest one without an outcome.
// Interview times are local wall time, so they compare as text with now in the same layout.
func nextInterview(s *store.Store, jobID int64) (store.Interview, bool) {
	ivs, _ := s.Interviews(jobID, true)
	now := time.Now().Format("2006-01-02T15:04")
	var past store.Interview
	for _, iv := range ivs {
		if iv.Outcome != "" {
			continue
		}
		if !before(iv.At, now) {
			return iv, true
		}
		past = iv
	}
	return past, past.ID != 0
}

// before reports whether wall time at, a date or a date and time, is already past now; a date-only
// interview counts as to come for its whole day.
func before(at, now string) bool {
	if at == "" {
		return false
	}
	return at < now[:min(len(at), len(now))]
}

// tickPrep marks the checklist items a prep pack covers on the job's next interview.
func (rn *Runner) tickPrep(j store.Job) {
	iv, ok := nextInterview(rn.Store, j.ID)
	if !ok {
		return
	}
	done := iv.Prep
	for _, k := range prepItems {
		if !iv.PrepDone(k) {
			done += "," + k
		}
	}
	done = strings.TrimPrefix(done, ",")
	_ = rn.Store.UpdateInterview(iv.ID, store.InterviewPatch{Prep: &done})
}

// exported reports whether resume.pdf is the one to send: rendered from the current resume.md, or
// the user's own file, which a run never overwrites.
func (rn *Runner) exported(dir string) (bool, string) {
	st, err := os.Stat(filepath.Join(dir, "resume.pdf"))
	if err != nil {
		if rn.formattedSource() {
			return false, "resume.pdf made from " + rn.CVSource
		}
		return false, "resume.pdf"
	}
	switch apply.ExportState(dir, "resume") {
	case apply.ExportCurrent, apply.ExportOutdated:
		if rn.formattedSource() {
			return false, "resume.pdf made from " + rn.CVSource
		}
		if apply.ExportState(dir, "resume") == apply.ExportOutdated {
			return false, "resume.pdf from an older resume.md"
		}
		return true, "resume.pdf"
	case apply.ExportOwn:
		return true, "resume.pdf, your own file"
	}
	// no record of an export: an older version made it, or the user put it there
	md, err := os.Stat(filepath.Join(dir, "resume.md"))
	return rn.formattedSource() || err != nil || !md.ModTime().After(st.ModTime()), "resume.pdf"
}

// wordTemplate is true when config.yaml names a Word cover letter template: every letter is that
// file with its slots filled, never a Markdown letter of its own.
func (rn *Runner) wordTemplate() bool { return apply.IsWordTemplate(rn.CoverTemplate) }

func (rn *Runner) templatePath() string {
	if filepath.IsAbs(rn.CoverTemplate) {
		return rn.CoverTemplate
	}
	return filepath.Join(rn.Store.Home, rn.CoverTemplate)
}

// coverBuilt reports whether the cover letter was built from the template, is unchanged since, and
// kept the template. A letter not rendered for want of LibreOffice counts; the checklist warns.
func (rn *Runner) coverBuilt(dir string) (bool, string) {
	rec := apply.LoadProvenance(dir).Cover
	switch {
	case rec == nil:
		return false, "cover-letter.docx built from " + filepath.Base(rn.CoverTemplate)
	case len(apply.CoverChanges(dir)) > 0:
		return false, "a cover letter rebuilt after changes to " + strings.Join(apply.CoverChanges(dir), ", ")
	case rec.State == apply.CoverFail:
		return false, "a cover letter that fits the template: " + rec.Summary()
	}
	return true, "cover-letter.docx: " + rec.Summary()
}

// buildCover fills the template when the agent wrote new slot values but did not build the letter.
func (rn *Runner) buildCover(ctx context.Context, run *Run, j store.Job) {
	dir := apply.Dir(rn.Store, j)
	if !rn.wordTemplate() || !exists(dir, apply.SlotsName) {
		return
	}
	if rec := apply.LoadProvenance(dir).Cover; rec != nil && len(apply.CoverChanges(dir)) == 0 {
		return
	}
	rec, err := apply.BuildCover(ctx, rn.Store, j, rn.templatePath())
	if err != nil {
		run.logf("cover letter: %v", err)
		return
	}
	run.logf("cover letter from %s: %s %s", filepath.Base(rn.CoverTemplate), rec.State, rec.Summary())
}

// formattedSource is true when config.yaml names a PDF or Word CV: the final resume is made from
// it, never rendered from the Markdown draft.
func (rn *Runner) formattedSource() bool {
	ext := strings.ToLower(filepath.Ext(rn.CVSource))
	return ext != "" && ext != ".md" && ext != ".txt"
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
		if rn.wordTemplate() {
			return modTime(dir, apply.SlotsName) >= stamp
		}
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
		if rn.formattedSource() {
			return "not rendered from Markdown: your CV source is " + rn.CVSource + "; save the final resume.pdf made from it in the application folder", true, nil
		}
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

// Stale maps each step whose recorded result no longer covers the current files to what changed.
func (rn *Runner) Stale(j store.Job) map[string]string {
	dir := apply.Dir(rn.Store, j)
	out := map[string]string{}
	if _, ok := rn.lastEvent(j, "review:"); ok {
		if changed, _ := apply.ReviewChanges(dir); len(changed) > 0 {
			out["review"] = "changed since the review: " + strings.Join(changed, ", ")
		}
	}
	if apply.ExportState(dir, "resume") == apply.ExportOutdated {
		out["export"] = "resume.md changed since resume.pdf was made"
	}
	if changed, _ := apply.ATSChanges(dir); len(changed) > 0 {
		out["ats"] = "changed since the score: " + strings.Join(changed, ", ")
	}
	return out
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

// fetchPosting is search.FetchPosting; tests point it at a fake.
var fetchPosting = func(ctx context.Context, u string) (search.Posting, error) {
	return search.FetchPosting(ctx, u, nil, nil)
}

// ensurePosting saves the posting from the job's URL to posting.md before the agent reads it, so
// the fit review and the draft review check the drafts against the posting, not the board's tags.
func (rn *Runner) ensurePosting(ctx context.Context, run *Run, j store.Job) {
	if j.URL == "" || apply.HasPosting(rn.Store, j) {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	p, err := fetchPosting(ctx, j.URL)
	if err == nil {
		err = apply.SavePosting(rn.Store, j, p.Description)
	}
	switch {
	case err != nil:
		run.logf("posting.md: %v", err)
	case apply.HasPosting(rn.Store, j):
		run.logf("posting.md: saved from %s", j.URL)
	}
}
