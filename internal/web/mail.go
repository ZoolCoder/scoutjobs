package web

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/mail"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

// mailRow is one logged message with the Gmail links the templates need.
type mailRow struct {
	store.Mail
	When  time.Time
	Open  string // the thread (or message) in Gmail
	Reply string // Gmail compose to the sender, Re: subject
}

func (m mailRow) Day() string {
	if m.When.IsZero() {
		return ""
	}
	return m.When.Local().Format("2006-01-02")
}

func (v *server) mailSource() string {
	if v.cfg.Mail.Status == nil {
		return ""
	}
	src, _ := v.cfg.Mail.Status()
	return src
}

func (v *server) mailRows(jobID int64, limit int) []mailRow {
	ms, _ := v.s.Mails(jobID, limit)
	out := make([]mailRow, 0, len(ms))
	for _, m := range ms {
		out = append(out, toRow(m))
	}
	return out
}

func toRow(m store.Mail) mailRow {
	r := mailRow{Mail: m}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, firstNonEmpty(m.Date, m.At)); err == nil {
			r.When = t
			break
		}
	}
	switch {
	case m.Thread != "":
		r.Open = "https://mail.google.com/mail/u/0/#all/" + url.PathEscape(m.Thread)
	case strings.HasPrefix(m.MessageID, "<"):
		r.Open = mail.GmailSearch("rfc822msgid:" + strings.Trim(m.MessageID, "<>"))
	}
	if m.Dir == "in" && m.From != "" {
		subj := m.Subject
		if !strings.HasPrefix(strings.ToLower(subj), "re:") {
			subj = "Re: " + subj
		}
		r.Reply = mail.GmailCompose(m.From, subj, "")
	}
	return r
}

var gmailCompose = mail.GmailCompose

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// composeTo opens Gmail on a new message to the job's contact: a follow-up, a thank-you.
func composeTo(j store.Job) string {
	to := ""
	if strings.Contains(j.Contact, "@") {
		to = j.Contact
	}
	return gmailCompose(to, j.Title+" application", "")
}

// mailSearch finds everything Gmail has from the company: the contact's domain, else the name.
func mailSearch(j store.Job) string {
	if _, d, ok := strings.Cut(j.Contact, "@"); ok && d != "" {
		return mail.GmailSearch("from:" + d)
	}
	return mail.GmailSearch(`"` + j.Company + `"`)
}

type mailDay struct {
	Day  string
	Rows []mailRow
}

func (v *server) inbox(w http.ResponseWriter, r *http.Request) {
	ms, err := v.s.Mails(0, 300)
	if err != nil {
		fail(w, err)
		return
	}
	kind := r.URL.Query().Get("kind")
	var days []mailDay
	counts := map[string]int{}
	for _, m := range ms {
		counts[m.Kind]++
		if kind != "" && m.Kind != kind {
			continue
		}
		row := toRow(m)
		d := row.Day()
		if len(days) == 0 || days[len(days)-1].Day != d {
			days = append(days, mailDay{Day: d})
		}
		days[len(days)-1].Rows = append(days[len(days)-1].Rows, row)
	}
	src, account := "", ""
	if v.cfg.Mail.Status != nil {
		src, account = v.cfg.Mail.Status()
	}
	gm := v.gmailClient()
	v.render(w, r, "inbox.html", "inbox", map[string]any{"Gmail": gm != nil, "GmailClient": gm != nil && gm.ID != "", "Home": v.s.Home, "Days": days, "Total": len(ms), "Kind": kind, "Counts": counts,
		"Kinds":  []string{"interview", "offer", "request", "rejected", "ack"},
		"Source": src, "Account": account, "CanSync": src != "" && v.cfg.Mail.Sync != nil,
		"Msg": r.URL.Query().Get("msg"), "Err": r.URL.Query().Get("err")})
}

func (v *server) syncInbox(w http.ResponseWriter, r *http.Request) {
	dest := back(r, "/inbox")
	if v.cfg.Mail.Sync == nil {
		http.Redirect(w, r, withParam(dest, "err", "mail sync is not available"), http.StatusSeeOther)
		return
	}
	n, err := v.cfg.Mail.Sync()
	if err != nil {
		http.Redirect(w, r, withParam(dest, "err", err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, withParam(dest, "msg", fmt.Sprintf("%d new %s matched to jobs", n, plural(n, "reply", "replies"))), http.StatusSeeOther)
}

// gmailDraft puts the job's application e-mail into Gmail's drafts and returns to the page with a link.
func (v *server) gmailDraft(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	dest := back(r, fmt.Sprintf("/jobs/%d", id))
	if v.cfg.Mail.Draft == nil {
		http.Redirect(w, r, withParam(dest, "err", "gmail is not connected"), http.StatusSeeOther)
		return
	}
	link, err := v.cfg.Mail.Draft(id, r.FormValue("kind"))
	if err != nil {
		http.Redirect(w, r, withParam(dest, "err", err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, withParam(dest, "drafted", link), http.StatusSeeOther)
}

func withParam(u, k, val string) string {
	p, err := url.Parse(u)
	if err != nil {
		return u
	}
	q := p.Query()
	q.Set(k, val)
	p.RawQuery = q.Encode()
	return p.String()
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// apiLogMail is the connector path: an agent that read Gmail records the threads it matched.
func (v *server) apiLogMail(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		apiErr(w, 400, err)
		return
	}
	j, err := v.s.JobByID(id)
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	var in struct {
		From, Subject, Snippet, Date, Thread, Kind, Dir string
		MessageID                                       string `json:"message_id"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		apiErr(w, 400, err)
		return
	}
	if strings.TrimSpace(in.Subject) == "" {
		apiErr(w, 400, fmt.Errorf("subject is required"))
		return
	}
	if in.Dir == "" {
		in.Dir = "in"
	}
	if in.Dir != "in" && in.Dir != "out" {
		apiErr(w, 400, fmt.Errorf("dir must be in or out"))
		return
	}
	if in.MessageID == "" {
		in.MessageID = "connector:" + in.Thread + ":" + store.Slug(in.From+" "+in.Subject+" "+in.Date)
	}
	created, err := v.s.LogMail(store.Mail{MessageID: in.MessageID, JobID: j.ID, Dir: in.Dir, From: mail.Address(in.From), Subject: in.Subject,
		Snippet: in.Snippet, Date: in.Date, Thread: in.Thread, Source: "connector", Kind: in.Kind})
	if err != nil {
		apiErr(w, 400, err)
		return
	}
	if created && in.Dir == "in" {
		_ = v.s.AddEvent(j.ID, "reply:received", "from "+mail.Address(in.From)+": "+in.Subject)
	}
	code := 200
	if created {
		code = 201
	}
	writeJSON(w, code, map[string]any{"logged": created, "message_id": in.MessageID})
}

func (v *server) apiMail(w http.ResponseWriter, r *http.Request) {
	job, _ := strconv.ParseInt(r.URL.Query().Get("job"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	ms, err := v.s.Mails(job, limit)
	if err != nil {
		apiErr(w, 400, err)
		return
	}
	writeJSON(w, 200, ms)
}
