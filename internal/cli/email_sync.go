package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	netmail "net/mail"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/mail"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

func imapAccount(cfg Config) mail.IMAP {
	pass := os.Getenv("SCOUTJOBS_IMAP_PASSWORD")
	if pass == "" {
		pass = os.Getenv("SCOUTJOBS_SMTP_PASSWORD")
	}
	user := cfg.IMAPUser
	if user == "" {
		user = cfg.EmailFrom
	}
	return mail.IMAP{Host: cfg.IMAPHost, User: user, Pass: pass}
}

func smtpAccount(cfg Config) mail.SMTP {
	return mail.SMTP{Host: cfg.SMTPHost, User: cfg.SMTPUser, Pass: os.Getenv("SCOUTJOBS_SMTP_PASSWORD")}
}

// runEmailTest sends a one-line message to yourself and, when imap_host is set, logs into the inbox.
// Cheap proof that the account works before the first real application goes out.
func runEmailTest(cfg Config, rest []string, out io.Writer) error {
	fs := flag.NewFlagSet("email test", flag.ContinueOnError)
	to := fs.String("to", "", "recipient of the probe; default: email_from")
	asJSON := fs.Bool("json", false, "")
	if _, err := parse(fs, rest); err != nil {
		return err
	}
	if cfg.EmailFrom == "" {
		return errors.New("email_from missing in config.yaml")
	}
	if *to == "" {
		*to = cfg.EmailFrom
	}
	now := time.Now()
	m := mail.Message{From: cfg.EmailFrom, To: *to, Subject: "ScoutJobs SMTP test",
		Body: "ScoutJobs can send from this account. " + now.Format(time.RFC1123Z) + "\n", MessageID: mail.NewMessageID(cfg.EmailFrom, now)}
	raw, err := mail.Build(m, now)
	if err != nil {
		return err
	}
	res := map[string]any{"smtp_host": cfg.SMTPHost, "to": *to, "smtp": "ok"}
	if err := smtpAccount(cfg).Send(m, raw); err != nil {
		return fmt.Errorf("smtp %s as %s: %w (password: %s)", cfg.SMTPHost, orDash(cfg.SMTPUser), err, envState("SCOUTJOBS_SMTP_PASSWORD"))
	}
	if cfg.IMAPHost != "" {
		acct := imapAccount(cfg)
		if _, err := acct.Fetch(now.AddDate(0, 0, -1), 1); err != nil {
			return fmt.Errorf("smtp ok, imap %s as %s: %w (password: %s)", cfg.IMAPHost, acct.User, err, envState("SCOUTJOBS_IMAP_PASSWORD"))
		}
		res["imap_host"], res["imap"] = cfg.IMAPHost, "ok"
	}
	return emit(out, *asJSON, res, func() {
		fmt.Fprintf(out, "smtp ok: probe sent to %s through %s\n", *to, cfg.SMTPHost)
		if cfg.IMAPHost != "" {
			fmt.Fprintf(out, "imap ok: inbox readable on %s\n", cfg.IMAPHost)
		}
	})
}

func envState(name string) string {
	if os.Getenv(name) != "" {
		return "$" + name + " set"
	}
	return "$" + name + " not set"
}

// Reply is one inbox message matched to an application.
type Reply struct {
	UID     uint32 `json:"uid"`
	From    string `json:"from"`
	Subject string `json:"subject"`
	Job     int64  `json:"job"`
	Company string `json:"company"`
	Title   string `json:"title"`
	Status  string `json:"status,omitempty"` // new status applied, "" when the text gave no hint
	Via     string `json:"via"`              // "thread" (In-Reply-To matched our Message-ID), "contact" (sender is the job's contact) or "domain" (same company domain)
}

