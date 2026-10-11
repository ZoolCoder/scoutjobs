package apply

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/store"
)

// Quality gates run before an application leaves the machine. Each gate is deterministic
// and explains itself; a failed gate blocks `email send` unless the user skips checks on
// purpose, and the skip is logged. Warnings never block.

const (
	Pass = "pass"
	Warn = "warn"
	Fail = "fail"
	Skip = "skip"
)

type Gate struct {
	Name   string   `json:"name"`
	State  string   `json:"state"`
	Detail string   `json:"detail"`
	Items  []string `json:"items,omitempty"`
}

type Report struct {
	Gates  []Gate `json:"gates"`
	Failed int    `json:"failed"`
	Warned int    `json:"warned"`
	Acked  int    `json:"acknowledged,omitempty"`
}

// OK is true when nothing blocks.
func (r Report) OK() bool { return r.Failed == 0 }

// Unresolved lists every failed gate with its reason, for the refusal message and the skip event.
func (r Report) Unresolved() []string {
	var out []string
	for _, g := range r.Gates {
		if g.State == Fail {
			out = append(out, g.Name+": "+g.Detail)
		}
	}
	return out
}

// CheckOptions narrows the run. Export skips the gates that need a finished kit, so
// `cv export` can produce the PDF the ATS gate later inspects.
type CheckOptions struct {
	Export bool
	// AllowAcknowledged lets an acknowledged coverage gap stop counting as a failure (portal
	// applications); email send never sets it.
	AllowAcknowledged bool
}

var (
	allowedExt = map[string]bool{".pdf": true, ".docx": true, ".md": true, ".txt": true, ".json": true, ".eml": true}
	sloppyName = regexp.MustCompile(`(?i)(\(\d+\)|\bcopy\b|\bfinal\b|\bv\d+\b|\bnew\b|\bold\b|\bdraft\b|\bbackup\b|\buntitled\b)`)
)

// Check runs every gate on the application folder of j.
func Check(s *store.Store, j store.Job, events []store.Event, o CheckOptions) Report {
	dir := Dir(s, j)
	drafts := map[string]string{}
	for _, n := range []string{"resume.md", "cover-letter.md"} {
		if b, err := os.ReadFile(filepath.Join(dir, n)); err == nil {
			drafts[n] = string(b)
		}
	}
	var r Report
	add := func(g Gate) {
		switch g.State {
		case Fail:
			r.Failed++
		case Warn:
			r.Warned++
		}
		r.Gates = append(r.Gates, g)
	}
	review, reviewErr := LoadReview(dir)
	if !o.Export {
		add(gateDocuments(dir, drafts))
	}
	add(gateFormat(s.Home, drafts["resume.md"]))
	add(gateFitReview(j, review, reviewErr))
	add(gateCoverage(review, reviewErr, drafts))
	add(gateGrounding(dir, events, drafts))
	add(gateDuplicates(s, dir, drafts))
	if !o.Export {
		add(gateATS(dir))
		add(gateFilenames(dir))
	}
	if g, ok := gateCover(dir); ok {
		add(g)
	}
	if o.AllowAcknowledged {
		r.allowAcknowledged(events, review, drafts)
	}
	return r
}

func gateDocuments(dir string, drafts map[string]string) Gate {
	var missing []string
	if !exists(filepath.Join(dir, "resume.md")) && !exists(filepath.Join(dir, "resume.pdf")) && !exists(filepath.Join(dir, "resume.docx")) {
		missing = append(missing, "resume.md or resume.pdf")
	}
	if _, ok := drafts["cover-letter.md"]; !ok {
		missing = append(missing, "cover-letter.md")
	}
	for n, body := range drafts {
		if len(strings.TrimSpace(body)) < 200 {
			missing = append(missing, n+" is under 200 characters")
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		return Gate{"documents", Fail, "required documents missing: " + strings.Join(missing, ", "), missing}
	}
	return Gate{"documents", Pass, "tailored resume and cover letter present", nil}
}

// gateFormat keeps every tailored resume in the layout of the main CV: same sections, same order,
// same heading levels. Tailoring changes the content, not the shape.
func gateFormat(home, resume string) Gate {
	main, err := os.ReadFile(filepath.Join(home, "cv", "resume.md"))
	if err != nil || resume == "" {
		return Gate{"format", Pass, "no main CV to compare with", nil}
	}
	want, got := Outline(string(main)), Outline(resume)
	var diff []string
	have := map[string]bool{}
	for _, h := range got {
		have[h] = true
	}
	for _, h := range want {
		if !have[h] {
			diff = append(diff, "missing section "+h)
		}
		delete(have, h)
	}
	for _, h := range got {
		if have[h] {
			diff = append(diff, "section not in the main CV "+h)
		}
	}
	if len(diff) == 0 && strings.Join(want, "|") != strings.Join(got, "|") {
		diff = append(diff, "sections are in a different order than the main CV")
	}
	if len(diff) > 0 {
		return Gate{"format", Warn, "resume.md does not follow the layout of cv/resume.md", diff}
	}
	return Gate{"format", Pass, "same sections and order as the main CV", nil}
}

// Outline is the section headings of a Markdown CV below the name, with their level: "## Experience".
func Outline(md string) []string {
	var out []string
	for _, l := range strings.Split(md, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "##") {
			level, text, _ := strings.Cut(l, " ")
			out = append(out, level+" "+strings.ToLower(strings.TrimSpace(text)))
		}
	}
	return out
}

