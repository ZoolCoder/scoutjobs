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

// activityRow is one event as the Activity page shows it.
type activityRow struct {
	store.Event
	Group, Name, Tone string
	Long              bool
}

func activityRowOf(e store.Event) activityRow {
	row := activityRow{Event: e, Group: eventGroup(e.Kind), Name: eventTitles[e.Kind]}
	if row.Name == "" {
		row.Name = strings.ToUpper(row.Group[:1]) + row.Group[1:]
	}
	switch e.Kind {
	case "status":
		row.Note = strings.ReplaceAll(e.Note, "->", "→")
	case "run:failed":
		row.Tone = "failed"
	}
	row.Long = len(row.Note) > 160
	return row
}
