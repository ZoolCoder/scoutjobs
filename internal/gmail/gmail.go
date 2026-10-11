// Package gmail talks to the Gmail API with the user's own OAuth desktop client: read inbox
// metadata for reply matching, and create drafts. It never sends, labels, archives or deletes.
// Stdlib only, like the rest of the binary.
package gmail

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/mail"
)

// Scopes: read for reply matching, compose for drafts, calendar events to read the user's calendar and
// keep the interviews ScoutJobs tracks in it. Compose cannot send without the user pressing Send.
const Scopes = MailScope + " " + ComposeScope + " " + CalendarEventsScope

// MailScope reads inbox metadata; ComposeScope creates drafts. Google's consent screen lets the user
// untick either, so each is checked before use.
const (
	MailScope    = "https://www.googleapis.com/auth/gmail.readonly"
	ComposeScope = "https://www.googleapis.com/auth/gmail.compose"
)

const TokenFile = "gmail-token.json"

// Client holds the OAuth client from config.yaml and where the token lives. The URLs default to Google's.
type Client struct {
	ID, Secret string
	Home       string
	HTTP       *http.Client
	AuthURL    string
	TokenURL   string
	APIBase    string
	RevokeURL  string
	CalBase    string
}

// Token is what gmail-token.json holds. The refresh token is the long-lived secret; the file is 0600.
type Token struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	Expiry       time.Time `json:"expiry"`
	Email        string    `json:"email"`
	Scope        string    `json:"scope,omitempty"` // granted scopes; tokens from before Calendar have none
	// RefreshExpiry is when Google ends the grant: set only for an app still in Testing mode (7 days)
	RefreshExpiry time.Time `json:"refresh_expiry,omitempty"`
	// Problem is why the last refresh failed for good (expired or revoked); cleared on a new connect
	Problem string `json:"problem,omitempty"`
}

func (c *Client) defaults() {
	if c.HTTP == nil {
		c.HTTP = &http.Client{Timeout: 30 * time.Second}
	}
	if c.AuthURL == "" {
		c.AuthURL = "https://accounts.google.com/o/oauth2/v2/auth"
	}
	if c.TokenURL == "" {
		c.TokenURL = "https://oauth2.googleapis.com/token"
	}
	if c.APIBase == "" {
		c.APIBase = "https://gmail.googleapis.com/gmail/v1/users/me"
	}
	if c.RevokeURL == "" {
		c.RevokeURL = "https://oauth2.googleapis.com/revoke"
	}
	if c.CalBase == "" {
		c.CalBase = "https://www.googleapis.com/calendar/v3"
	}
}

func (c *Client) tokenPath() string { return filepath.Join(c.Home, TokenFile) }

// Connected reports whether a token was saved; it does not call Google.
func (c *Client) Connected() bool {
	_, err := os.Stat(c.tokenPath())
	return err == nil
}

// Account returns the connected address, "" when not connected.
func (c *Client) Account() string {
	t, err := c.load()
	if err != nil {
		return ""
	}
	return t.Email
}

func (c *Client) load() (Token, error) {
	var t Token
	b, err := os.ReadFile(c.tokenPath())
	if err != nil {
		return t, errors.New("gmail not connected: run scoutjobs gmail connect")
	}
	err = json.Unmarshal(b, &t)
	return t, err
}

// save writes atomically: the dashboard's calendar sync and a mail sync may refresh at the same time,
// and neither may read a half-written token.
func (c *Client) save(t Token) error {
	b, _ := json.MarshalIndent(t, "", "  ")
	tmp := c.tokenPath() + "." + randomString(6) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, c.tokenPath()); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func randomString(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// Connect runs the installed-app flow: a one-shot listener on 127.0.0.1, the consent page in the
// browser, PKCE so an intercepted code is useless. show receives the URL to open.
func (c *Client) Connect(ctx context.Context, show func(authURL string)) (Token, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return Token{}, err
	}
	defer ln.Close()
	authURL, p, err := c.Begin("http://" + ln.Addr().String() + "/callback")
	if err != nil {
		return Token{}, err
	}
	show(authURL)

	type result struct {
		code string
		err  error
	}
	done := make(chan result, 1)
	hs := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/callback" {
			http.NotFound(w, r)
			return
		}
		code, err := p.Code(r.URL.Query())
		if err != nil {
			http.Error(w, "Gmail was not connected: "+err.Error(), http.StatusBadRequest)
		} else {
			fmt.Fprintln(w, "Google connected to ScoutJobs. You can close this tab.")
		}
		done <- result{code, err}
	})}
	go func() { _ = hs.Serve(ln) }()
	defer func() { _ = hs.Close() }()

	var res result
	select {
	case res = <-done:
	case <-ctx.Done():
		return Token{}, errors.New("gave up waiting for the browser")
	}
	if res.err != nil {
		return Token{}, res.err
	}
	return c.Finish(ctx, p, res.code)
}

