package web

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zoolcoder/scoutjobs/internal/apply"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

// checkItem is one line of the job checklist: what is done, what needs a look, and what comes next.
type checkItem struct {
	Label  string
	State  string // done | warn | blocked | next | todo
	Detail string
	Href   string // where to look or act
	Act    string
	Files  string // the files and SHA-256 prefixes the result covered
}

type checklist struct {
	Items []checkItem
	Done  int
}

// jobChecklist reads the application state in the order an application is built, from fit to sent.
func jobChecklist(j store.Job, kit apply.Kit, gates apply.Report, events []store.Event, dir string) checklist {
	base := fmt.Sprintf("/jobs/%d", j.ID)
	file := func(name string) string { return base + "/files/" + name }
	items := []checkItem{
		item("Fit scored", j.FitScore >= 0, fmt.Sprintf("fit %d", j.FitScore), "", ""),
		item("Tailored resume", kit.Resume, "resume.md", file("resume.md"), "Preview"),
		item("Cover letter", kit.Cover, "cover-letter.md", file("cover-letter.md"), "Preview"),
	}
	review := checkItem{Label: "Draft review"}
	for _, e := range events { // newest first
		if strings.HasPrefix(e.Kind, "review:") {
			review.Detail = e.Note
			review.State = "done"
			if e.Kind == "review:fix" {
				review.State, review.Href, review.Act = "warn", base+"#docs", "Edit drafts"
			}
			break
		}
	}
	prov := apply.ProvenanceOf(dir)
	if review.State != "" {
		if prov.Review != nil {
			review.Files = covered(prov.Review.Files)
		}
		if len(prov.ReviewChanged) > 0 {
			review.State, review.Detail = "warn", "stale, changed since the review: "+strings.Join(prov.ReviewChanged, ", ")
			review.Href, review.Act = base+"#run", "Review again"
		}
	}
	items = append(items, review)
	_, err := os.Stat(filepath.Join(dir, "resume.pdf"))
	pdf := item("Resume PDF", err == nil, "resume.pdf", file("resume.pdf"), "Preview")
	switch prov.ResumePDF {
	case apply.ExportOwn:
		pdf.Detail = "resume.pdf, your own file"
	case apply.ExportOutdated:
		pdf.State, pdf.Detail = "warn", "stale, made from an older resume.md"
	}
	if e, ok := prov.Exports["resume.pdf"]; ok && pdf.State != "" {
		pdf.Files = covered(e.Files)
	}
	items = append(items, pdf)
	ats := checkItem{Label: "ATS check"}
	if c := kit.ATS; c != nil {
		ats.State, ats.Detail = "done", fmt.Sprintf("ATS %d, content %d", c.ATSScore, c.Score)
		if c.ATSScore < 70 {
			ats.State = "warn"
			if c.Gaps != "" {
				ats.Detail += ": " + c.Gaps
			}
		}
	}
	if ats.State != "" {
		if prov.ATS != nil {
			ats.Files = covered(prov.ATS.Files)
		}
		if len(prov.ATSChanged) > 0 {
			ats.State, ats.Detail = "warn", "stale, the score covers another version of "+strings.Join(prov.ATSChanged, ", ")
			ats.Href, ats.Act = base+"#run", "Score again"
		}
	}
	items = append(items, ats)
	gate := checkItem{Label: "Quality gates", State: "done", Detail: "all pass", Href: base + "#gates", Act: "Details"}
	if !gates.OK() {
		var names []string
		for _, g := range gates.Gates {
			if g.State == apply.Fail {
				names = append(names, g.Name)
			}
		}
		gate.State, gate.Detail = "blocked", "sending blocked: "+strings.Join(names, ", ")
	}
	items = append(items, gate)
	draft := checkItem{Label: "Application e-mail"}
	if kit.Draft != nil {
		draft.State, draft.Detail = "done", "draft to "+orNone(kit.Draft.To)
	}
	items = append(items, draft, item("Sent", kit.Sent, "you send it; ScoutJobs never does", "", ""))

	var c checklist
	next := false
	for i := range items {
		switch items[i].State {
		case "done":
			c.Done++
		case "":
			items[i].State = "todo"
			if !next {
				items[i].State, next = "next", true
				if items[i].Href == "" && items[i].Label != "Sent" {
					items[i].Href, items[i].Act = base+"#run", "Prepare"
				}
			}
		}
	}
	c.Items = items
	return c
}

func item(label string, done bool, detail, href, act string) checkItem {
	if !done {
		return checkItem{Label: label}
	}
	return checkItem{Label: label, State: "done", Detail: detail, Href: href, Act: act}
}

// covered lists files with their SHA-256 prefix, in a stable order.
func covered(files map[string]string) string {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for i, n := range names {
		names[i] = n + " " + apply.Short(files[n])
	}
	return strings.Join(names, " · ")
}

func orNone(s string) string {
	if s == "" {
		return "no recipient yet"
	}
	return s
}
