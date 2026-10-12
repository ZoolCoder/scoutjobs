package mail

import (
	"bufio"
	"encoding/base64"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuild(t *testing.T) {
	dir := t.TempDir()
	cv := filepath.Join(dir, "resume.pdf")
	os.WriteFile(cv, []byte("%PDF-1.4 fake"), 0o644)
	raw, err := Build(Message{From: "me@x.io", To: "jobs@acme.com", Subject: "Application: Go Dev", Body: "Hi,\nsee attached.", Attachments: []string{cv}}, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, want := range []string{"To: jobs@acme.com", "Subject: ", "Content-Type: multipart/mixed", "see attached.", `filename="resume.pdf"`, base64.StdEncoding.EncodeToString([]byte("%PDF-1.4 fake"))} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in\n%s", want, s)
		}
	}
	if _, err := Build(Message{To: "a@b"}, time.Now()); err == nil {
		t.Error("expected validation error")
	}
}

// fakeSMTP speaks just enough SMTP for net/smtp on localhost (PLAIN auth is allowed there without TLS).
func fakeSMTP(t *testing.T) (addr string, got *strings.Builder) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("no local listener:", err)
	}
	got = &strings.Builder{}
	go func() {
		defer ln.Close()
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		say := func(s string) { conn.Write([]byte(s + "\r\n")) }
		say("220 fake ESMTP")
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			got.WriteString(line)
			switch cmd := strings.ToUpper(strings.TrimSpace(line)); {
			case strings.HasPrefix(cmd, "EHLO"):
				say("250-fake")
				say("250 AUTH PLAIN")
			case strings.HasPrefix(cmd, "AUTH"):
				say("235 ok")
			case strings.HasPrefix(cmd, "MAIL"), strings.HasPrefix(cmd, "RCPT"):
				say("250 ok")
			case cmd == "DATA":
				say("354 go")
				for {
					l, err := r.ReadString('\n')
					if err != nil {
						return
					}
					got.WriteString(l)
					if l == ".\r\n" {
						break
					}
				}
				say("250 queued")
			case cmd == "QUIT":
				say("221 bye")
				return
			default:
				say("250 ok")
			}
		}
	}()
	return ln.Addr().String(), got
}

func TestSend(t *testing.T) {
	addr, got := fakeSMTP(t)
	m := Message{From: "me@x.io", To: "jobs@acme.com", Subject: "Hi", Body: "body"}
	raw, _ := Build(m, time.Now())
	if err := (SMTP{Host: addr, User: "me@x.io", Pass: "pw"}).Send(m, raw); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"MAIL FROM:<me@x.io>", "RCPT TO:<jobs@acme.com>", "Subject: Hi"} {
		if !strings.Contains(got.String(), want) {
			t.Errorf("server did not see %q", want)
		}
	}
	if err := (SMTP{Host: addr}).Send(m, raw); err == nil || !strings.Contains(err.Error(), "SCOUTJOBS_SMTP_PASSWORD") {
		t.Errorf("expected missing password error, got %v", err)
	}
}

func TestMailto(t *testing.T) {
	got := Mailto(Message{To: "a@b.c", Subject: "S & T", Body: "l1\nl2"})
	if got != "mailto:a@b.c?subject=S %26 T&body=l1%0Al2" {
		t.Error(got)
	}
}

func TestBuildThreadsReply(t *testing.T) {
	m := Message{From: "me@x.test", To: "hr@acme.test", Subject: "Re: Interview", Body: "Thanks", InReplyTo: "<m1@acme.test>"}
	raw, err := Build(m, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "In-Reply-To: <m1@acme.test>\r\nReferences: <m1@acme.test>\r\n") {
		t.Errorf("no thread headers:\n%s", raw)
	}
	m.InReplyTo = "<a>\r\nBcc: x@y"
	if raw, _ := Build(m, time.Now()); strings.Contains(string(raw), "Bcc") {
		t.Error("header injection through InReplyTo")
	}
}
