package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/gmail"
	"github.com/zoolcoder/scoutjobs/internal/mail"
	"github.com/zoolcoder/scoutjobs/internal/store"
	"github.com/zoolcoder/scoutjobs/internal/web"
)

func gmailClient(s *store.Store, cfg Config) *gmail.Client {
	secret := os.Getenv("SCOUTJOBS_GMAIL_CLIENT_SECRET")
	if secret == "" {
		secret = cfg.GmailSecret
	}
	return &gmail.Client{ID: cfg.GmailClientID, Secret: secret, Home: s.Home}
}

func runGmail(s *store.Store, cfg Config, rest []string, out io.Writer) error {
	cmd, rest := sub(rest)
	c := gmailClient(s, cfg)
	switch cmd {
	case "connect":
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		t, err := c.Connect(ctx, func(u string) {
			fmt.Fprintf(out, "Open this page, pick your Google account and allow access:\n\n  %s\n\nWaiting for Google (5 minutes)...\n", u)
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "gmail connected as %s; token in %s (keep it private)\n", orDash(t.Email), filepath.Join(s.Home, gmail.TokenFile))
		return nil
	case "status":
		fs := flag.NewFlagSet("gmail status", flag.ContinueOnError)
		asJSON := fs.Bool("json", false, "")
		if _, err := parse(fs, rest); err != nil {
			return err
		}
		res := map[string]any{"client_id": cfg.GmailClientID != "", "connected": c.Connected(), "account": c.Account(), "imap_host": cfg.IMAPHost}
		return emit(out, *asJSON, res, func() {
			switch {
			case c.Connected():
				fmt.Fprintf(out, "gmail: connected as %s\n", orDash(c.Account()))
			case cfg.GmailClientID == "":
				fmt.Fprintln(out, "gmail: off (set gmail_client_id in config.yaml, then scoutjobs gmail connect)")
			default:
				fmt.Fprintln(out, "gmail: client set, not connected (scoutjobs gmail connect)")
			}
		})
	case "disconnect":
		if err := c.Disconnect(context.Background()); err != nil {
			return err
		}
		fmt.Fprintln(out, "gmail disconnected: access revoked at Google, token deleted")
		return nil
	case "draft":
		fs := flag.NewFlagSet("gmail draft", flag.ContinueOnError)
		asJSON := fs.Bool("json", false, "")
		pos, err := parse(fs, rest)
		if err != nil {
			return err
		}
		if len(pos) == 0 {
			return errors.New("usage: gmail draft <job>   (after scoutjobs email draft <job>)")
		}
		j, err := s.ResolveJob(pos[0])
		if err != nil {
			return err
		}
		d, link, err := gmailDraft(s, cfg, j)
		if err != nil {
			return err
		}
		return emit(out, *asJSON, map[string]any{"job": j.ID, "draft": d, "open": link}, func() {
			fmt.Fprintf(out, "draft created in Gmail for %s; review and send it there:\n  %s\n", j.Company, link)
		})
	}
	return errors.New("usage: gmail connect | status | disconnect | draft <job>")
}

// gmailDraft uploads the job's application e-mail (email.json from `email draft`) as a Gmail draft with
// the attachments. The Message-ID is recorded so replies match the job by thread later.
func gmailDraft(s *store.Store, cfg Config, j store.Job) (gmail.Draft, string, error) {
	c := gmailClient(s, cfg)
	m, _, err := loadDraft(s, j)
	if err != nil {
		return gmail.Draft{}, "", err
	}
	if m.From == "" {
		m.From = c.Account()
	}
	if m.From == "" {
		m.From = cfg.EmailFrom
	}
	now := time.Now()
	m.MessageID = mail.NewMessageID(m.From, now)
	raw, err := mail.Build(m, now)
	if err != nil {
		return gmail.Draft{}, "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	d, err := c.CreateDraft(ctx, raw, "")
	if err != nil {
		return d, "", err
	}
	_ = s.RecordMail(m.MessageID, j.ID, "out", 0)
	_ = s.AddEvent(j.ID, "email:gmail_draft", "to "+m.To+": "+m.Subject)
	return d, "https://mail.google.com/mail/#drafts?compose=" + d.MessageID, nil
}

// mailSource picks where `email sync` reads: Gmail when connected, IMAP when imap_host is set.
func mailSource(s *store.Store, cfg Config, via string) (string, error) {
	switch via {
	case "gmail", "imap":
		return via, nil
	case "", "auto":
		if gmailClient(s, cfg).Connected() {
			return "gmail", nil
		}
		if cfg.IMAPHost != "" {
			return "imap", nil
		}
		return "", errors.New("no inbox to read: run scoutjobs gmail connect, or set imap_host in config.yaml")
	}
	return "", fmt.Errorf("--via %q: use gmail, imap or auto", via)
}

func fetchMail(s *store.Store, cfg Config, source string, since time.Time, limit int) ([]mail.Mail, error) {
	if source == "gmail" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		return gmailClient(s, cfg).Fetch(ctx, since, limit)
	}
	return imapAccount(cfg).Fetch(since, limit)
}

// mailHooks lets the dashboard read the inbox and make Gmail drafts without importing cli.
func mailHooks(s *store.Store, cfg Config) web.MailHooks {
	return web.MailHooks{
		Status: func() (string, string) {
			c := gmailClient(s, cfg)
			if c.Connected() {
				return "gmail", c.Account()
			}
			if cfg.IMAPHost != "" {
				return "imap", imapAccount(cfg).User
			}
			return "", cfg.EmailFrom
		},
		Sync: func() (int, error) { return SyncMail(s, cfg) },
		Draft: func(id int64) (string, error) {
			j, err := s.JobByID(id)
			if err != nil {
				return "", err
			}
			_, link, err := gmailDraft(s, cfg, j)
			return link, err
		},
	}
}