// grant posts to the token endpoint; refresh keeps the old refresh token when Google sends none.
func (c *Client) grant(ctx context.Context, form url.Values) (Token, error) {
	form.Set("client_id", c.ID)
	if c.Secret != "" {
		form.Set("client_secret", c.Secret)
	}
	req, _ := http.NewRequestWithContext(ctx, "POST", c.TokenURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Token{}, err
	}
	defer resp.Body.Close()
	var out struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		RefreshIn    int    `json:"refresh_token_expires_in"` // only for apps in Testing mode
		Scope        string `json:"scope"`
		Error        string `json:"error"`
		Desc         string `json:"error_description"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return Token{}, fmt.Errorf("token endpoint: %s", resp.Status)
	}
	if out.Error != "" || out.AccessToken == "" {
		return Token{}, fmt.Errorf("token endpoint: %s %s", out.Error, out.Desc)
	}
	t := Token{AccessToken: out.AccessToken, RefreshToken: out.RefreshToken, Scope: out.Scope, Expiry: time.Now().Add(time.Duration(out.ExpiresIn) * time.Second)}
	if out.RefreshIn > 0 {
		t.RefreshExpiry = time.Now().Add(time.Duration(out.RefreshIn) * time.Second)
	}
	return t, nil
}

// access returns a valid access token, refreshing and saving it when it is about to expire.
func (c *Client) access(ctx context.Context) (string, error) {
	c.defaults()
	t, err := c.load()
	if err != nil {
		return "", err
	}
	if t.AccessToken != "" && time.Until(t.Expiry) > time.Minute {
		return t.AccessToken, nil
	}
	if t.RefreshToken == "" {
		return "", errors.New("gmail token has no refresh token: run scoutjobs gmail connect again")
	}
	n, err := c.grant(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {t.RefreshToken}})
	if err != nil {
		if strings.Contains(err.Error(), "invalid_grant") { // for good: only a new connect helps
			t.Problem = expiredProblem(t)
			_ = c.save(t)
			return "", errors.New(t.Problem)
		}
		return "", fmt.Errorf("google refresh: %w", err)
	}
	t.AccessToken, t.Expiry, t.Problem = n.AccessToken, n.Expiry, ""
	if n.RefreshToken != "" {
		t.RefreshToken = n.RefreshToken
	}
	if !n.RefreshExpiry.IsZero() {
		t.RefreshExpiry = n.RefreshExpiry
	}
	if n.Scope != "" {
		t.Scope = n.Scope
	}
	return t.AccessToken, c.save(t)
}

// expiredProblem explains a refresh Google refused for good; a Testing-mode app is the usual cause.
func expiredProblem(t Token) string {
	if !t.RefreshExpiry.IsZero() {
		return "Google ended the connection after 7 days because your Google Cloud app is in Testing mode: " +
			"publish it (Google Cloud > Audience > Publish app), then connect Google again"
	}
	return "Google ended the connection (access was revoked or the password changed): connect Google again"
}

func (c *Client) call(ctx context.Context, tok, method, path string, body, out any) error {
	return c.callURL(ctx, tok, method, c.APIBase+path, body, out)
}

// callURL repeats a request Gmail turned away for rate or a transient error, up to retries times with backoff.
func (c *Client) callURL(ctx context.Context, tok, method, u string, body, out any) error {
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	for attempt := 0; ; attempt++ {
		wait, err := c.once(ctx, tok, method, u, b, out, attempt)
		if wait == 0 {
			return err
		}
		if attempt == retries || Sleep(ctx, wait) != nil {
			if rateLimited(err.Error()) || strings.Contains(err.Error(), " 429 ") {
				return ErrRateLimited
			}
			return err
		}
	}
}

func (c *Client) once(ctx context.Context, tok, method, u string, body []byte, out any, attempt int) (time.Duration, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, _ := http.NewRequestWithContext(ctx, method, u, rd)
	req.Header.Set("Authorization", "Bearer "+tok)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		path := strings.TrimPrefix(strings.SplitN(u, "?", 2)[0], c.APIBase)
		err := fmt.Errorf("google %s %s: %s %s", method, path, resp.Status, strings.TrimSpace(string(msg)))
		if wait := retryAfter(resp, string(msg), attempt); wait > 0 {
			return wait, err
		}
		return 0, apiError(err)
	}
	if out == nil {
		return 0, nil
	}
	return 0, json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
}

// Fetch lists inbox messages since the day, newest first, at most limit, as the reply matcher wants them.
// Only metadata and Gmail's snippet are read; bodies stay in Gmail.
func (c *Client) Fetch(ctx context.Context, since time.Time, limit int) ([]mail.Mail, error) {
	if c.Connected() && !c.HasMail() {
		return nil, errors.New("reading Gmail was not allowed when you connected Google: connect again and tick every box Google offers")
	}
	tok, err := c.access(ctx)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 200
	}
	// not only the inbox: a reply the user archived, or a filter moved under a label, still counts
	query := "-in:sent -in:drafts -in:chats -category:promotions -category:social after:" + strconv.FormatInt(since.Unix(), 10)
	var ids []string
	page := ""
	for len(ids) < limit {
		q := url.Values{"q": {query}, "maxResults": {strconv.Itoa(min(limit-len(ids), 100))}}
		if page != "" {
			q.Set("pageToken", page)
		}
		var list struct {
			Messages []struct {
				ID string `json:"id"`
			} `json:"messages"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := c.call(ctx, tok, "GET", "/messages?"+q.Encode(), nil, &list); err != nil {
			return nil, err
		}
		for _, m := range list.Messages {
			ids = append(ids, m.ID)
		}
		if list.NextPageToken == "" {
			break
		}
		page = list.NextPageToken
	}
	// Messages read before come from the cache; new ones are paced under Gmail's per-user rate and
	// cached as they arrive, so a sync stopped by the quota resumes where it stopped.
	cache := c.loadCache()
	out := make([]mail.Mail, 0, len(ids))
	var err2 error
	fetched := 0
	for _, id := range ids {
		if e, ok := cache[id]; ok {
			out = append(out, e.mail())
			continue
		}
		if fetched > 0 {
			if err2 = Sleep(ctx, messageGap); err2 != nil {
				break
			}
		}
		fetched++
		m, err := c.message(ctx, tok, id)
		if err != nil {
			err2 = err
			break
		}
		cache[id] = toCache(m)
		out = append(out, m)
	}
	if fetched > 0 {
		if err := c.saveCache(cache); err != nil && err2 == nil {
			err2 = err
		}
	}
	return out, err2
}