// runEmailSync reads the inbox and logs replies to applications: reply:received on the job,
// status interview or rejected when the wording is clear. Never deletes or flags anything.
func runEmailSync(s *store.Store, cfg Config, rest []string, out io.Writer) (err error) {
	fs := flag.NewFlagSet("email sync", flag.ContinueOnError)
	since := fs.String("since", "14d", "how far back to look: 14d, 3w, 2026-09-01; never before your first tracked job")
	limit := fs.Int("limit", 500, "newest messages to inspect at most")
	dry := fs.Bool("dry-run", false, "show matches, change nothing")
	via := fs.String("via", "auto", "agent (your AI agent's mail connector), gmail (API, after gmail connect), imap (imap_host), or auto")
	auto := fs.Bool("auto", false, "only when email_sync is auto, and never through the AI agent; otherwise do nothing")
	asJSON := fs.Bool("json", false, "")
	if _, err := parse(fs, rest); err != nil {
		return err
	}
	switch mode := emailMode(s, cfg); {
	case *auto && mode != "auto":
		return emit(out, *asJSON, []Reply{}, func() { fmt.Fprintf(out, "email_sync is %s: skipped\n", mode) })
	case mode == "off" && cfg.EmailSync == "off":
		return errEmailOff
	}
	from, err := parseSince(*since, time.Now())
	if err != nil {
		return err
	}
	source, err := mailSource(s, cfg, *via)
	if err != nil {
		return err
	}
	if source == "agent" {
		if *auto {
			return emit(out, *asJSON, []Reply{}, func() { fmt.Fprintln(out, "mail is read by your AI agent: skipped") })
		}
		return agentSync(s, cfg, from, out)
	}
	// a sync stopped by Gmail's rate limit still logs the replies among the messages it read
	mails, fetchErr := fetchMail(s, cfg, source, from, *limit)
	if fetchErr != nil && len(mails) == 0 {
		return fetchErr
	}
	replies, err := syncReplies(s, mails, source, *dry)
	if err != nil {
		return err
	}
	review := reviewCandidates(s, mails)
	defer func() {
		if fetchErr != nil {
			err = fetchErr
		}
	}()
	return emit(out, *asJSON, map[string]any{"source": source, "inspected": len(mails), "replies": replies, "review": review, "dry_run": *dry}, func() {
		if len(review) > 0 {
			defer fmt.Fprintf(out, "%d other messages may concern your search; --json lists them for inbox-triage\n", len(review))
		}
		if len(replies) == 0 {
			fmt.Fprintf(out, "%d messages inspected, no new replies to applications\n", len(mails))
			return
		}
		var rows [][]string
		for _, r := range replies {
			rows = append(rows, []string{strconv.FormatInt(r.Job, 10) + " " + trunc(r.Company, 18), trunc(r.From, 28), trunc(r.Subject, 36), orDash(r.Status), r.Via})
		}
		table(out, "JOB\tFROM\tSUBJECT\tSTATUS\tVIA", rows)
		if *dry {
			fmt.Fprintln(out, "dry run: nothing logged")
		}
	})
}

// SyncMail is `email sync` for the dashboard's Sync button: default window, auto source, no output.
func SyncMail(s *store.Store, cfg Config) (int, error) {
	source, err := mailSource(s, cfg, "auto")
	if err != nil {
		return 0, err
	}
	if source == "agent" {
		return 0, errors.New("mail is read by your AI agent: start it from the Inbox page")
	}
	mails, fetchErr := fetchMail(s, cfg, source, time.Now().AddDate(0, 0, -14), 500)
	replies, err := syncReplies(s, mails, source, false)
	if fetchErr != nil {
		return len(replies), fetchErr
	}
	return len(replies), err
}

