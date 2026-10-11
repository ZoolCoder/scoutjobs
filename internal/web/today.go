package web

import (
	"fmt"
	"strings"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/store"
)

// todo is one row of the overview's Today list: what to do, and buttons that do it.
type todo struct {
	Kind    string // interview | reply | followup | send | deadline | score
	Title   string
	Detail  string
	Href    string // the row itself
	Links   []todoLink
	Status  string // a one-click status change, posted to /jobs/{JobID}/status
	JobID   int64
	StatusL string
}

type todoLink struct {
	Label, Href string
	External    bool
}

// today collects what needs a hand now, most urgent first. Each source is capped so one kind cannot flood the list.
func (v *server) today(st store.Stats, upcoming []store.Interview) []todo {
	now := time.Now()
	soon := now.Add(72 * time.Hour).Format("2006-01-02T15:04")
	var out []todo
	for _, i := range upcoming {
		if i.At > soon || i.Outcome != "" {
			continue
		}
		out = append(out, todo{Kind: "interview", Title: fmt.Sprintf("Prepare for %s · %s", i.Company, strings.ReplaceAll(i.Stage, "_", " ")),
			Detail: fmt.Sprintf("%s · prep %d/%d", strings.Replace(i.At, "T", " ", 1), i.PrepCount(), len(store.PrepItems)),
			Href:   fmt.Sprintf("/calendar?view=week&d=%s&ev=i%d", day(i.At), i.ID),
			Links:  []todoLink{{"Open prep checklist", fmt.Sprintf("/calendar?view=week&d=%s&ev=i%d", day(i.At), i.ID), false}}})
	}
	cutoff := now.AddDate(0, 0, -3)
	n := 0
	if ms, err := v.s.Mails(0, 30); err == nil {
		for _, m := range ms {
			row := toRow(m, v.mailAccount())
			if m.Dir != "in" || n >= 4 || row.When.Before(cutoff) || m.Kind == "ack" || m.Kind == "rejected" {
				continue
			}
			n++
			t := todo{Kind: "reply", Title: fmt.Sprintf("Answer %s: %s", m.Company, m.Subject), Detail: m.From, Href: fmt.Sprintf("/pipeline?job=%d", m.JobID)}
			if row.Open != "" {
				t.Links = append(t.Links, todoLink{"Open in Gmail", row.Open, true})
			}
			if row.Reply != "" {
				t.Links = append(t.Links, todoLink{"Reply", row.Reply, true})
			}
			out = append(out, t)
		}
	}
	for i, j := range st.DueFollowUps {
		if i == 4 {
			break
		}
		out = append(out, todo{Kind: "followup", Title: "Follow up with " + j.Company, Detail: j.Title + " · quiet since " + day(j.UpdatedAt),
			Href:  fmt.Sprintf("/pipeline?job=%d", j.ID),
			Links: []todoLink{{"Write in Gmail", composeFollowUp(j), true}}, JobID: j.ID, Status: "no_response", StatusL: "No response"})
	}
	if drafts, err := v.s.ListJobs(store.JobFilter{Status: "drafted", Limit: 4}); err == nil {
		for _, j := range drafts {
			out = append(out, todo{Kind: "send", Title: "Send your application to " + j.Company, Detail: j.Title,
				Href: fmt.Sprintf("/jobs/%d", j.ID), Links: []todoLink{{"Review", fmt.Sprintf("/jobs/%d", j.ID), false}},
				JobID: j.ID, Status: "applied", StatusL: "Mark applied"})
		}
	}
	horizon := now.AddDate(0, 0, 3).Format("2006-01-02")
	open, _ := v.s.ListJobs(store.JobFilter{})
	unscored := 0
	for _, j := range open {
		if store.FinalStatuses[j.Status] {
			continue
		}
		if j.Status == "new" && j.FitScore < 0 {
			unscored++
		}
		if j.Deadline != "" && j.Deadline <= horizon && (j.Status == "new" || j.Status == "ranked" || j.Status == "drafted") {
			out = append(out, todo{Kind: "deadline", Title: "Deadline " + j.Deadline + ": " + j.Company, Detail: j.Title, Href: fmt.Sprintf("/pipeline?job=%d", j.ID)})
		}
	}
	if unscored > 0 {
		out = append(out, todo{Kind: "score", Title: fmt.Sprintf("Score %d new %s", unscored, plural(unscored, "job", "jobs")),
			Detail: "run /scoutjobs:fit or the fit-scorer agent", Href: "/pipeline?sort=updated"})
	}
	return out
}

func composeFollowUp(j store.Job) string {
	to := ""
	if strings.Contains(j.Contact, "@") {
		to = j.Contact
	}
	return gmailCompose(to, "Following up: "+j.Title+" application", "")
}
