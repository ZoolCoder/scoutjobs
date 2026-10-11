// Package mail builds and sends one application e-mail. No dependency beyond the stdlib.
package mail

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/smtp"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Message struct {
	From        string   `json:"from"`
	To          string   `json:"to"`
	Subject     string   `json:"subject"`
	Body        string   `json:"body"`
	Attachments []string `json:"attachments"`
	MessageID   string   `json:"message_id,omitempty"` // set on send; replies carry it in In-Reply-To
}

// NewMessageID makes a unique id in the sender's domain, the key `scoutjobs email sync` matches replies on.
func NewMessageID(from string, now time.Time) string {
	domain := "scoutjobs.local"
	if _, d, ok := strings.Cut(from, "@"); ok && d != "" {
		domain = d
	}
	var b [8]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("<jq-%d-%x@%s>", now.UnixNano(), b, domain)
}

func (m Message) Validate() error {
	switch {
	case m.From == "":
		return errors.New("from address missing: set email_from in config.yaml")
	case m.To == "":
		return errors.New("to address missing: --to or scoutjobs job set <id> --contact")
	case m.Subject == "":
		return errors.New("subject missing")
	case strings.TrimSpace(m.Body) == "":
		return errors.New("body is empty")
	}
	for _, a := range m.Attachments {
		if _, err := os.Stat(a); err != nil {
			return fmt.Errorf("attachment %s: %w", a, err)
		}
	}
	return nil
}

// Build renders the RFC 5322 message: text body plus base64 attachments.
func Build(m Message, now time.Time) ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	fmt.Fprintf(&b, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\n",
		m.From, m.To, mime.QEncoding.Encode("utf-8", m.Subject), now.Format(time.RFC1123Z))
	if m.MessageID != "" {
		fmt.Fprintf(&b, "Message-ID: %s\r\n", m.MessageID)
	}
	fmt.Fprintf(&b, "Content-Type: multipart/mixed; boundary=%s\r\n\r\n", w.Boundary())
	text, err := w.CreatePart(textproto.MIMEHeader{"Content-Type": {"text/plain; charset=utf-8"}, "Content-Transfer-Encoding": {"8bit"}})
	if err != nil {
		return nil, err
	}
	fmt.Fprint(text, strings.ReplaceAll(m.Body, "\n", "\r\n"), "\r\n")
	for _, path := range m.Attachments {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		name := filepath.Base(path)
		ct := mime.TypeByExtension(filepath.Ext(name))
		if ct == "" {
			ct = "application/octet-stream"
		}
		part, err := w.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {fmt.Sprintf("%s; name=%q", ct, name)},
			"Content-Disposition":       {fmt.Sprintf("attachment; filename=%q", name)},
			"Content-Transfer-Encoding": {"base64"},
		})
		if err != nil {
			return nil, err
		}
		writeBase64(part, data)
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func writeBase64(w io.Writer, data []byte) {
	enc := base64.StdEncoding.EncodeToString(data)
	for len(enc) > 0 {
		n := min(76, len(enc))
		fmt.Fprint(w, enc[:n], "\r\n")
		enc = enc[n:]
	}
}

// SMTP is one outgoing account. Password comes from the environment, never from a file.
type SMTP struct {
	Host string // host:port; 465 = implicit TLS, anything else = STARTTLS
	User string
	Pass string
}

// dial connects, upgrades to TLS and authenticates. The caller closes the client.
func (a SMTP) dial(user string) (*smtp.Client, error) {
	if a.Host == "" {
		return nil, errors.New("smtp_host missing in config.yaml")
	}
	if a.Pass == "" {
		return nil, errors.New("SCOUTJOBS_SMTP_PASSWORD is not set")
	}
	host, port, err := net.SplitHostPort(a.Host)
	if err != nil {
		return nil, fmt.Errorf("smtp_host must be host:port: %w", err)
	}
	var c *smtp.Client
	if port == "465" {
		conn, err := tls.Dial("tcp", a.Host, &tls.Config{ServerName: host})
		if err != nil {
			return nil, err
		}
		if c, err = smtp.NewClient(conn, host); err != nil {
			return nil, err
		}
	} else {
		if c, err = smtp.Dial(a.Host); err != nil {
			return nil, err
		}
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(&tls.Config{ServerName: host}); err != nil {
				return nil, err
			}
		}
	}
	if a.User != "" {
		user = a.User
	}
	if err := c.Auth(smtp.PlainAuth("", user, a.Pass, host)); err != nil {
		c.Close()
		return nil, fmt.Errorf("smtp auth: %w", err)
	}
	return c, nil
}

// Check connects and logs in without sending anything: the cheapest proof the account works.
func (a SMTP) Check(user string) error {
	c, err := a.dial(user)
	if err != nil {
		return err
	}
	defer c.Close()
	return c.Quit()
}

// Send delivers raw (from Build) to m.To through the account.
func (a SMTP) Send(m Message, raw []byte) error {
	c, err := a.dial(m.From)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.Mail(m.From); err != nil {
		return err
	}
	if err := c.Rcpt(m.To); err != nil {
		return err
	}
	wc, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := wc.Write(raw); err != nil {
		return err
	}
	if err := wc.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// Mailto is the link the dashboard offers when the user prefers their own mail app. Attachments cannot ride along.
func Mailto(m Message) string {
	q := func(s string) string {
		return strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(s, "%", "%25"), "&", "%26"), "\n", "%0A")
	}
	return "mailto:" + m.To + "?subject=" + q(m.Subject) + "&body=" + q(m.Body)
}
