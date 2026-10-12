package web

import (
	"strings"

	"github.com/zoolcoder/scoutjobs/internal/store"
)

// activityGroups are the Activity filters, in the order the page shows them.
var activityGroups = []string{"jobs", "status", "applications", "assistant", "emails", "interviews", "learning", "preferences", "notes"}

// eventTitles names an event kind in plain words. Kinds missing here fall back on their group.
var eventTitles = map[string]string{
	"added":                    "Job added",
	"scored":                   "Fit scored",
	"fit:review":               "Fit reviewed",
	"status":                   "Status changed",
	"interview":                "Interview updated",
	"practice:drill":           "Practice drill",
	"practice:mock_behavioral": "Mock interview: behavioral",
	"practice:mock_sysdesign":  "Mock interview: system design",
	"practice:mock_coding":     "Mock interview: coding",
	"email:draft":              "Email drafted",
	"email:sent":               "Email sent",
	"email:gmail_draft":        "Gmail draft created",
	"email:proposal":           "Email suggested",
	"cv:export":                "CV exported",
	"cv:cover":                 "Cover letter written",
	"cv_check":                 "CV checked",
	"review:fix":               "Draft review asked for fixes",
	"review:send":              "Draft review: ready to send",
	"pref:proposed":            "Preference suggested",
	"pref:confirmed":           "Preference confirmed",
	"pref:rejected":            "Preference rejected",
	"pref:edited":              "Preference changed",
	"run:fit":                  "Assistant scored the fit",
	"run:tailor":               "Assistant tailored your CV",
	"run:cover":                "Assistant wrote a cover letter",
	"run:draft":                "Assistant drafted an email",
	"run:review":               "Assistant reviewed the drafts",
	"run:check":                "Assistant checked the drafts",
	"run:ats":                  "Assistant ran an ATS check",
	"run:export":               "Assistant exported your CV",
	"run:done":                 "Assistant finished",
	"run:failed":               "Assistant run failed",
	"learning":                 "Learning logged",
	"note":                     "Note",
}

// eventGroup files a kind under one of activityGroups.
func eventGroup(kind string) string {
	k, _, _ := strings.Cut(kind, ":")
	switch k {
	case "added", "scored", "fit", "decision":
		return "jobs"
	case "status":
		return "status"
	case "cv", "cv_check", "review":
		return "applications"
	case "run":
		return "assistant"
	case "email", "gmail":
		return "emails"
	case "interview", "practice":
		return "interviews"
	case "learning":
		return "learning"
	case "pref":
		return "preferences"
	}
	return "notes"
}

// activityRow is one event as the Activity page shows it: a plain summary, and the raw data behind it.
type activityRow struct {
	store.Event
	Group, Name, Tone string
	Long              bool
	Items             []evItem // the checks or parts the event is made of
	Detail            string   // the full note, when the summary shortens it
}

// evItem is one part of an event, such as a check that did not pass.
type evItem struct {
	Label, Text string
}

func activityRowOf(e store.Event) activityRow {
	row := activityRow{Event: e, Group: eventGroup(e.Kind), Name: eventTitles[e.Kind]}
	if row.Name == "" {
		row.Name = strings.ToUpper(row.Group[:1]) + row.Group[1:]
	}
	note := strings.TrimSpace(e.Note)
	switch {
	case e.Kind == "status":
		from, to, ok := strings.Cut(note, "->")
		if ok {
			row.Note = "From " + stageName(from) + " to " + stageName(to) + "."
		}
	case e.Kind == "run:done":
		agent, msg, _ := strings.Cut(note, " ")
		row.Note = agentTitle(agent) + " finished. Check the drafts before you send anything."
		row.Detail = msg
	case e.Kind == "run:cancelled":
		row.Name, row.Note = "Assistant stopped", "You stopped the run. Drafts already written stay."
	case e.Kind == "run:failed", strings.HasPrefix(note, "failed: "):
		row.Tone = "failed"
		if e.Kind != "run:failed" {
			row.Name = stepLabel(strings.TrimPrefix(e.Kind, "run:")) + ": did not finish"
		}
		row.Note, row.Detail = firstSentence(strings.TrimPrefix(note, "failed: ")), note
	case strings.Contains(note, "sending blocked"):
		head, gates, _ := strings.Cut(note, "sending blocked")
		row.Items = gateItems(strings.TrimLeft(strings.TrimPrefix(gates, " by"), ": "))
		row.Tone = "warn"
		row.Name = "Checks found " + plural(len(row.Items), "a problem", "problems")
		row.Note = "Not ready to send: fix " + plural(len(row.Items), "this", "these") + " first."
		if head = strings.TrimRight(head, "; "); head != "" {
			row.Note = head + ". " + row.Note
		}
	case e.Kind == "run:check":
		row.Name, row.Tone, row.Note = "All checks passed", "ok", "Ready for you to review and send."
	case e.Kind == "run:draft":
		to, _, _ := strings.Cut(strings.TrimPrefix(note, "email.json to "), ",")
		row.Note = "Saved, not sent. To: " + to + "."
		if to == "no recipient yet" {
			row.Note = "Saved, not sent. Add who it goes to before you send."
		}
	case e.Kind == "email:draft":
		to, subject, _ := strings.Cut(strings.TrimPrefix(note, "to "), ": ")
		row.Note = "“" + subject + "”"
		if to = strings.TrimSpace(to); to != "" {
			row.Note += " to " + to
		} else {
			row.Note += ", no recipient yet"
		}
	default:
		if len(note) > 160 {
			row.Note, row.Detail = firstSentence(note), note
		}
	}
	if row.Detail == row.Note {
		row.Detail = ""
	}
	row.Long = len(row.Note) > 160
	return row
}

// gateItems splits "coverage: …; grounding: …" into one item per check; a detail may hold "; " itself.
func gateItems(s string) []evItem {
	var out []evItem
	for _, part := range strings.Split(s, "; ") {
		name, text, ok := strings.Cut(part, ": ")
		if _, known := gateLabels[name]; ok && known {
			out = append(out, evItem{gateLabel(name), text})
		} else if n := len(out); n > 0 {
			out[n-1].Text += "; " + part
		} else {
			out = append(out, evItem{Text: part})
		}
	}
	return out
}

// firstSentence cuts a long note to its first sentence, or 140 characters.
func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i > 0 && i < 140 {
		return s[:i+1]
	}
	if len(s) > 140 {
		return strings.TrimSpace(s[:140]) + "…"
	}
	return s
}

func stageName(s string) string {
	return agentTitle(strings.ReplaceAll(strings.TrimSpace(s), "_", " "))
}

// eventGroupsOf lists the groups the events fall in, in the Activity filter order.
func eventGroupsOf(events []store.Event) []string {
	seen := map[string]bool{}
	for _, e := range events {
		seen[eventGroup(e.Kind)] = true
	}
	var out []string
	for _, g := range activityGroups {
		if seen[g] {
			out = append(out, g)
		}
	}
	return out
}
