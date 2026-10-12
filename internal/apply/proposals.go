package apply

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/mail"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

// Proposal is an e-mail ScoutJobs suggests for where a job stands. It is written from a template, so
// it costs no tokens, and it is only ever saved or turned into a draft: never sent.
type Proposal struct {
	Kind  string       `json:"kind"`
	Label string       `json:"label"`
	Why   string       `json:"why"`
	Saved bool         `json:"saved"` // the user saved an edited version
	Msg   mail.Message `json:"message"`
}

// ProposalKinds in the order the job page lists them.
var ProposalKinds = []string{"followup", "thanks", "offer-accept", "offer-negotiate", "offer-decline"}

func ValidKind(kind string) bool {
	for _, k := range ProposalKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// DraftFile is the file holding a kind's message; "" and "application" are email.json.
func DraftFile(kind string) string {
	if kind == "" || kind == "application" {
		return DraftName
	}
	return "email-" + kind + ".json"
}

// Proposals returns the e-mails due for the job now: a follow-up when an application has gone quiet
// for followUpDays, a thank-you in the days after an interview, and the three replies to an offer.
func Proposals(s *store.Store, j store.Job, followUpDays int, now time.Time) []Proposal {
	events, _ := s.Events(j.ID, 200)
	mails, _ := s.Mails(j.ID, 50)
	ivs, _ := s.Interviews(j.ID, false)
	t := template{job: j, name: SenderName(s.Home), to: j.Contact, subject: fmt.Sprintf("Application: %s at %s", j.Title, j.Company)}
	if m, err := LoadDraft(Dir(s, j)); err == nil && m.Subject != "" {
		t.subject = m.Subject
	}
	var lastIn time.Time
	for _, m := range mails {
		if m.Dir != "in" {
			continue
		}
		if at := mailTime(m); at.After(lastIn) {
			lastIn, t.reply = at, m
			if addr := address(m.From); addr != "" {
				t.to = addr
			}
		}
	}
	var out []Proposal
	switch j.Status {
	case "applied":
		applied := statusSince(events, "applied", j.UpdatedAt)
		if !applied.IsZero() && now.Sub(applied) >= time.Duration(followUpDays)*24*time.Hour && lastIn.Before(applied) {
			out = append(out, t.followup(int(now.Sub(applied).Hours()/24)))
		}
	case "interview":
		for _, iv := range ivs {
			at, err := time.ParseInLocation("2006-01-02T15:04", iv.At, time.Local)
			if err == nil && at.Before(now) && now.Sub(at) < 4*24*time.Hour {
				out = append(out, t.thanks(iv))
				break
			}
		}
	case "offer":
		out = append(out, t.offer("offer-accept"), t.offer("offer-negotiate"), t.offer("offer-decline"))
	}
	dir := Dir(s, j)
	for i, p := range out {
		if m, err := LoadProposal(dir, p.Kind); err == nil {
			out[i].Msg, out[i].Saved = m, true
		}
	}
	return out
}

// LoadProposal reads a saved proposal back.
func LoadProposal(dir, kind string) (mail.Message, error) {
	var m mail.Message
	raw, err := os.ReadFile(filepath.Join(dir, DraftFile(kind)))
	if err == nil {
		err = json.Unmarshal(raw, &m)
	}
	return m, err
}

// SaveProposal keeps the user's edited proposal next to the application drafts.
func SaveProposal(s *store.Store, j store.Job, kind string, m mail.Message) error {
	if !ValidKind(kind) {
		return fmt.Errorf("unknown e-mail kind %q", kind)
	}
	if strings.TrimSpace(m.Body) == "" {
		return errors.New("the e-mail is empty")
	}
	dir := Dir(s, j)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, DraftFile(kind)), raw, 0o600); err != nil {
		return err
	}
	_ = s.AddEvent(j.ID, "email:proposal", kind+" to "+m.To+": "+m.Subject)
	return nil
}

// SenderName is the "Name:" line of profile.md, for signing proposals.
func SenderName(home string) string {
	f, err := os.Open(filepath.Join(home, "profile.md"))
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimLeft(strings.TrimSpace(sc.Text()), "-* ")
		if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.Trim(k, "* "), "name") {
			return strings.TrimSpace(strings.Trim(v, "* "))
		}
	}
	return ""
}

type template struct {
	job     store.Job
	name    string
	to      string
	subject string
	reply   store.Mail
}

