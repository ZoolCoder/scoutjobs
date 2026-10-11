package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/apply"
	"github.com/zoolcoder/scoutjobs/internal/mail"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

func runEmail(s *store.Store, cfg Config, rest []string, out io.Writer) error {
	cmd, rest := sub(rest)
	switch cmd {
	case "draft":
		fs := flag.NewFlagSet("email draft", flag.ContinueOnError)
		to := fs.String("to", "", "recipient; default: the job's contact")
		subject := fs.String("subject", "", "default: Application: <title> at <company>")
		bodyFile := fs.String("body", "cover-letter.md", "file in the application folder used as body")
		var attach stringList
		fs.Var(&attach, "attach", "extra file to attach (repeatable); default: resume.pdf or resume.md in the application folder")
		asJSON := fs.Bool("json", false, "")
		pos, err := parse(fs, rest)
		if err != nil {
			return err
		}
		if len(pos) == 0 {
			return errors.New("usage: email draft <id> [--to hr@company.com] [--subject ...] [--attach file]")
		}
		j, err := s.ResolveJob(pos[0])
		if err != nil {
			return err
		}
		m, dir, err := apply.Draft(s, j, cfg.EmailFrom, apply.DraftOptions{To: *to, Subject: *subject, Body: *bodyFile, Attach: attach})
		if err != nil {
			return err
		}
		return emit(out, *asJSON, map[string]any{"job": j.ID, "dir": dir, "email": m, "mailto": mail.Mailto(m)}, func() { preview(out, m, dir) })
	case "show":
		fs := flag.NewFlagSet("email show", flag.ContinueOnError)
		asJSON := fs.Bool("json", false, "")
		pos, err := parse(fs, rest)
		if err != nil {
			return err
		}
		if len(pos) == 0 {
			return errors.New("usage: email show <id>")
		}
		j, err := s.ResolveJob(pos[0])
		if err != nil {
			return err
		}
		m, dir, err := loadDraft(s, j)
		if err != nil {
			return err
		}
		return emit(out, *asJSON, map[string]any{"job": j.ID, "dir": dir, "email": m, "mailto": mail.Mailto(m)}, func() { preview(out, m, dir) })
	case "send":
		fs := flag.NewFlagSet("email send", flag.ContinueOnError)
		yes := fs.Bool("yes", false, "actually send; without it the draft is shown and nothing leaves the machine")
		skip := fs.Bool("skip-checks", false, "send although a quality gate failed; the skip is logged on the job")
		asJSON := fs.Bool("json", false, "")
		pos, err := parse(fs, rest)
		if err != nil {
			return err
		}
		if len(pos) == 0 {
			return errors.New("usage: email send <id> --yes")
		}
		j, err := s.ResolveJob(pos[0])
		if err != nil {
			return err
		}
		m, dir, err := loadDraft(s, j)
		if err != nil {
			return err
		}
		if m.From == "" {
			m.From = cfg.EmailFrom
		}
		now := time.Now()
		m.MessageID = mail.NewMessageID(m.From, now)
		raw, err := mail.Build(m, now)
		if err != nil {
			return err
		}
		if !*yes {
			preview(out, m, dir)
			return errors.New("not sent: review the draft above, then run again with --yes")
		}
		events, _ := s.Events(j.ID, 200)
		if rep := apply.Check(s, j, events, apply.CheckOptions{}); !rep.OK() {
			if !*skip {
				printReport(out, j, rep)
				return fmt.Errorf("not sent: %w\nfix them, or `--skip-checks` to send anyway", unresolved(rep))
			}
			_ = s.AddEvent(j.ID, "check:skipped", strings.Join(rep.Unresolved(), "; "))
		}
		if err := smtpAccount(cfg).Send(m, raw); err != nil {
			return err
		}
		_ = os.WriteFile(filepath.Join(dir, "email-sent.eml"), raw, 0o600)
		_, _ = s.LogMail(store.Mail{MessageID: m.MessageID, JobID: j.ID, Dir: "out", From: m.From, Subject: m.Subject, Snippet: m.Body, Source: "smtp"})
		_ = s.AddEvent(j.ID, "email:sent", "to "+m.To+": "+m.Subject)
		if j.Status == "new" || j.Status == "ranked" || j.Status == "drafted" {
			st := "applied"
			_ = s.UpdateJob(j.ID, store.JobPatch{Status: &st})
		}
		return emit(out, *asJSON, map[string]any{"job": j.ID, "sent": true, "to": m.To}, func() { fmt.Fprintf(out, "sent to %s: %s\n", m.To, m.Subject) })
	case "test":
		return runEmailTest(cfg, rest, out)
	case "sync":
		return runEmailSync(s, cfg, rest, out)
	case "log":
		return runEmailLog(s, rest, out)
	}
	return errors.New("usage: email draft|show|send <id> | email test | email sync | email log <id>")
}

