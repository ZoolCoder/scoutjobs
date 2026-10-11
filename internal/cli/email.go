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
		if err := smtpAccount(cfg).Send(m, raw); err != nil {
			return err
		}
		_ = os.WriteFile(filepath.Join(dir, "email-sent.eml"), raw, 0o600)
		_ = s.RecordMail(m.MessageID, j.ID, "out", 0)
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
	}
	return errors.New("usage: email draft|show|send <id> | email test | email sync")
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