func (c *Client) message(ctx context.Context, tok, id string) (mail.Mail, error) {
	q := url.Values{"format": {"metadata"}}
	for _, h := range []string{"From", "Subject", "Date", "Message-ID", "In-Reply-To", "References"} {
		q.Add("metadataHeaders", h)
	}
	var msg struct {
		ThreadID string `json:"threadId"`
		Snippet  string `json:"snippet"`
		Internal string `json:"internalDate"`
		Payload  struct {
			Headers []struct{ Name, Value string } `json:"headers"`
		} `json:"payload"`
	}
	if err := c.call(ctx, tok, "GET", "/messages/"+url.PathEscape(id)+"?"+q.Encode(), nil, &msg); err != nil {
		return mail.Mail{}, err
	}
	m := mail.Mail{Thread: msg.ThreadID, Text: msg.Snippet, MessageID: "gmail:" + id}
	var refs []string
	for _, h := range msg.Payload.Headers {
		switch strings.ToLower(h.Name) {
		case "from":
			m.From = h.Value
		case "subject":
			m.Subject = h.Value
		case "date":
			m.Date = h.Value
		case "message-id":
			m.MessageID = strings.TrimSpace(h.Value)
		case "in-reply-to", "references":
			refs = append(refs, h.Value)
		}
	}
	m.InReplyTo = strings.TrimSpace(strings.Join(refs, " "))
	m.From = mail.Address(m.From)
	if ms, err := strconv.ParseInt(msg.Internal, 10, 64); err == nil {
		m.Date = time.UnixMilli(ms).UTC().Format(time.RFC3339)
	}
	return m, nil
}

