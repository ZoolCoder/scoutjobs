package mail

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

const header1 = "From: Jane Recruiter <jane@acme.test>\r\nSubject: =?utf-8?q?Re=3A_Application?=\r\nMessage-ID: <r1@acme.test>\r\nIn-Reply-To: <jq-1@x.test>\r\n"
const text1 = "Hi,\r\nunfortunately we went with other candidates.\r\n"

// fakeIMAP answers LOGIN, SELECT, UID SEARCH and UID FETCH with one message and refuses STARTTLS (loopback is allowed plain).
func fakeIMAP(t *testing.T) (addr string, got *strings.Builder) {
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
		say("* OK fake IMAP ready")
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			got.WriteString(line)
			tag, cmd, _ := strings.Cut(strings.TrimSpace(line), " ")
			switch c := strings.ToUpper(cmd); {
			case strings.HasPrefix(c, "STARTTLS"):
				say(tag + " BAD no tls here")
			case strings.HasPrefix(c, "LOGIN"):
				if strings.Contains(cmd, `"pw"`) {
					say(tag + " OK logged in")
				} else {
					say(tag + " NO bad password")
				}
			case strings.HasPrefix(c, "SELECT"):
				say("* 1 EXISTS")
				say(tag + " OK [READ-WRITE] SELECT done")
			case strings.HasPrefix(c, "UID SEARCH"):
				say("* SEARCH 7")
				say(tag + " OK search done")
			case strings.HasPrefix(c, "UID FETCH"):
				say(fmt.Sprintf("* 1 FETCH (UID 7 BODY[HEADER.FIELDS (FROM SUBJECT DATE MESSAGE-ID IN-REPLY-TO REFERENCES)] {%d}", len(header1)))
				conn.Write([]byte(header1))
				say(fmt.Sprintf(" BODY[TEXT]<0> {%d}", len(text1)))
				conn.Write([]byte(text1))
				say(")")
				say(tag + " OK fetch done")
			case strings.HasPrefix(c, "LOGOUT"):
				say("* BYE")
				say(tag + " OK bye")
				return
			default:
				say(tag + " BAD unknown")
			}
		}
	}()
	return ln.Addr().String(), got
}

func TestIMAPFetch(t *testing.T) {
	addr, got := fakeIMAP(t)
	mails, err := (IMAP{Host: addr, User: "me@x.test", Pass: "pw"}).Fetch(time.Now().AddDate(0, 0, -7), 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(mails) != 1 {
		t.Fatalf("want 1 mail, got %d", len(mails))
	}
	m := mails[0]
	if m.UID != 7 || m.From != "jane@acme.test" || m.Subject != "Re: Application" || m.MessageID != "<r1@acme.test>" || !strings.Contains(m.InReplyTo, "<jq-1@x.test>") || !strings.Contains(m.Text, "other candidates") {
		t.Errorf("parsed badly: %+v", m)
	}
	s := got.String()
	if !strings.Contains(s, "BODY.PEEK[") || strings.Contains(s, "STORE") {
		t.Error("fetch must peek, never flag")
	}
	if !strings.Contains(s, `LOGIN "me@x.test" "pw"`) {
		t.Errorf("login line: %s", s)
	}
	if _, err := (IMAP{Host: addr, User: "me@x.test"}).Fetch(time.Now(), 1); err == nil || !strings.Contains(err.Error(), "SCOUTJOBS_IMAP_PASSWORD") {
		t.Errorf("missing password: %v", err)
	}
}

func TestIMAPBadLogin(t *testing.T) {
	addr, _ := fakeIMAP(t)
	if _, err := (IMAP{Host: addr, User: "me@x.test", Pass: "wrong"}).Fetch(time.Now(), 1); err == nil || !strings.Contains(err.Error(), "bad password") {
		t.Errorf("want login error, got %v", err)
	}
}

func TestClassify(t *testing.T) {
	cases := map[string]string{
		"Unfortunately we will not move forward":     "rejected",
		"Let's schedule a call: interview next week": "interview",
		"Thanks for applying, we received it":        "",
		"Leider haben wir uns anders entschieden":    "rejected",
	}
	for text, want := range cases {
		if got := Classify("Re: Application", text); got != want {
			t.Errorf("%q: got %q want %q", text, got, want)
		}
	}
}

func TestNewMessageID(t *testing.T) {
	id := NewMessageID("me@x.test", time.Now())
	if !strings.HasPrefix(id, "<jq-") || !strings.HasSuffix(id, "@x.test>") {
		t.Error(id)
	}
	raw, _ := Build(Message{From: "a@b.c", To: "d@e.f", Subject: "s", Body: "b", MessageID: id}, time.Now())
	if !strings.Contains(string(raw), "Message-ID: "+id) {
		t.Error("Message-ID header missing")
	}
}
