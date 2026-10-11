// Package gmail talks to the Gmail API with the user's own OAuth desktop client: read inbox
// metadata for reply matching, and create drafts. It never sends, labels, archives or deletes.
// Stdlib only, like the rest of the binary.
package gmail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
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

// Scopes: read for reply matching, compose for drafts. Compose cannot send without the user pressing Send.
const Scopes = "https://www.googleapis.com/auth/gmail.readonly https://www.googleapis.com/auth/gmail.compose"

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
}

// Token is what gmail-token.json holds. The refresh token is the long-lived secret; the file is 0600.
type Token struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	Expiry       time.Time `json:"expiry"`
	Email        string    `json:"email"`
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

func (c *Client) save(t Token) error {
	b, _ := json.MarshalIndent(t, "", "  ")
	return os.WriteFile(c.tokenPath(), b, 0o600)
}

func randomString(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// Connect runs the installed-app flow: a one-shot listener on 127.0.0.1, the consent page in the
// browser, PKCE so an intercepted code is useless. show receives the URL to open.
func (c *Client) Connect(ctx context.Context, show func(authURL string)) (Token, error) {
	c.defaults()
	if c.ID == "" {
		return Token{}, errors.New("gmail_client_id missing in config.yaml: create a Desktop OAuth client in Google Cloud and paste its id")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return Token{}, err
	}
	defer ln.Close()
	redirect := "http://" + ln.Addr().String() + "/callback"
	state, verifier := randomString(16), randomString(48)
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{"client_id": {c.ID}, "redirect_uri": {redirect}, "response_type": {"code"}, "scope": {Scopes},
		"access_type": {"offline"}, "prompt": {"consent"}, "state": {state},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"}}
	show(c.AuthURL + "?" + q.Encode())

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
		v := r.URL.Query()
		switch {
		case v.Get("state") != state:
			http.Error(w, "state mismatch", http.StatusBadRequest)
			done <- result{err: errors.New("oauth state mismatch")}
		case v.Get("error") != "":
			fmt.Fprintln(w, "Gmail was not connected. You can close this tab.")
			done <- result{err: fmt.Errorf("google refused: %s", v.Get("error"))}
		default:
			fmt.Fprintln(w, "Gmail connected to ScoutJobs. You can close this tab.")
			done <- result{code: v.Get("code")}
		}
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
	t, err := c.grant(ctx, url.Values{"grant_type": {"authorization_code"}, "code": {res.code}, "redirect_uri": {redirect}, "code_verifier": {verifier}})
	if err != nil {
		return Token{}, err
	}
	var prof struct {
		EmailAddress string `json:"emailAddress"`
	}
	if err := c.call(ctx, t.AccessToken, "GET", "/profile", nil, &prof); err == nil {
		t.Email = prof.EmailAddress
	}
	return t, c.save(t)
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
		Error        string `json:"error"`
		Desc         string `json:"error_description"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return Token{}, fmt.Errorf("token endpoint: %s", resp.Status)
	}
	if out.Error != "" || out.AccessToken == "" {
		return Token{}, fmt.Errorf("token endpoint: %s %s", out.Error, out.Desc)
	}
	return Token{AccessToken: out.AccessToken, RefreshToken: out.RefreshToken, Expiry: time.Now().Add(time.Duration(out.ExpiresIn) * time.Second)}, nil
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
		return "", fmt.Errorf("gmail refresh: %w (run scoutjobs gmail connect again if access was revoked)", err)
	}
	t.AccessToken, t.Expiry = n.AccessToken, n.Expiry
	if n.RefreshToken != "" {
		t.RefreshToken = n.RefreshToken
	}
	return t.AccessToken, c.save(t)
}

func (c *Client) call(ctx context.Context, tok, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequestWithContext(ctx, method, c.APIBase+path, rd)
	req.Header.Set("Authorization", "Bearer "+tok)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("gmail %s %s: %s %s", method, strings.SplitN(path, "?", 2)[0], resp.Status, strings.TrimSpace(string(msg)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
}

// Fetch lists inbox messages since the day, newest first, at most limit, as the reply matcher wants them.
// Only metadata and Gmail's snippet are read; bodies stay in Gmail.
func (c *Client) Fetch(ctx context.Context, since time.Time, limit int) ([]mail.Mail, error) {
	tok, err := c.access(ctx)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 200
	}
	query := "in:inbox -category:promotions -category:social after:" + strconv.FormatInt(since.Unix(), 10)
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
	out := make([]mail.Mail, 0, len(ids))
	for _, id := range ids {
		m, err := c.message(ctx, tok, id)
		if err != nil {
			return out, err
		}
		out = append(out, m)
	}
	return out, nil
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

// Disconnect revokes the grant at Google (best effort) and deletes the token file.
func (c *Client) Disconnect(ctx context.Context) error {
	c.defaults()
	if t, err := c.load(); err == nil && t.RefreshToken != "" {
		req, _ := http.NewRequestWithContext(ctx, "POST", c.RevokeURL, strings.NewReader(url.Values{"token": {t.RefreshToken}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if resp, err := c.HTTP.Do(req); err == nil {
			resp.Body.Close()
		}
	}
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
