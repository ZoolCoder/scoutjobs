package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/mail"
	"github.com/zoolcoder/scoutjobs/internal/runner"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

func TestSyncReplies(t *testing.T) {
	t.Setenv("SCOUTJOBS_HOME", t.TempDir())
	run(t, "init")
	run(t, "job", "add", "--company", "Acme", "--title", "Go Dev", "--contact", "jobs@acme.test")
	run(t, "job", "add", "--company", "Beta", "--title", "SRE")
	run(t, "job", "set", "1", "--status", "applied")
	run(t, "job", "set", "2", "--status", "applied")
	s, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.RecordMail("<jq-2@x.test>", 2, "out", 0); err != nil {
		t.Fatal(err)
	}
	mails := []mail.Mail{
		{UID: 1, From: "noreply@newsletter.test", Subject: "Weekly digest", MessageID: "<n1@x>"},
		{UID: 2, From: "hr@beta.test", Subject: "Re: Application", MessageID: "<b1@beta.test>", InReplyTo: "<jq-2@x.test>", Text: "We would love to schedule a call for an interview."},
		{UID: 3, From: "Jobs@Acme.test", Subject: "Your application", MessageID: "<a1@acme.test>", Text: "Unfortunately we decided not to proceed."},
	}
	replies, err := syncReplies(s, mails, "imap", true)
	if err != nil || len(replies) != 2 {
		t.Fatalf("dry run: %v %+v", err, replies)
	}
	if j, _ := s.JobByID(2); j.Status != "applied" {
		t.Error("dry run changed status")
	}
	replies, err = syncReplies(s, mails, "imap", false)
	if err != nil || len(replies) != 2 {
		t.Fatalf("sync: %v %+v", err, replies)
	}
	if replies[0].Job != 2 || replies[0].Via != "thread" || replies[0].Status != "interview" {
		t.Errorf("thread match: %+v", replies[0])
	}
	if replies[1].Job != 1 || replies[1].Via != "contact" || replies[1].Status != "rejected" {
		t.Errorf("contact match: %+v", replies[1])
	}
	if j, _ := s.JobByID(2); j.Status != "interview" {
		t.Errorf("status not moved: %s", j.Status)
	}
	if j, _ := s.JobByID(1); j.Status != "rejected" {
		t.Errorf("status not moved: %s", j.Status)
	}
	events, _ := s.Events(1, 10)
	found := false
	for _, e := range events {
		if e.Kind == "reply:received" && strings.Contains(e.Note, "Jobs@Acme.test") {
			found = true
		}
	}
	if !found {
		t.Error("reply:received not logged")
	}
	if again, _ := syncReplies(s, mails, "imap", false); len(again) != 0 {
		t.Errorf("second sync must skip seen mail, got %+v", again)
	}
}

func TestParseSince(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	if got, _ := parseSince("14d", now); got != now.AddDate(0, 0, -14) {
		t.Error(got)
	}
	if got, _ := parseSince("2w", now); got != now.AddDate(0, 0, -14) {
		t.Error(got)
	}
	if got, _ := parseSince("2026-09-01", now); got.Format("2006-01-02") != "2026-09-01" {
		t.Error(got)
	}
	if _, err := parseSince("soon", now); err == nil {
		t.Error("want error")
	}
}

