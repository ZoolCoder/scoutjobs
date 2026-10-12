package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/apply"
	"github.com/zoolcoder/scoutjobs/internal/calsync"
	"github.com/zoolcoder/scoutjobs/internal/gmail"
	"github.com/zoolcoder/scoutjobs/internal/mail"
	"github.com/zoolcoder/scoutjobs/internal/runner"
	"github.com/zoolcoder/scoutjobs/internal/store"
	"github.com/zoolcoder/scoutjobs/internal/web"
)

// gmailClient takes the OAuth client from config.yaml when set there, else from the file gmail setup
// imported. A config.yaml ID without a secret takes the secret of the imported client with the same ID.
func gmailClient(s *store.Store, cfg Config) *gmail.Client {
	id, secret := cfg.GmailClientID, cfg.GmailSecret
	if f := gmail.LoadCredentials(s.Home); id == "" || (secret == "" && f.ID == id) {
		id, secret = f.ID, f.Secret
	}
	if env := os.Getenv("SCOUTJOBS_GMAIL_CLIENT_SECRET"); env != "" {
		secret = env
	}
	return &gmail.Client{ID: id, Secret: secret, Home: s.Home}
}

// Google Cloud pages for a Desktop OAuth client, in the order setup needs them.
const gmailSteps = `Google setup: a Desktop OAuth client in your own Google Cloud project (about 3 minutes, once).

  1. Create a project (any name, e.g. scoutjobs):
     https://console.cloud.google.com/projectcreate
  2. Enable the Gmail and Calendar APIs in it:
     ` + gmail.EnableAPIs + `
  3. Configure the consent screen: Get started, app name ScoutJobs, audience External, your e-mail:
     https://console.cloud.google.com/auth/overview/create
  4. Publish the app (Publish app). Do not skip it: in Testing mode Google disconnects you every 7 days.
     https://console.cloud.google.com/auth/audience
  5. Create the client: Application type Desktop app, Create, then Download JSON:
     https://console.cloud.google.com/auth/clients/create

Google will say it has not verified the app: it is your own app, so choose Advanced > Go to ScoutJobs.
Tick every box on Google's screen: mail, drafts and calendar.
ScoutJobs reads inbox metadata and your calendar, and creates drafts.
It never sends, labels or deletes mail, and in your calendar it writes only the interviews it adds.
`

