package web

import (
	"fmt"
	"os"
	"path/filepath"
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
	items = append(items, review)
	_, err := os.Stat(filepath.Join(dir, "resume.pdf"))
	items = append(items, item("Resume PDF", err == nil, "resume.pdf", file("resume.pdf"), "Preview"))
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

func orNone(s string) string {
	if s == "" {
		return "no recipient yet"
	}
	return s
}
