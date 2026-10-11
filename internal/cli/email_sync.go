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
func runEmailSync(s *store.Store, cfg Config, rest []string, out io.Writer) error {
	fs := flag.NewFlagSet("email sync", flag.ContinueOnError)
	since := fs.String("since", "14d", "how far back to look: 14d, 3w, 2026-09-01")
	limit := fs.Int("limit", 500, "newest messages to inspect at most")
	dry := fs.Bool("dry-run", false, "show matches, change nothing")
	via := fs.String("via", "auto", "gmail (API, after gmail connect), imap (imap_host), or auto")
	asJSON := fs.Bool("json", false, "")
	if _, err := parse(fs, rest); err != nil {
		return err
	}
	from, err := parseSince(*since, time.Now())
	if err != nil {
		return err
	}
	source, err := mailSource(s, cfg, *via)
	if err != nil {
		return err
	}
	mails, err := fetchMail(s, cfg, source, from, *limit)
	if err != nil {
		return err
	}
	replies, err := syncReplies(s, mails, source, *dry)
	if err != nil {
		return err
	}
	return emit(out, *asJSON, map[string]any{"source": source, "inspected": len(mails), "replies": replies, "dry_run": *dry}, func() {
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
	mails, err := fetchMail(s, cfg, source, time.Now().AddDate(0, 0, -14), 500)
	if err != nil {
		return 0, err
	}
	replies, err := syncReplies(s, mails, source, false)
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
	return store.Job{}, "", false
}

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