// runEmailLog records one message an agent matched through its Gmail connector, so the dashboard
// shows it on the job and in the inbox. --status moves the job, like a reply found by email sync.
func runEmailLog(s *store.Store, rest []string, out io.Writer) error {
	fs := flag.NewFlagSet("email log", flag.ContinueOnError)
	from := fs.String("from", "", "sender address")
	subject := fs.String("subject", "", "subject line")
	snippet := fs.String("snippet", "", "one or two lines of the message, no more")
	date := fs.String("date", "", "when it was sent: RFC3339 or YYYY-MM-DD; default now")
	thread := fs.String("thread", "", "Gmail thread id, for the Open in Gmail link")
	msgID := fs.String("message-id", "", "Message-ID header or the connector's message id; dedupes repeat runs")
	kind := fs.String("kind", "", "rejected | interview | offer | request | ack")
	dir := fs.String("dir", "in", "in (received) or out (you sent it)")
	status := fs.String("status", "", "move the job too, e.g. rejected or interview")
	asJSON := fs.Bool("json", false, "")
	pos, err := parse(fs, rest)
	if err != nil {
		return err
	}
	if len(pos) == 0 || *subject == "" {
		return errors.New("usage: email log <job> --from hr@acme.com --subject \"...\" [--thread id] [--snippet ...] [--status rejected]")
	}
	j, err := s.ResolveJob(pos[0])
	if err != nil {
		return err
	}
	if *dir != "in" && *dir != "out" {
		return fmt.Errorf("--dir %q: use in or out", *dir)
	}
	id := *msgID
	if id == "" {
		id = "connector:" + *thread + ":" + store.Slug(*from+" "+*subject+" "+*date)
	}
	when := *date
	if when != "" {
		when = mailDate(when)
		if t, err := time.Parse("2006-01-02", when); err == nil {
			when = t.UTC().Format(time.RFC3339)
		}
	}
	created, err := s.LogMail(store.Mail{MessageID: id, JobID: j.ID, Dir: *dir, From: mail.Address(*from), Subject: *subject, Snippet: *snippet,
		Date: when, Thread: *thread, Source: "connector", Kind: *kind})
	if err != nil {
		return err
	}
	if created && *dir == "in" {
		_ = s.AddEvent(j.ID, "reply:received", "from "+mail.Address(*from)+": "+*subject)
	}
	if *status != "" && *status != j.Status {
		if err := s.UpdateJob(j.ID, store.JobPatch{Status: status}); err != nil {
			return err
		}
	}
	return emit(out, *asJSON, map[string]any{"job": j.ID, "logged": created, "message_id": id}, func() {
		if created {
			fmt.Fprintf(out, "logged on %d %s: %s\n", j.ID, j.Company, *subject)
		} else {
			fmt.Fprintln(out, "already logged")
		}
	})
}

func loadDraft(s *store.Store, j store.Job) (mail.Message, string, error) {
	dir := apply.Dir(s, j)
	m, err := apply.LoadDraft(dir)
	if err != nil {
		return m, dir, fmt.Errorf("no draft for job %d: run scoutjobs email draft %d first", j.ID, j.ID)
	}
	return m, dir, nil
}

func preview(out io.Writer, m mail.Message, dir string) {
	fmt.Fprintf(out, "From:    %s\nTo:      %s\nSubject: %s\n", orDash(m.From), orDash(m.To), m.Subject)
	for _, a := range m.Attachments {
		fmt.Fprintf(out, "Attach:  %s\n", a)
	}
	fmt.Fprintf(out, "\n%s\n\ndraft: %s\n", strings.TrimSpace(m.Body), filepath.Join(dir, apply.DraftName))
}

func orDash(s string) string {
	if s == "" {
		return "(missing)"
	}
	return s
}

type stringList []string

func (l *stringList) String() string     { return strings.Join(*l, ",") }
func (l *stringList) Set(v string) error { *l = append(*l, v); return nil }
