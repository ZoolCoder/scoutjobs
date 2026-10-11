package cli

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/mail"
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