// setupGmail imports the client from --file, or waits for Google's download to land in ~/Downloads.
func setupGmail(s *store.Store, cfg Config, file string, wait time.Duration, out io.Writer) error {
	if file == "" && gmailClient(s, cfg).ID != "" {
		fmt.Fprintln(out, "gmail client already set up (--file <client_secret_….json> replaces it)")
		return nil
	}
	if file == "" {
		fmt.Fprint(out, gmailSteps)
		dir := downloadsDir()
		if _, err := os.ReadDir(dir); err != nil {
			return fmt.Errorf("cannot read %s (on macOS, the terminal needs access to Downloads); rerun with --file <path to the downloaded JSON>", dir)
		}
		if file = gmail.FindDownload(dir, time.Time{}); file == "" {
			fmt.Fprintf(out, "\nWaiting for client_secret_….json in %s (%s; or rerun with --file <path>)...\n", dir, wait)
			deadline := time.Now().Add(wait)
			for file == "" && time.Now().Before(deadline) {
				time.Sleep(2 * time.Second)
				file = gmail.FindDownload(dir, time.Time{})
			}
			if file == "" {
				return errors.New("no client file arrived; rerun scoutjobs gmail setup --file <path to the downloaded JSON>")
			}
		}
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	c, err := gmail.ParseCredentials(b)
	if err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	if err := gmail.SaveCredentials(s.Home, c); err != nil {
		return err
	}
	fmt.Fprintf(out, "\nclient imported from %s into %s (0600); you can delete the download\n", file, filepath.Join(s.Home, gmail.ClientFile))
	if cfg.GmailClientID != "" {
		fmt.Fprintln(out, "note: gmail_client_id in config.yaml still wins; remove it to use the imported client")
	}
	return nil
}

func downloadsDir() string {
	h, _ := os.UserHomeDir()
	return filepath.Join(h, "Downloads")
}

func connectGmail(s *store.Store, cfg Config, out io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	t, err := gmailClient(s, cfg).Connect(ctx, func(u string) {
		fmt.Fprintf(out, "\nOpen this page, pick your Google account and allow access:\n\n  %s\n\nWaiting for Google (5 minutes)...\n", u)
		openBrowser(u)
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "google connected as %s; token in %s (keep it private)\n", orDash(t.Email), filepath.Join(s.Home, gmail.TokenFile))
	printGrant(out, gmailClient(s, cfg).Status())
	return nil
}

// printGrant warns about boxes left unticked on Google's screen and about a Testing-mode app.
func printGrant(out io.Writer, st gmail.State) {
	var missing []string
	if !st.Mail {
		missing = append(missing, "reading mail")
	}
	if !st.Drafts {
		missing = append(missing, "creating drafts")
	}
	if !st.CalendarWrite {
		missing = append(missing, "calendar")
	}
	if len(missing) > 0 {
		fmt.Fprintf(out, "not allowed: %s. Run scoutjobs gmail connect again and tick every box.\n", strings.Join(missing, ", "))
	}
	if st.Testing {
		fmt.Fprintf(out, "your Google Cloud app is in Testing mode: Google disconnects it on %s. Publish it at "+
			"https://console.cloud.google.com/auth/audience, then run scoutjobs gmail connect again\n", st.Ends.Format("Jan 2"))
	}
}

// testGoogle checks mail and calendar separately, so one failing does not hide the other.
func testGoogle(c *gmail.Client, out io.Writer) error {
	if !c.Connected() {
		return errors.New("gmail not connected: run scoutjobs gmail setup")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	failed := false
	if ms, err := c.Fetch(ctx, time.Now().AddDate(0, 0, -7), 5); err != nil {
		failed = true
		fmt.Fprintf(out, "mail:     FAIL %v\n", err)
	} else {
		fmt.Fprintf(out, "mail:     ok, %s (%d recent inbox messages read, metadata only)\n", orDash(c.Account()), len(ms))
	}
	switch evs, err := c.Events(ctx, time.Now(), time.Now().AddDate(0, 0, 14)); {
	case !c.HasCalendar():
		fmt.Fprintln(out, "calendar: off (scoutjobs gmail connect, and allow Calendar)")
	case err != nil:
		failed = true
		fmt.Fprintf(out, "calendar: FAIL %v\n", err)
	default:
		fmt.Fprintf(out, "calendar: ok (%d events in the next 14 days, read-only)\n", len(evs))
	}
	if failed {
		return errors.New("google test failed")
	}
	return nil
}

func runGmail(s *store.Store, cfg Config, rest []string, out io.Writer) error {
	cmd, rest := sub(rest)
	c := gmailClient(s, cfg)
	switch cmd {
	case "setup":
		fs := flag.NewFlagSet("gmail setup", flag.ContinueOnError)
		file := fs.String("file", "", "client JSON downloaded from Google Cloud; default: newest client_secret_*.json in ~/Downloads")
		wait := fs.Duration("wait", 15*time.Minute, "how long to wait for the download")
		noConnect := fs.Bool("no-connect", false, "import the client only")
		if _, err := parse(fs, rest); err != nil {
			return err
		}
		if err := setupGmail(s, cfg, *file, *wait, out); err != nil {
			return err
		}
		if *noConnect || c.Connected() && *file == "" {
			return nil
		}
		return connectGmail(s, cfg, out)
	case "connect":
		return connectGmail(s, cfg, out)
	case "status":
		fs := flag.NewFlagSet("gmail status", flag.ContinueOnError)
		asJSON := fs.Bool("json", false, "")
		if _, err := parse(fs, rest); err != nil {
			return err
		}
		st := c.Status()
		res := map[string]any{"client_id": c.ID != "", "connected": c.Connected(), "account": c.Account(), "calendar": c.HasCalendar(),
			"calendar_sync": c.CanWriteCalendar(), "imap_host": cfg.IMAPHost, "mail": st.Mail, "drafts": st.Drafts,
			"testing": st.Testing, "problem": st.Problem}
		return emit(out, *asJSON, res, func() {
			switch {
			case st.Problem != "":
				fmt.Fprintf(out, "gmail: disconnected by Google: %s\n", st.Problem)
			case c.Connected():
				fmt.Fprintf(out, "gmail: connected as %s; calendar %s\n", orDash(c.Account()), calendarState(c))
				printGrant(out, st)
			case c.ID == "":
				fmt.Fprintln(out, "gmail: off (scoutjobs gmail setup connects it in a few minutes)")
			default:
				fmt.Fprintln(out, "gmail: client set, not connected (scoutjobs gmail connect)")
			}
		})
	case "test":
		return testGoogle(c, out)
	case "disconnect":
		if err := c.Disconnect(context.Background()); err != nil {
			return err
		}
		fmt.Fprintln(out, "gmail disconnected: access revoked at Google, token deleted")
		return nil
	case "draft":
		fs := flag.NewFlagSet("gmail draft", flag.ContinueOnError)
		asJSON := fs.Bool("json", false, "")
		kind := fs.String("kind", "", "application (default) | "+strings.Join(apply.ProposalKinds, " | "))
		pos, err := parse(fs, rest)
		if err != nil {
			return err
		}
		if len(pos) == 0 {
			return errors.New("usage: gmail draft <job> [--kind followup|thanks|offer-accept|offer-negotiate|offer-decline]")
		}
		j, err := s.ResolveJob(pos[0])
		if err != nil {
			return err
		}
		d, link, err := gmailDraft(s, cfg, j, *kind)
		if err != nil {
			return err
		}
		return emit(out, *asJSON, map[string]any{"job": j.ID, "draft": d, "open": link}, func() {
			fmt.Fprintf(out, "draft created in Gmail for %s; review and send it there:\n  %s\n", j.Company, link)
		})
	}
	return errors.New("usage: gmail setup [--file client.json] | connect | status | test | disconnect | draft <job>")
}

// gmailDraft uploads the job's application e-mail (email.json from `email draft`) as a Gmail draft with
// the attachments. The Message-ID is recorded so replies match the job by thread later.
func gmailDraft(s *store.Store, cfg Config, j store.Job, kind string) (gmail.Draft, string, error) {
	c := gmailClient(s, cfg)
	m, _, err := loadDraft(s, j)
	if kind != "" && kind != "application" {
		if !apply.ValidKind(kind) {
			return gmail.Draft{}, "", fmt.Errorf("unknown e-mail kind %q, want one of %s", kind, strings.Join(apply.ProposalKinds, ", "))
		}
		m, err = proposal(s, cfg, j, kind)
	}
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
	thread := ""
	if kind != "" && kind != "application" { // a follow-up or thank-you answers the conversation so far
		var ref string
		thread, ref = s.JobThread(j.ID)
		m.InReplyTo = ref
	}
	raw, err := mail.Build(m, now)
	if err != nil {
		return gmail.Draft{}, "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	d, err := c.CreateDraft(ctx, raw, thread)
	if err != nil {
		return d, "", err
	}
	_ = s.RecordDraft(m.MessageID, j.ID, d.Thread)
	_ = s.AddEvent(j.ID, "email:gmail_draft", "to "+m.To+": "+m.Subject)
	return d, gmail.DraftURL(c.Account(), d.MessageID), nil
}

// proposal is the saved version of a proposed e-mail, else the one proposed now.
func proposal(s *store.Store, cfg Config, j store.Job, kind string) (mail.Message, error) {
	if m, err := apply.LoadProposal(apply.Dir(s, j), kind); err == nil {
		return m, nil
	}
	for _, p := range apply.Proposals(s, j, cfg.FollowUpDays, time.Now()) {
		if p.Kind == kind {
			return p.Msg, nil
		}
	}
	return mail.Message{}, fmt.Errorf("no %s e-mail is proposed for job %d now", kind, j.ID)
}

// mailSource picks where `email sync` reads: the AI agent when the user chose it on the dashboard,
// else Gmail when connected, else IMAP when imap_host is set.
func mailSource(s *store.Store, cfg Config, via string) (string, error) {
	switch via {
	case "agent", "gmail", "imap":
		return via, nil
	case "", "auto":
		switch s.Setting(runner.MailVia) {
		case "agent":
			return "agent", nil
		case "imap":
			if cfg.IMAPHost != "" {
				return "imap", nil
			}
		}
		if gmailClient(s, cfg).Connected() {
			return "gmail", nil
		}
		if cfg.IMAPHost != "" {
			return "imap", nil
		}
		return "", errors.New("no inbox to read: connect one on the dashboard's Inbox page, run scoutjobs gmail setup, or set imap_host in config.yaml")
	}
	return "", fmt.Errorf("--via %q: use agent, gmail, imap or auto", via)
}

// fetchMail never reads mail from before the first tracked job: an older message cannot be a reply to
// an application, and skipping it keeps the inbox, and the agent that triages it, small.
func fetchMail(s *store.Store, cfg Config, source string, since time.Time, limit int) ([]mail.Mail, error) {
	start, ok := s.Started()
	if !ok {
		return nil, nil
	}
	if since.Before(start) {
		since = start
	}
	var mails []mail.Mail
	var err error
	if source == "gmail" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		mails, err = gmailClient(s, cfg).Fetch(ctx, since, limit)
	} else {
		mails, err = imapAccount(cfg).Fetch(since, limit)
	}
	return sentSince(mails, start), err
}

// sentSince drops messages dated before start; IMAP searches by whole day, so the morning before the first job slips in.
func sentSince(mails []mail.Mail, start time.Time) []mail.Mail {
	out := mails[:0]
	for _, m := range mails {
		if t, err := time.Parse(time.RFC3339, mailDate(m.Date)); err == nil && t.Before(start) {
			continue
		}
		out = append(out, m)
	}
	return out
}

// mailHooks lets the dashboard read the inbox and make Gmail drafts without importing cli.
func mailHooks(s *store.Store, cfg Config) web.MailHooks {
	return web.MailHooks{
		Status: func() (string, string) {
			src, _ := mailSource(s, cfg, "")
			switch src {
			case "agent":
				return src, mailAgentName(s, cfg)
			case "gmail":
				return src, gmailClient(s, cfg).Account()
			case "imap":
				return src, imapAccount(cfg).User
			}
			return "", cfg.EmailFrom
		},
		Test: func() error {
			src, err := mailSource(s, cfg, "")
			switch src {
			case "gmail":
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				_, err = gmailClient(s, LoadConfig(s.Home)).Fetch(ctx, time.Now(), 1)
			case "imap":
				_, err = imapAccount(cfg).Fetch(time.Now(), 1)
			}
			return err
		},
		Sync:  func() (int, error) { return SyncMail(s, cfg) },
		Gmail: func() *gmail.Client { return gmailClient(s, LoadConfig(s.Home)) }, // config.yaml edits apply without a restart
		Compose: func(id int64, kind string) (runner.MailTask, error) {
			j, err := s.JobByID(id)
			if err != nil {
				return runner.MailTask{}, err
			}
			return draftTask(s, cfg, j, kind)
		},
		Draft: func(id int64, kind string) (string, error) {
			j, err := s.JobByID(id)
			if err != nil {
				return "", err
			}
			_, link, err := gmailDraft(s, cfg, j, kind)
			return link, err
		},
	}
}

// openBrowser is best effort: the URL is printed too, for SSH sessions and headless boxes.
func openBrowser(u string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	if cmd.Start() == nil {
		go func() { _ = cmd.Wait() }()
	}
}

func calendarState(c *gmail.Client) string {
	switch {
	case c.CanWriteCalendar():
		return "on (two-way sync)"
	case c.HasCalendar():
		return "read-only (scoutjobs gmail connect again to sync interviews)"
	}
	return "off"
}

// runCalendar syncs interviews with Google Calendar both ways; the dashboard does the same every few minutes.
func runCalendar(s *store.Store, cfg Config, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] != "sync" {
		return errors.New("usage: calendar sync [--json]")
	}
	fs := flag.NewFlagSet("calendar sync", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "")
	if _, err := parse(fs, args[1:]); err != nil {
		return err
	}
	c := gmailClient(s, cfg)
	if !c.HasCalendar() {
		return errors.New("calendar is not connected: run scoutjobs gmail connect and allow Calendar")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	rep, err := calsync.Sync(ctx, s, c, time.Now())
	if e := emit(out, *asJSON, rep, func() { fmt.Fprintln(out, rep.String()) }); e != nil {
		return e
	}
	if err == nil && !c.CanWriteCalendar() {
		fmt.Fprintln(os.Stderr, "calendar is read-only: interviews are read from it, not added; scoutjobs gmail connect again to allow that")
	}
	return err
}