func TestEmailTestNeedsConfig(t *testing.T) {
	t.Setenv("SCOUTJOBS_HOME", t.TempDir())
	run(t, "init")
	err := Run([]string{"email", "test"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "email_from") {
		t.Errorf("want email_from error, got %v", err)
	}
}

func TestEmailLog(t *testing.T) {
	t.Setenv("SCOUTJOBS_HOME", t.TempDir())
	run(t, "init")
	run(t, "job", "add", "--company", "Acme", "--title", "Go Dev")
	run(t, "job", "set", "1", "--status", "applied")
	args := []string{"email", "log", "1", "--from", "Acme HR <hr@acme.test>", "--subject", "Next steps", "--snippet", "Can you do Tuesday?",
		"--date", "2026-10-10", "--thread", "t9", "--message-id", "gmail:abc", "--kind", "interview", "--status", "interview"}
	run(t, args...)
	run(t, args...) // the message id dedupes a second run
	s, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ms, err := s.Mails(1, 0)
	if err != nil || len(ms) != 1 {
		t.Fatalf("mails %+v %v", ms, err)
	}
	if m := ms[0]; m.From != "hr@acme.test" || m.Thread != "t9" || m.Kind != "interview" || m.Source != "connector" || !strings.HasPrefix(m.Date, "2026-10-10") {
		t.Errorf("logged %+v", m)
	}
	if j, _ := s.JobByID(1); j.Status != "interview" {
		t.Errorf("status %q, want interview", j.Status)
	}
	if err := Run([]string{"email", "log", "1", "--from", "x@y"}, io.Discard); err == nil {
		t.Error("log without --subject should fail")
	}
}

func TestReviewCandidates(t *testing.T) {
	t.Setenv("SCOUTJOBS_HOME", t.TempDir())
	run(t, "init")
	run(t, "job", "add", "--company", "Acme", "--title", "Go Dev", "--contact", "jobs@acme.test")
	run(t, "job", "add", "--company", "Globex", "--title", "SRE")
	s, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mails := []mail.Mail{
		{From: "jobs@acme.test", Subject: "Re: Go Dev", MessageID: "<matched@acme>"},
		{From: "noreply@newsletter.test", Subject: "Weekly digest", MessageID: "<n1@x>"},
		{From: "no-reply@us.greenhouse.io", Subject: "Thanks", MessageID: "<g1@x>"},
		{From: "talent@globex-careers.test", Subject: "Globex: next steps", Text: "We would like to schedule an interview", MessageID: "<x1@x>"},
		{From: "someone@corp.test", Subject: "Your application for Data Engineer", MessageID: "<y1@x>"},
	}
	got := reviewCandidates(s, mails)
	if len(got) != 3 {
		t.Fatalf("want 3 candidates, got %+v", got)
	}
	if got[0].MessageID != "<g1@x>" || got[1].Company != "Globex" || got[1].Hint != "interview" || got[2].MessageID != "<y1@x>" {
		t.Errorf("%+v", got)
	}
}

func TestGmailSetupImportsDownload(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SCOUTJOBS_HOME", home)
	run(t, "init")
	file := filepath.Join(t.TempDir(), "client_secret_1.json")
	if err := os.WriteFile(file, []byte(`{"installed":{"client_id":"1-a.apps.googleusercontent.com","client_secret":"s"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out := run(t, "gmail", "setup", "--file", file, "--no-connect")
	if !strings.Contains(out, "client imported") || strings.Contains(out, `"s"`) {
		t.Errorf("setup output: %s", out)
	}
	if st := run(t, "gmail", "status"); !strings.Contains(st, "client set, not connected") {
		t.Errorf("status after setup: %s", st)
	}
	if out := run(t, "gmail", "setup", "--no-connect"); !strings.Contains(out, "already set up") {
		t.Errorf("second setup: %s", out)
	}
}

func TestSentSinceDropsMailFromBeforeTheFirstJob(t *testing.T) {
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	got := sentSince([]mail.Mail{
		{Subject: "old", Date: "Thu, 1 Oct 2026 09:00:00 +0000"},
		{Subject: "new", Date: "Thu, 1 Oct 2026 13:00:00 +0000"},
		{Subject: "api", Date: "2026-10-02T08:00:00Z"},
		{Subject: "undated", Date: "someday"},
	}, start)
	var subjects []string
	for _, m := range got {
		subjects = append(subjects, m.Subject)
	}
	if strings.Join(subjects, ",") != "new,api,undated" {
		t.Fatalf("kept %v", subjects)
	}
}

func TestFetchMailReadsNothingBeforeAnyJob(t *testing.T) {
	t.Setenv("SCOUTJOBS_HOME", t.TempDir())
	run(t, "init")
	s, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, ok := s.Started(); ok {
		t.Fatal("no job yet, so no start")
	}
	// imap_host is unset: reaching the server would fail, so a nil error proves nothing was fetched
	if mails, err := fetchMail(s, Config{}, "imap", time.Now().AddDate(-1, 0, 0), 10); err != nil || mails != nil {
		t.Fatalf("got %v, %v", mails, err)
	}
	run(t, "job", "add", "--company", "Acme", "--title", "Go Dev")
	if st, ok := s.Started(); !ok || time.Since(st) > time.Minute {
		t.Fatalf("started %v %v", st, ok)
	}
}

func TestMatchReplyByThreadAndCompany(t *testing.T) {
	t.Setenv("SCOUTJOBS_HOME", t.TempDir())
	run(t, "init")
	run(t, "job", "add", "--company", "Acme", "--title", "Go Dev")
	run(t, "job", "add", "--company", "Globex", "--title", "SRE")
	run(t, "job", "set", "2", "--status", "applied")
	s, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.RecordDraft("<d1@me>", 1, "t-acme"); err != nil {
		t.Fatal(err)
	}
	// Gmail gives the sent draft a new Message-ID, so only the thread ties the reply to the job
	if j, how, ok := matchReply(s, mail.Mail{From: "x@other.test", Thread: "t-acme", InReplyTo: "<new@mail.gmail.com>"}); !ok || j.ID != 1 || how != "thread" {
		t.Errorf("thread match: %d %q %v", j.ID, how, ok)
	}
	ats := mail.Mail{From: "no-reply@us.greenhouse.io", Subject: "Thank you for applying to Globex"}
	if j, how, ok := matchReply(s, ats); !ok || j.ID != 2 || how != "company" {
		t.Errorf("company match: %d %q %v", j.ID, how, ok)
	}
	// Acme is not applied to yet, and "Globex" inside a word is not the company
	for _, m := range []mail.Mail{{From: "no-reply@us.greenhouse.io", Subject: "Thank you for applying to Acme"},
		{From: "news@x.test", Subject: "Globextra weekly"}} {
		if j, _, ok := matchReply(s, m); ok {
			t.Errorf("%q matched job %d", m.Subject, j.ID)
		}
	}
	if th, ref := s.JobThread(1); th != "t-acme" || ref != "<d1@me>" {
		t.Errorf("job thread %q %q", th, ref)
	}
}

func TestMailSourceAgent(t *testing.T) {
	t.Setenv("SCOUTJOBS_HOME", t.TempDir())
	run(t, "init")
	s, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg := Config{IMAPHost: "imap.mail.me.com:993"}
	if src, _ := mailSource(s, cfg, "auto"); src != "imap" {
		t.Errorf("before choosing the agent: %q", src)
	}
	if err := s.SetSetting(runner.MailVia, "agent"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting(runner.MailAgent, "codex"); err != nil {
		t.Fatal(err)
	}
	if src, _ := mailSource(s, cfg, ""); src != "agent" {
		t.Errorf("agent chosen: %q", src)
	}
	if got := mailAgentName(s, Config{Agent: "claude"}); got != "codex" {
		t.Errorf("mail agent: %q", got)
	}
	if err := agentSync(s, cfg, time.Now(), &mcpOutput{}); err == nil || !strings.Contains(err.Error(), "email_log") {
		t.Errorf("an MCP client must not start another agent: %v", err)
	}
	t.Setenv("SCOUTJOBS_RUNNER", "1")
	if err := agentSync(s, cfg, time.Now(), io.Discard); err == nil || !strings.Contains(err.Error(), "email_log") {
		t.Errorf("the agent's own email_sync call must not start another agent: %v", err)
	}
	if _, err := draftTask(s, cfg, store.Job{ID: 1, Company: "Acme"}, "nonsense"); err == nil {
		t.Error("unknown kind accepted")
	}
}