// Draft is a draft created in the user's Gmail.
type Draft struct {
	ID        string `json:"id"`
	MessageID string `json:"message_id"` // Gmail's message id, used for the compose link
	Thread    string `json:"thread"`
}

// CreateDraft uploads a complete RFC 822 message as a draft. Nothing is sent.
func (c *Client) CreateDraft(ctx context.Context, raw []byte, thread string) (Draft, error) {
	if c.Connected() && !c.granted(ComposeScope) && !c.oldGrant() {
		return Draft{}, errors.New("creating Gmail drafts was not allowed when you connected Google: connect again and tick every box Google offers")
	}
	tok, err := c.access(ctx)
	if err != nil {
		return Draft{}, err
	}
	msg := map[string]string{"raw": base64.URLEncoding.EncodeToString(raw)}
	if thread != "" {
		msg["threadId"] = thread
	}
	var out struct {
		ID      string `json:"id"`
		Message struct {
			ID       string `json:"id"`
			ThreadID string `json:"threadId"`
		} `json:"message"`
	}
	if err := c.call(ctx, tok, "POST", "/drafts", map[string]any{"message": msg}, &out); err != nil {
		return Draft{}, err
	}
	return Draft{ID: out.ID, MessageID: out.Message.ID, Thread: out.Message.ThreadID}, nil
}

// Disconnect revokes the grant at Google (best effort) and deletes the token file and the message cache.
func (c *Client) Disconnect(ctx context.Context) error {
	c.defaults()
	if t, err := c.load(); err == nil && t.RefreshToken != "" {
		req, _ := http.NewRequestWithContext(ctx, "POST", c.RevokeURL, strings.NewReader(url.Values{"token": {t.RefreshToken}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if resp, err := c.HTTP.Do(req); err == nil {
			resp.Body.Close()
		}
	}
	_ = os.Remove(c.cachePath())
	err := os.Remove(c.tokenPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// ThreadURL opens a thread in Gmail's web app; account is the address, so the right inbox opens.
func ThreadURL(account, thread string) string {
	return "https://mail.google.com/mail/?authuser=" + url.QueryEscape(account) + "#all/" + url.PathEscape(thread)
}

// DraftURL opens a draft in the account it was made in, whichever Google account the browser lists first.
func DraftURL(account, messageID string) string {
	return "https://mail.google.com/mail/?authuser=" + url.QueryEscape(account) + "#drafts?compose=" + url.QueryEscape(messageID)
}

// HasMail reports whether the grant lets ScoutJobs read inbox metadata.
func (c *Client) HasMail() bool { return c.granted(MailScope) || c.oldGrant() }

// oldGrant is a token saved before scopes were recorded: it was granted every scope asked then, mail included.
func (c *Client) oldGrant() bool {
	t, err := c.load()
	return err == nil && t.Scope == ""
}

// State is the Google connection as the dashboard and `gmail status` show it.
type State struct {
	Client        bool      `json:"client"` // an OAuth client is set up
	Connected     bool      `json:"connected"`
	Account       string    `json:"account"`
	Mail          bool      `json:"mail"`   // inbox metadata can be read
	Drafts        bool      `json:"drafts"` // drafts can be created
	Calendar      bool      `json:"calendar"`
	CalendarWrite bool      `json:"calendar_write"`
	Problem       string    `json:"problem,omitempty"`
	Testing       bool      `json:"testing"`       // the app is in Testing mode: Google ends the grant after 7 days
	Ends          time.Time `json:"ends,omitzero"` // when, for a Testing-mode app
}

// Status reads the saved token; it does not call Google.
func (c *Client) Status() State {
	st := State{Client: c.ID != ""}
	t, err := c.load()
	if err != nil {
		return st
	}
	st.Connected, st.Account, st.Problem = true, t.Email, t.Problem
	st.Mail, st.Drafts = c.HasMail(), c.granted(ComposeScope) || t.Scope == ""
	st.Calendar, st.CalendarWrite = c.HasCalendar(), c.CanWriteCalendar()
	st.Testing, st.Ends = !t.RefreshExpiry.IsZero(), t.RefreshExpiry
	return st
}
