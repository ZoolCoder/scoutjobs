package mail

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	netmail "net/mail"
	"strconv"
	"strings"
	"time"
)

// IMAP is the inbox `scoutjobs email sync` reads replies from. Read-only: messages are peeked, never flagged or moved.
type IMAP struct {
	Host string // host:port; 993 = implicit TLS, anything else = STARTTLS (plain only on loopback)
	User string
	Pass string
}

// Mail is the slice of one inbox message the reply matcher needs.
type Mail struct {
	UID       uint32 `json:"uid"`
	From      string `json:"from"`
	Subject   string `json:"subject"`
	Date      string `json:"date"`
	MessageID string `json:"message_id"`
	InReplyTo string `json:"in_reply_to"`
	Thread    string `json:"thread,omitempty"` // Gmail thread id; IMAP leaves it empty
	Text      string `json:"-"`                // first bytes of the body, for keyword classification only
}

const textPeek = 4000

// Fetch returns the inbox messages received since the given day, newest last, at most limit.
func (a IMAP) Fetch(since time.Time, limit int) ([]Mail, error) {
	if a.Host == "" {
		return nil, errors.New("imap_host missing in config.yaml")
	}
	if a.Pass == "" {
		return nil, errors.New("SCOUTJOBS_IMAP_PASSWORD is not set")
	}
	c, err := a.connect()
	if err != nil {
		return nil, err
	}
	defer c.close()
	if _, err := c.cmd("LOGIN " + quote(a.User) + " " + quote(a.Pass)); err != nil {
		return nil, fmt.Errorf("imap login: %w", err)
	}
	if _, err := c.cmd("SELECT INBOX"); err != nil {
		return nil, err
	}
	lines, err := c.cmd("UID SEARCH SINCE " + since.UTC().Format("02-Jan-2006"))
	if err != nil {
		return nil, err
	}
	var uids []string
	for _, l := range lines {
		if rest, ok := strings.CutPrefix(l.text, "* SEARCH"); ok {
			uids = append(uids, strings.Fields(rest)...)
		}
	}
	if len(uids) == 0 {
		return nil, nil
	}
	if limit > 0 && len(uids) > limit {
		uids = uids[len(uids)-limit:]
	}
	lines, err = c.cmd("UID FETCH " + strings.Join(uids, ",") +
		fmt.Sprintf(" (UID BODY.PEEK[HEADER.FIELDS (FROM SUBJECT DATE MESSAGE-ID IN-REPLY-TO REFERENCES)] BODY.PEEK[TEXT]<0.%d>)", textPeek))
	if err != nil {
		return nil, err
	}
	var out []Mail
	for _, l := range lines {
		if !strings.Contains(l.text, " FETCH ") || len(l.literals) == 0 {
			continue
		}
		m := Mail{UID: uidOf(l.text)}
		for i, lit := range l.literals {
			if i == 0 {
				m.fillHeader(lit)
			} else {
				m.Text = lit
			}
		}
		out = append(out, m)
	}
	return out, nil
}

func (m *Mail) fillHeader(raw string) {
	msg, err := netmail.ReadMessage(strings.NewReader(raw + "\r\n\r\n"))
	if err != nil {
		return
	}
	dec := new(mime.WordDecoder)
	decode := func(s string) string {
		if d, err := dec.DecodeHeader(s); err == nil {
			return d
		}
		return s
	}
	m.Subject = decode(msg.Header.Get("Subject"))
	m.Date = msg.Header.Get("Date")
	m.MessageID = strings.TrimSpace(msg.Header.Get("Message-ID"))
	m.InReplyTo = strings.TrimSpace(msg.Header.Get("In-Reply-To") + " " + msg.Header.Get("References"))
	if addr, err := netmail.ParseAddress(decode(msg.Header.Get("From"))); err == nil {
		m.From = addr.Address
	} else {
		m.From = msg.Header.Get("From")
	}
}

func uidOf(line string) uint32 {
	if i := strings.Index(line, "UID "); i >= 0 {
		n, _ := strconv.ParseUint(strings.Fields(line[i+4:])[0], 10, 32)
		return uint32(n)
	}
	return 0
}

// ---- minimal IMAP4rev1 client: enough for LOGIN, SELECT, UID SEARCH, UID FETCH ----

type imapConn struct {
	conn net.Conn
	r    *bufio.Reader
	tag  int
}

type imapLine struct {
	text     string   // the response line with literal markers stripped
	literals []string // literal bodies, in order
}

func (a IMAP) connect() (*imapConn, error) {
	host, port, err := net.SplitHostPort(a.Host)
	if err != nil {
		return nil, fmt.Errorf("imap_host must be host:port: %w", err)
	}
	var conn net.Conn
	if port == "993" {
		conn, err = tls.Dial("tcp", a.Host, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	} else {
		conn, err = net.DialTimeout("tcp", a.Host, 20*time.Second)
	}
	if err != nil {
		return nil, err
	}
	c := &imapConn{conn: conn, r: bufio.NewReader(conn)}
	if _, err := c.r.ReadString('\n'); err != nil { // greeting
		conn.Close()
		return nil, err
	}
	if port != "993" {
		if _, err := c.cmd("STARTTLS"); err != nil {
			if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
				conn.Close()
				return nil, fmt.Errorf("imap: server offers no STARTTLS and %s is not local: refusing a plain-text login", host)
			}
			return c, nil
		}
		tc := tls.Client(conn, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
		c.conn, c.r = tc, bufio.NewReader(tc)
	}
	return c, nil
}

func (c *imapConn) close() {
	_, _ = c.cmd("LOGOUT")
	c.conn.Close()
}

// cmd sends one command and returns every untagged line up to the tagged OK; literals are read whole.
func (c *imapConn) cmd(s string) ([]imapLine, error) {
	c.tag++
	tag := fmt.Sprintf("a%03d", c.tag)
	_ = c.conn.SetDeadline(time.Now().Add(60 * time.Second))
	if _, err := fmt.Fprintf(c.conn, "%s %s\r\n", tag, s); err != nil {
		return nil, err
	}
	var out []imapLine
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			return out, err
		}
		line = strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(line, tag+" ") {
			status := strings.TrimPrefix(line, tag+" ")
			if strings.HasPrefix(status, "OK") {
				return out, nil
			}
			return out, errors.New(status)
		}
		cur := imapLine{}
		for {
			n, ok := literalSize(line)
			if !ok {
				cur.text += line
				break
			}
			cur.text += line[:strings.LastIndex(line, "{")]
			buf := make([]byte, n)
			if _, err := io.ReadFull(c.r, buf); err != nil {
				return out, err
			}
			cur.literals = append(cur.literals, string(buf))
			if line, err = c.r.ReadString('\n'); err != nil {
				return out, err
			}
			line = strings.TrimRight(line, "\r\n")
		}
		out = append(out, cur)
	}
}

// literalSize reports the {n} marker that ends a line when the next n bytes are a literal.
func literalSize(line string) (int, bool) {
	if !strings.HasSuffix(line, "}") {
		return 0, false
	}
	i := strings.LastIndex(line, "{")
	if i < 0 {
		return 0, false
	}
	n, err := strconv.Atoi(line[i+1 : len(line)-1])
	return n, err == nil
}

func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