func gateFitReview(j store.Job, review FitReview, err error) Gate {
	if j.Veto != "" {
		return Gate{"fit_review", Fail, "hard veto on the job: " + j.Veto, []string{j.Veto}}
	}
	if os.IsNotExist(err) {
		return Gate{"fit_review", Warn, "no " + ReviewName + ": run the fit-review skill so every claim maps to evidence", nil}
	}
	if err != nil {
		return Gate{"fit_review", Fail, err.Error(), nil}
	}
	if err := review.Validate(); err != nil {
		return Gate{"fit_review", Fail, err.Error(), nil}
	}
	if v := review.VetoText(); v != "" {
		return Gate{"fit_review", Fail, "hard veto: " + v, []string{v}}
	}
	return Gate{"fit_review", Pass, fmt.Sprintf("%d requirements reviewed, no veto, score %d", len(review.Requirements), review.Score), nil}
}

func gateCoverage(review FitReview, err error, drafts map[string]string) Gate {
	if err != nil {
		return Gate{"coverage", Skip, "needs " + ReviewName, nil}
	}
	if gaps := review.Gaps(); len(gaps) > 0 {
		return Gate{"coverage", Fail, fmt.Sprintf("%d must-have requirement(s) without evidence", len(gaps)), gaps}
	}
	return gateWording(review, drafts)
}

// gateWording wants the agreed wording of every evidenced must-have in the drafts.
func gateWording(review FitReview, drafts map[string]string) Gate {
	if len(drafts) == 0 {
		return Gate{"coverage", Warn, "no markdown drafts to match the agreed wording against", nil}
	}
	text := ""
	for _, b := range drafts {
		text += " " + fold(b)
	}
	var missing []string
	for _, q := range review.Requirements {
		if q.Kind != "must" || q.Wording == "" {
			continue
		}
		if !strings.Contains(text, fold(q.Wording)) {
			missing = append(missing, q.Requirement+" → "+q.Wording)
		}
	}
	if len(missing) > 0 {
		return Gate{"coverage", Fail, fmt.Sprintf("%d must-have(s) have agreed wording that is not in the drafts", len(missing)), missing}
	}
	return Gate{"coverage", Pass, "every must-have is covered and its wording is in the drafts", nil}
}

// gateGrounding wants a draft-reviewer verdict newer than the last edit of the drafts.
func gateGrounding(dir string, events []store.Event, drafts map[string]string) Gate {
	if len(drafts) == 0 {
		return Gate{"grounding", Skip, "no markdown drafts", nil}
	}
	var latest store.Event
	for _, e := range events {
		if (e.Kind == "review:send" || e.Kind == "review:fix") && e.At > latest.At {
			latest = e
		}
	}
	if latest.Kind == "" {
		return Gate{"grounding", Fail, "no reviewer verdict: run the draft-reviewer, then `scoutjobs job review <id> --verdict send`", nil}
	}
	if latest.Kind == "review:fix" {
		return Gate{"grounding", Fail, "last review asked for fixes: " + latest.Note, nil}
	}
	at, err := time.Parse(time.RFC3339, latest.At)
	if err != nil {
		return Gate{"grounding", Fail, "cannot read the review time " + latest.At, nil}
	}
	stale, known := ReviewChanges(dir)
	if !known { // a review recorded before provenance: compare modification times
		for n := range drafts {
			if st, err := os.Stat(filepath.Join(dir, n)); err == nil && st.ModTime().After(at.Add(time.Second)) {
				stale = append(stale, n)
			}
		}
		sort.Strings(stale)
	}
	if len(stale) > 0 {
		return Gate{"grounding", Fail, "edited after the last review: " + strings.Join(stale, ", "), stale}
	}
	return Gate{"grounding", Pass, "reviewed " + latest.At[:10] + ", drafts unchanged since", nil}
}

// gateDuplicates catches a paragraph pasted twice and a cover letter reused from another application.
func gateDuplicates(s *store.Store, dir string, drafts map[string]string) Gate {
	var hits []string
	names := make([]string, 0, len(drafts))
	for n := range drafts {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		seen := map[string]bool{}
		for _, p := range paragraphs(drafts[n]) {
			if len(p) < 60 {
				continue
			}
			if seen[p] {
				hits = append(hits, n+": repeated paragraph starting "+quote(p))
				continue
			}
			seen[p] = true
		}
	}
	if cover, ok := drafts["cover-letter.md"]; ok {
		mine := fold(cover)
		others, _ := filepath.Glob(filepath.Join(s.Home, "applications", "*", "cover-letter.md"))
		for _, p := range others {
			if filepath.Dir(p) == dir {
				continue
			}
			if b, err := os.ReadFile(p); err == nil && fold(string(b)) == mine {
				hits = append(hits, "cover-letter.md is identical to "+filepath.Base(filepath.Dir(p))+"/cover-letter.md")
			}
		}
	}
	if len(hits) > 0 {
		return Gate{"duplicates", Fail, fmt.Sprintf("%d duplicate(s)", len(hits)), hits}
	}
	return Gate{"duplicates", Pass, "no repeated paragraphs, cover letter unique", nil}
}

