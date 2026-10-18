package web

import (
	"fmt"
	"strings"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/apply"
	"github.com/zoolcoder/scoutjobs/internal/autopilot"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

// todo is one row of the overview's Today list: what to do, and buttons that do it.
type todo struct {
	Kind    string // outcome | interview | reply | followup | thanks | quiet | send | deadline | score | digest
	Title   string
	Detail  string
	Href    string // the row itself
	Links   []todoLink
	Status  string // a one-click status change, posted to /jobs/{JobID}/status
	JobID   int64
	StatusL string
	Post    string // a one-click action posted to this URL
	PostL   string
}

type todoLink struct {
	Label, Href string
	External    bool
}

// today collects what needs a hand now, most urgent first. Each source is capped so one kind cannot flood the list.
func (v *server) today(st store.Stats, upcoming, waiting []store.Interview) []todo {
	now := time.Now()
	soon := now.Add(72 * time.Hour).Format("2006-01-02T15:04")
	var out []todo
	for _, i := range waiting {
		out = append(out, todo{Kind: "outcome", Title: fmt.Sprintf("How did the %s %s interview go?", i.Company, strings.ReplaceAll(i.Stage, "_", " ")),
			Detail: friendlyWhen(i.At, now) + " · log the outcome so the pipeline moves on",
			Href:   "/interviews",
			Links:  []todoLink{{"Log outcome", "/interviews", false}}})
	}
	for _, i := range upcoming {
		if i.At > soon || i.Outcome != "" {
			continue
		}
		out = append(out, todo{Kind: "interview", Title: fmt.Sprintf("Prepare for %s · %s", i.Company, strings.ReplaceAll(i.Stage, "_", " ")),
			Detail: fmt.Sprintf("%s · prep %d/%d", friendlyWhen(i.At, time.Now()), i.PrepCount(), len(store.PrepItems)),
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
	drafted := map[int64]bool{}
	if applied, err := v.s.ListJobs(store.JobFilter{Status: "applied"}); err == nil {
		for _, j := range applied {
			if len(drafted) == 4 || !apply.PendingProposal(v.s, j, "followup") {
				continue
			}
			drafted[j.ID] = true
			href := fmt.Sprintf("/jobs/%d?tab=mail#emails", j.ID)
			out = append(out, todo{Kind: "followup", Title: "Review follow-up to " + j.Company, Detail: j.Title + " · drafted for you, not sent" + v.followUpCount(j),
				Href: href, Links: []todoLink{{"Review draft", href, false}}, JobID: j.ID, Status: "no_response", StatusL: "No response",
				Post: fmt.Sprintf("/jobs/%d/followed-up", j.ID), PostL: "Mark sent"})
		}
	}
	if met, err := v.s.ListJobs(store.JobFilter{Status: "interview"}); err == nil {
		n := 0
		for _, j := range met {
			if n == 3 || !apply.PendingProposal(v.s, j, "thanks") {
				continue
			}
			n++
			href := fmt.Sprintf("/jobs/%d?tab=mail#emails", j.ID)
			out = append(out, todo{Kind: "thanks", Title: "Review thank-you to " + j.Company, Detail: j.Title + " · drafted for you, not sent",
				Href: href, Links: []todoLink{{"Review draft", href, false}}})
		}
	}
	n = 0
	for _, j := range st.DueFollowUps {
		if n == 4 {
			break
		}
		if drafted[j.ID] {
			continue
		}
		drafted[j.ID] = true
		n++
		out = append(out, todo{Kind: "followup", Title: "Follow up with " + j.Company, Detail: j.Title + " · quiet since " + day(j.UpdatedAt) + v.followUpCount(j),
			Href:  fmt.Sprintf("/pipeline?job=%d", j.ID),
			Links: []todoLink{{"Write in Gmail", composeFollowUp(j), true}}, JobID: j.ID, Status: "no_response", StatusL: "No response",
			Post: fmt.Sprintf("/jobs/%d/followed-up", j.ID), PostL: "Mark sent"})
	}
	if len(st.Stale) > 0 {
		var names []string
		for _, j := range st.Stale {
			drafted[j.ID] = true
			if len(names) < 3 {
				names = append(names, j.Company)
			}
		}
		if len(st.Stale) > len(names) {
			names = append(names, fmt.Sprintf("%d more", len(st.Stale)-len(names)))
		}
		out = append(out, todo{Kind: "quiet", Title: fmt.Sprintf("Close %d %s quiet %d+ days", len(st.Stale), plural(len(st.Stale), "application", "applications"), v.cfg.StaleDays),
			Detail: strings.Join(names, ", ") + " · marks them no response; reopen any later", Href: "/pipeline?status=applied",
			Post: "/jobs/sweep", PostL: "Close all"})
	}
	out = append(out, v.quiet(now, drafted)...)
	if n := v.digestCount(now); n > 0 {
		out = append(out, todo{Kind: "digest", Title: fmt.Sprintf("%d new %s from your daily searches", n, plural(n, "job", "jobs")),
			Detail: "found in the last 24 hours · best fit first", Href: "/pipeline?sort=fit",
			Links: []todoLink{{"Look through", "/pipeline?sort=fit", false}}})
	}
	if drafts, err := v.s.ListJobs(store.JobFilter{Status: "drafted", Limit: 4}); err == nil {
		for _, j := range drafts {
			out = append(out, todo{Kind: "send", Title: "Send your application to " + j.Company, Detail: j.Title,
				Href: fmt.Sprintf("/jobs/%d", j.ID), Links: []todoLink{{"Review", fmt.Sprintf("/jobs/%d", j.ID), false}},
				JobID: j.ID, Status: "applied", StatusL: "Mark applied"})
		}
	}
	horizon := now.AddDate(0, 0, 7).Format("2006-01-02")
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
			out = append(out, deadlineTodo(j, now))
		}
	}
	if unscored > 0 {
		out = append(out, todo{Kind: "score", Title: fmt.Sprintf("Score %d new %s", unscored, plural(unscored, "job", "jobs")),
			Detail: "free local match against your profile; an agent review is only worth it for the top ones", Href: "/pipeline?sort=fit",
			Post: "/jobs/rank", PostL: "Score now"})
	}
	return out
}

// quietFactor is how many times a source's usual wait an application may stay quiet before it is likely lost.
// quietFloor keeps fast-answering sources from flagging a job after a few days.
const quietFactor, quietFloor = 2, 14

// quiet lists applications silent for well past their source's usual wait for an answer, so the user can
// close them; jobs already on the list for a follow-up are left out.
func (v *server) quiet(now time.Time, listed map[int64]bool) []todo {
	f, err := v.s.Funnel()
	if err != nil {
		return nil
	}
	applied, _ := v.s.ListJobs(store.JobFilter{Status: "applied"})
	var out []todo
	for _, j := range applied {
		usual, ok := f.AnswerDays(j.Source)
		since, _, known := parseAt(j.UpdatedAt)
		if listed[j.ID] || !ok || !known || len(out) == 3 {
			continue
		}
		if days := int(now.Sub(since).Hours() / 24); days > max(quietFactor*usual, quietFloor) {
			src := strings.TrimSpace(j.Source)
			if src == "" {
				src = "manual"
			}
			out = append(out, todo{Kind: "quiet", Title: j.Company + " has likely moved on", JobID: j.ID, Href: fmt.Sprintf("/jobs/%d", j.ID),
				Detail: fmt.Sprintf("quiet %d days; %s jobs usually answer in %d %s", days, src, usual, plural(usual, "day", "days")), Status: "no_response", StatusL: "No response"})
		}
	}
	return out
}

// digestCount is how many jobs the daily searches added in the last 24 hours.
func (v *server) digestCount(now time.Time) int {
	events, err := v.s.Events(0, 300)
	if err != nil {
		return 0
	}
	since := now.Add(-24 * time.Hour).UTC().Format(time.RFC3339)
	n := 0
	for _, e := range events {
		if e.At < since {
			break
		}
		if e.Kind == autopilot.DigestEvent {
			n++
		}
	}
	return n
}

// deadlineTodo puts urgency in the title: closing today, in a few days, or already closed.
func deadlineTodo(j store.Job, now time.Time) todo {
	t := todo{Kind: "deadline", Detail: j.Title + " · closes " + j.Deadline, Href: fmt.Sprintf("/pipeline?job=%d", j.ID)}
	due, err := time.ParseInLocation("2006-01-02", j.Deadline, time.Local)
	days := int(due.Sub(midnight(now)).Hours() / 24)
	switch {
	case err != nil:
		t.Title = "Deadline " + j.Deadline + ": " + j.Company
	case days < 0:
		t.Title = "Deadline passed: " + j.Company
		t.JobID, t.Status, t.StatusL = j.ID, "skipped", "Skip"
	case days == 0:
		t.Title = "Closes today: " + j.Company
	default:
		t.Title = fmt.Sprintf("Closes in %d %s: %s", days, plural(days, "day", "days"), j.Company)
	}
	return t
}

// followUpCount says how many follow-ups went out, once there was one.
func (v *server) followUpCount(j store.Job) string {
	if n := v.s.FollowUps(j.ID); n > 0 {
		return fmt.Sprintf(" · %d of %d follow-ups sent", n, store.MaxFollowUps)
	}
	return ""
}

func composeFollowUp(j store.Job) string {
	to := ""
	if strings.Contains(j.Contact, "@") {
		to = j.Contact
	}
	return gmailCompose(to, "Following up: "+j.Title+" application", "")
}