// syncReplies matches inbox messages to jobs and records them. Split from the fetch so tests feed messages directly.
func syncReplies(s *store.Store, mails []mail.Mail, source string, dry bool) ([]Reply, error) {
	var replies []Reply
	for _, m := range mails {
		id := m.MessageID
		if id == "" {
			id = "uid:" + strconv.FormatUint(uint64(m.UID), 10)
		}
		if s.MailSeen(id) {
			continue
		}
		j, via, ok := matchReply(s, m)
		if !ok {
			continue
		}
		r := Reply{UID: m.UID, From: m.From, Subject: m.Subject, Job: j.ID, Company: j.Company, Title: j.Title, Via: via}
		if st := mail.Classify(m.Subject, m.Text); st != "" && st != j.Status && !store.FinalStatuses[j.Status] {
			r.Status = st
		}
		replies = append(replies, r)
		if dry {
			continue
		}
		if err := s.AddEvent(j.ID, "reply:received", "from "+m.From+": "+m.Subject); err != nil {
			return replies, err
		}
		if r.Status != "" {
			st := r.Status
			if err := s.UpdateJob(j.ID, store.JobPatch{Status: &st}); err != nil {
				return replies, err
			}
		}
		if _, err := s.LogMail(store.Mail{MessageID: id, JobID: j.ID, Dir: "in", From: m.From, Subject: m.Subject, Snippet: m.Text,
			Date: mailDate(m.Date), Thread: m.Thread, Source: source, Kind: mail.Classify(m.Subject, m.Text)}); err != nil {
			return replies, err
		}
	}
	return replies, nil
}

// Candidate is an unmatched message that looks like it concerns the search: an ATS sender, an open
// company's name, or application wording. inbox-triage judges these few instead of reading the inbox.
type Candidate struct {
	From      string `json:"from"`
	Subject   string `json:"subject"`
	Snippet   string `json:"snippet"`
	Date      string `json:"date"`
	Thread    string `json:"thread,omitempty"`
	MessageID string `json:"message_id"`
	Company   string `json:"company,omitempty"` // open job's company named in the message
	Hint      string `json:"hint,omitempty"`    // rejected | interview from the wording
}

var atsDomains = []string{"greenhouse.io", "lever.co", "ashbyhq.com", "workablemail.com", "workable.com", "smartrecruiters.com",
	"personio.de", "personio.com", "myworkday.com", "icims.com", "teamtailor.com", "recruitee.com", "join.com", "bamboohr.com", "jobvite.com", "successfactors.com"}

var applicationWords = []string{"your application", "application for", "applying", "bewerbung", "candidature", "recruit", "hiring"}

const maxCandidates = 30

func reviewCandidates(s *store.Store, mails []mail.Mail) []Candidate {
	jobs, _ := s.ListJobs(store.JobFilter{Open: true})
	var out []Candidate
	for _, m := range mails {
		id := m.MessageID
		if id == "" {
			id = "uid:" + strconv.FormatUint(uint64(m.UID), 10)
		}
		if s.MailSeen(id) {
			continue
		}
		if _, _, ok := matchReply(s, m); ok {
			continue
		}
		text := strings.ToLower(m.From + "\n" + m.Subject + "\n" + m.Text)
		c := Candidate{From: m.From, Subject: m.Subject, Snippet: trunc(m.Text, 200), Date: mailDate(m.Date), Thread: m.Thread,
			MessageID: id, Hint: mail.Classify(m.Subject, m.Text)}
		for _, j := range jobs {
			if len(j.Company) >= 3 && strings.Contains(text, strings.ToLower(j.Company)) {
				c.Company = j.Company
				break
			}
		}
		if c.Company == "" && !atsSender(m.From) && !containsAny(text, applicationWords) {
			continue
		}
		if out = append(out, c); len(out) == maxCandidates {
			break
		}
	}
	return out
}

func atsSender(from string) bool {
	d := mailDomain(from)
	for _, a := range atsDomains {
		if d == a || strings.HasSuffix(d, "."+a) {
			return true
		}
	}
	return false
}