// gateCover holds a letter built from a Word template to the template check, and to the words the
// review read: a cover letter edited after the build no longer matches its DOCX and PDF.
func gateCover(dir string) (Gate, bool) {
	rec := LoadProvenance(dir).Cover
	if rec == nil {
		return Gate{}, false
	}
	if changed := CoverChanges(dir); len(changed) > 0 {
		return Gate{"cover_template", Fail, "changed after the cover letter was built, so the DOCX, PDF and Markdown may differ: " + strings.Join(changed, ", ") + "; run `scoutjobs cv cover <id>`", changed}, true
	}
	switch rec.State {
	case CoverFail:
		return Gate{"cover_template", Fail, "the cover letter does not fit its template", rec.Problems}, true
	case CoverWarn:
		return Gate{"cover_template", Warn, rec.Summary(), nil}, true
	}
	return Gate{"cover_template", Pass, rec.Summary(), nil}, true
}

// gateATS inspects resume.pdf: a text layer with fonts, no encryption, sane size.
func gateATS(dir string) Gate {
	p := filepath.Join(dir, "resume.pdf")
	b, err := os.ReadFile(p)
	if err != nil {
		if exists(filepath.Join(dir, "resume.md")) {
			return Gate{"ats_text", Warn, "no resume.pdf yet: `scoutjobs cv export <id>` renders resume.md", nil}
		}
		return Gate{"ats_text", Skip, "no resume.pdf", nil}
	}
	var problems []string
	if !bytes.HasPrefix(b, []byte("%PDF-")) {
		problems = append(problems, "not a PDF")
	}
	if bytes.Contains(b, []byte("/Encrypt")) {
		problems = append(problems, "encrypted: parsers cannot read it")
	}
	if !bytes.Contains(b, []byte("/Font")) || !bytes.Contains(b, []byte("BT")) {
		problems = append(problems, "no text layer: looks like a scan or an image export")
	}
	if len(problems) > 0 {
		return Gate{"ats_text", Fail, "resume.pdf " + strings.Join(problems, "; "), problems}
	}
	if len(b) > 5<<20 {
		return Gate{"ats_text", Warn, fmt.Sprintf("resume.pdf is %d MB, many portals cap at 5", len(b)>>20), nil}
	}
	return Gate{"ats_text", Pass, "resume.pdf has a text layer and is not encrypted", nil}
}

// gateFilenames: what a recruiter sees in the attachment list.
func gateFilenames(dir string) Gate {
	var bad, odd []string
	for _, f := range Files(dir) {
		n := f.Name
		if n == ReviewName || n == "email.json" || n == "email-sent.eml" {
			continue
		}
		for _, c := range n {
			if c > 126 || c < 32 {
				bad = append(bad, n+": non-ASCII character")
				break
			}
		}
		if !allowedExt[strings.ToLower(filepath.Ext(n))] {
			bad = append(bad, n+": unexpected type")
		}
		if strings.Contains(n, " ") || sloppyName.MatchString(strings.TrimSuffix(n, filepath.Ext(n))) {
			odd = append(odd, n)
		}
	}
	if len(bad) > 0 {
		return Gate{"filenames", Fail, strings.Join(bad, "; "), bad}
	}
	if len(odd) > 0 {
		return Gate{"filenames", Warn, "rename before attaching: " + strings.Join(odd, ", "), odd}
	}
	return Gate{"filenames", Pass, "plain ASCII names, known types", nil}
}

var spaceRe = regexp.MustCompile(`\s+`)

// fold lowercases and collapses whitespace so wording matches across line breaks and markdown emphasis.
func fold(s string) string {
	s = strings.NewReplacer("**", "", "*", "", "_", "", "`", "").Replace(s)
	return strings.TrimSpace(spaceRe.ReplaceAllString(strings.ToLower(s), " "))
}

func paragraphs(s string) []string {
	var out []string
	for _, block := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n\n") {
		block = strings.TrimSpace(block)
		if block == "" || strings.HasPrefix(block, "#") {
			continue
		}
		if strings.HasPrefix(block, "-") || strings.HasPrefix(block, "*") {
			for _, line := range strings.Split(block, "\n") {
				out = append(out, fold(strings.TrimLeft(line, "-* \t")))
			}
			continue
		}
		out = append(out, fold(block))
	}
	return out
}

func quote(p string) string {
	r := []rune(p)
	if len(r) > 40 {
		r = r[:40]
	}
	return `"` + string(r) + `…"`
}