func (t template) msg(subject, body string) mail.Message {
	sign := "Best regards"
	if t.name != "" {
		sign += ",\n" + t.name
	}
	return mail.Message{To: t.to, Subject: subject, Body: body + "\n\n" + sign + "\n"}
}

// re answers the last mail from the company when there is one, else the application itself.
func (t template) re() string {
	s := t.subject
	if t.reply.Subject != "" {
		s = t.reply.Subject
	}
	if !strings.HasPrefix(strings.ToLower(s), "re:") {
		s = "Re: " + s
	}
	return s
}

func (t template) followup(days int) Proposal {
	body := fmt.Sprintf("Hello,\n\nI applied for the %s position at %s %d days ago and wanted to follow up. I am still very interested in the role and would be glad to share anything else that helps your decision.\n\nIs there an update on the next steps?", t.job.Title, t.job.Company, days)
	return Proposal{Kind: "followup", Label: "Follow-up", Why: fmt.Sprintf("no reply %d days after applying", days), Msg: t.msg(t.re(), body)}
}

func (t template) thanks(iv store.Interview) Proposal {
	who := "Hello"
	if iv.Interviewer != "" {
		who = "Dear " + iv.Interviewer
	}
	stage := strings.ReplaceAll(iv.Stage, "_", " ")
	body := fmt.Sprintf("%s,\n\nThank you for the %s interview for the %s role. I enjoyed the conversation and learning more about the team and its work, and it confirmed my interest in joining %s.\n\nPlease let me know if I can share anything else.", who, stage, t.job.Title, t.job.Company)
	return Proposal{Kind: "thanks", Label: "Interview thank-you", Why: "interview on " + strings.Replace(iv.At, "T", " ", 1), Msg: t.msg(fmt.Sprintf("Thank you: %s interview, %s", t.job.Title, t.job.Company), body)}
}

func (t template) offer(kind string) Proposal {
	role := fmt.Sprintf("the %s offer at %s", t.job.Title, t.job.Company)
	switch kind {
	case "offer-accept":
		return Proposal{Kind: kind, Label: "Accept the offer", Why: "offer received", Msg: t.msg(t.re(), fmt.Sprintf("Hello,\n\nThank you for %s. I am happy to accept it.\n\nPlease send the contract and let me know the next steps and the start date you have in mind.", role))}
	case "offer-negotiate":
		return Proposal{Kind: kind, Label: "Negotiate", Why: "offer received", Msg: t.msg(t.re(), fmt.Sprintf("Hello,\n\nThank you for %s. I am excited about the role and the team.\n\nBefore I accept, I would like to discuss the compensation. Based on the scope of the role and my experience, I was hoping for [your number]. Is there flexibility there?\n\nI am confident we can find terms that work for both of us.", role))}
	default:
		return Proposal{Kind: kind, Label: "Decline the offer", Why: "offer received", Msg: t.msg(t.re(), fmt.Sprintf("Hello,\n\nThank you for %s and for the time the team spent with me. After careful thought I have decided to decline.\n\nI hope our paths cross again.", role))}
	}
}

// statusSince is when the job last moved to status, from its status events; fallback when none.
func statusSince(events []store.Event, status, fallback string) time.Time {
	for _, e := range events { // newest first
		if e.Kind == "status" && strings.HasSuffix(e.Note, "-> "+status) {
			if at, err := time.Parse(time.RFC3339, e.At); err == nil {
				return at
			}
		}
	}
	at, _ := time.Parse(time.RFC3339, fallback)
	return at
}

// PendingProposal reports whether a saved proposal of the kind waits for the user: no mail went
// either way since it was written.
func PendingProposal(s *store.Store, j store.Job, kind string) bool {
	st, err := os.Stat(filepath.Join(Dir(s, j), DraftFile(kind)))
	if err != nil {
		return false
	}
	mails, _ := s.Mails(j.ID, 50)
	for _, m := range mails {
		if mailTime(m).After(st.ModTime()) {
			return false
		}
	}
	return true
}

func mailTime(m store.Mail) time.Time {
	for _, v := range []string{m.Date, m.At} {
		if at, err := time.Parse(time.RFC3339, v); err == nil {
			return at
		}
	}
	return time.Time{}
}

func address(from string) string {
	if i := strings.LastIndex(from, "<"); i >= 0 {
		from = strings.TrimSuffix(from[i+1:], ">")
	}
	if strings.Contains(from, "@") {
		return strings.TrimSpace(from)
	}
	return ""
}