func containsAny(s string, words []string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

// mailDate turns an RFC 5322 Date header into RFC3339 so messages sort; unparseable dates stay as they are.
func mailDate(v string) string {
	if t, err := netmail.ParseDate(v); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	return v
}

func matchReply(s *store.Store, m mail.Mail) (store.Job, string, bool) {
	for _, ref := range strings.Fields(m.InReplyTo) {
		if id, ok := s.MailJob(ref); ok {
			if j, err := s.JobByID(id); err == nil {
				return j, "thread", true
			}
		}
	}
	if id, ok := s.ThreadJob(m.Thread); ok { // a reply in the Gmail thread of a draft or an earlier reply
		if j, err := s.JobByID(id); err == nil {
			return j, "thread", true
		}
	}
	if m.From == "" {
		return store.Job{}, "", false
	}
	jobs, err := s.ListJobs(store.JobFilter{Open: true})
	if err != nil {
		return store.Job{}, "", false
	}
	for _, j := range jobs {
		if j.Contact != "" && strings.EqualFold(j.Contact, m.From) {
			return j, "contact", true
		}
	}
	// same company domain as the contact (hr@acme.com wrote, jobs@acme.com was the contact), only when one open job fits
	domain := mailDomain(m.From)
	if domain == "" || freeMail[domain] {
		return store.Job{}, "", false
	}
	var hit []store.Job
	for _, j := range jobs {
		if mailDomain(j.Contact) == domain {
			hit = append(hit, j)
		}
	}
	if len(hit) == 1 {
		return hit[0], "domain", true
	}
	if j, ok := companyNamed(jobs, m); ok {
		return j, "company", true
	}
	return store.Job{}, "", false
}

// companyNamed matches the mail an applicant tracking system sends ("Your application to Acme", from
// no-reply@greenhouse.io): the message must come from an ATS or speak of an application, and name the
// company of exactly one job applied to.
func companyNamed(jobs []store.Job, m mail.Mail) (store.Job, bool) {
	text := strings.ToLower(m.Subject + "\n" + m.Text)
	if !atsSender(m.From) && !containsAny(text, applicationWords) {
		return store.Job{}, false
	}
	var hit []store.Job
	for _, j := range jobs {
		name := strings.ToLower(strings.TrimSpace(j.Company))
		if len(name) >= 3 && appliedStatus[j.Status] && hasWord(text, name) {
			hit = append(hit, j)
		}
	}
	if len(hit) == 1 {
		return hit[0], true
	}
	return store.Job{}, false
}

// appliedStatus are the open statuses a company writes back to: before applying, mail naming it is not a reply.
var appliedStatus = map[string]bool{"applied": true, "interview": true, "offer": true}

// hasWord finds w in s with no letter or digit right around it, so "Meta" does not match "metadata".
func hasWord(s, w string) bool {
	for i := 0; ; {
		k := strings.Index(s[i:], w)
		if k < 0 {
			return false
		}
		k += i
		end := k + len(w)
		before, after := k == 0 || !wordByte(s[k-1]), end == len(s) || !wordByte(s[end])
		if before && after {
			return true
		}
		i = k + 1
	}
}

func wordByte(b byte) bool { return b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b >= 0x80 }

var freeMail = map[string]bool{"gmail.com": true, "googlemail.com": true, "outlook.com": true, "hotmail.com": true, "yahoo.com": true, "icloud.com": true, "gmx.de": true, "web.de": true, "proton.me": true, "protonmail.com": true}

func mailDomain(addr string) string {
	_, d, ok := strings.Cut(strings.ToLower(strings.TrimSpace(addr)), "@")
	if !ok {
		return ""
	}
	return d
}

// parseSince accepts 14d, 3w, 2h-style windows or a YYYY-MM-DD day.
func parseSince(v string, now time.Time) (time.Time, error) {
	if t, err := time.Parse("2006-01-02", v); err == nil {
		return t, nil
	}
	if len(v) >= 2 {
		n, err := strconv.Atoi(v[:len(v)-1])
		if err == nil && n > 0 {
			switch v[len(v)-1] {
			case 'd':
				return now.AddDate(0, 0, -n), nil
			case 'w':
				return now.AddDate(0, 0, -7*n), nil
			case 'h':
				return now.Add(-time.Duration(n) * time.Hour), nil
			}
		}
	}
	return now, fmt.Errorf("--since %q: use 14d, 3w, 48h or YYYY-MM-DD", v)
}
