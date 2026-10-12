package web

import (
	"path/filepath"
	"strings"

	"github.com/zoolcoder/scoutjobs/internal/apply"
)

// gateLabels names each sending check in plain words.
var gateLabels = map[string]string{
	"documents":      "Resume and cover letter are written",
	"format":         "Resume keeps the layout of your CV",
	"fit_review":     "Fit checked against the job",
	"coverage":       "Every must-have is covered",
	"grounding":      "Every claim is backed by your profile",
	"ats_text":       "Hiring software can read the resume",
	"cover_template": "Cover letter follows your template",
	"duplicates":     "Not applied here before",
	"filenames":      "File names look professional",
}

func gateLabel(name string) string {
	if l, ok := gateLabels[name]; ok {
		return l
	}
	return strings.ReplaceAll(name, "_", " ")
}

// stepLabels names each assistant step in plain words.
var stepLabels = map[string]string{
	"fit":    "Read the posting and check your fit",
	"tailor": "Tailor your resume",
	"cover":  "Write a cover letter",
	"review": "Double-check the drafts",
	"export": "Make the resume PDF",
	"ats":    "Check it against hiring software",
	"check":  "Run the final checks",
	"draft":  "Prepare the e-mail",
}

func stepLabel(name string) string {
	if l, ok := stepLabels[name]; ok {
		return l
	}
	return name
}

// plainDetail drops the command hints a detail line may end with.
func plainDetail(s string) string {
	if i := strings.Index(s, "`"); i >= 0 {
		s = strings.TrimRight(s[:i], " ,:;")
		s = strings.TrimSuffix(strings.TrimSuffix(s, ", then"), ": run the draft-reviewer")
	}
	return s
}

// docCard is one draft on the job page: the editable file and the exports made from it.
type docCard struct {
	Label, Icon, Step string
	Main              doc
	Extras            []doc
}

func docCards(all []doc) []docCard {
	cards := []docCard{{Label: "Resume", Icon: "doc", Step: "tailor"}, {Label: "Cover letter", Icon: "doc", Step: "cover"}}
	for _, d := range all {
		i := 0
		if strings.HasPrefix(d.Name, "cover-letter") {
			i = 1
		}
		if strings.HasSuffix(d.Name, ".md") {
			cards[i].Main = d
		} else if d.Exists {
			cards[i].Extras = append(cards[i].Extras, d)
		}
	}
	return cards
}

// fitPoint is one requirement of the fit review, sorted strongest first and cut to a few.
type fitPoint struct {
	Text, Tone, Evidence string
}

func fitPoints(r apply.FitReview, n int) []fitPoint {
	var strong, weak []fitPoint
	for _, q := range r.Requirements {
		switch q.Strength {
		case "direct", "adjacent":
			strong = append(strong, fitPoint{q.Requirement, "ok", q.Evidence})
		case "partial":
			weak = append(weak, fitPoint{q.Requirement, "part", q.Evidence})
		default:
			weak = append(weak, fitPoint{q.Requirement, "gap", q.Evidence})
		}
	}
	out := append(strong, weak...)
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// fileLabels names the files of an application folder in plain words.
var fileLabels = map[string]string{
	"resume.md": "Resume", "resume.pdf": "Resume PDF", "resume.docx": "Resume (Word)",
	"cover-letter.md": "Cover letter", "cover-letter.pdf": "Cover letter PDF", "cover-letter.docx": "Cover letter (Word)",
	"posting.md": "Job posting", "fit-review.json": "Fit review", "ats-result.json": "Hiring software check",
	"review-result.json": "Draft review", "provenance.json": "Check record", "email.json": "E-mail draft",
}

func fileLabel(name string) string {
	if l, ok := fileLabels[name]; ok {
		return l
	}
	return name
}

// fileNotes says in a few words what each application file is for.
var fileNotes = map[string]string{
	"resume.md": "Tailored to this job", "cover-letter.md": "Written for this job",
	"resume.pdf": "Ready to send", "cover-letter.pdf": "Ready to send", "resume.docx": "For forms that want Word", "cover-letter.docx": "Built from your template",
	"posting.md": "The original job description", "fit-review.json": "Why this job fits you", "ats-result.json": "How hiring software reads your resume",
	"review-result.json": "What the review asked to fix", "provenance.json": "Which files the checks covered", "email.json": "The application e-mail",
}

// fileGroup is one heading on the Files tab.
type fileGroup struct {
	Title string
	Files []fileRow
}

type fileRow struct {
	doc
	Note     string
	Download bool
}

func fileGroups(dir string, names []string) []fileGroup {
	groups := []fileGroup{{Title: "Your application"}, {Title: "Assistant notes"}}
	for _, d := range docs(dir, names...) {
		d.Label = fileLabel(d.Name)
		row := fileRow{doc: d, Note: fileNotes[d.Name], Download: downloadable[filepath.Ext(d.Name)]}
		i := 1
		if strings.HasPrefix(d.Name, "resume") || strings.HasPrefix(d.Name, "cover-letter") {
			i = 0
		}
		groups[i].Files = append(groups[i].Files, row)
	}
	return groups
}
