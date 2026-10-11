package gmail

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ClientFile holds the OAuth client imported from Google's download, so nobody pastes ids into config.yaml.
const ClientFile = "gmail-client.json"

// Credentials is the Desktop OAuth client. Google treats a Desktop client's secret as non-confidential,
// but it still stays in a 0600 file and out of logs.
type Credentials struct {
	ID     string `json:"client_id"`
	Secret string `json:"client_secret"`
}

// ParseCredentials reads the JSON Google Cloud offers under Download JSON for an OAuth client.
func ParseCredentials(b []byte) (Credentials, error) {
	var f struct {
		Installed *Credentials `json:"installed"`
		Web       *Credentials `json:"web"`
		Credentials
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return Credentials{}, errors.New("not a Google OAuth client file (expected the JSON from Download JSON)")
	}
	switch {
	case f.Installed != nil && f.Installed.ID != "":
		return *f.Installed, nil
	case f.Web != nil:
		return Credentials{}, errors.New("this is a Web application client; create an OAuth client of type Desktop app")
	case f.ID != "":
		return f.Credentials, nil
	}
	return Credentials{}, errors.New("no Desktop OAuth client in this file")
}

// SaveCredentials stores the client in the data dir.
func SaveCredentials(home string, c Credentials) error {
	if !strings.HasSuffix(c.ID, ".apps.googleusercontent.com") {
		return errors.New("client id does not look like xxxx.apps.googleusercontent.com")
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(filepath.Join(home, ClientFile), b, 0o600)
}

// LoadCredentials returns the imported client; the zero value when there is none.
func LoadCredentials(home string) Credentials {
	var c Credentials
	if b, err := os.ReadFile(filepath.Join(home, ClientFile)); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	return c
}

// FindDownload returns the newest client_secret_*.json in dir changed after since, "" when none.
func FindDownload(dir string, since time.Time) string {
	matches, _ := filepath.Glob(filepath.Join(dir, "client_secret_*.json"))
	best, bestTime := "", since
	for _, m := range matches {
		if st, err := os.Stat(m); err == nil && st.ModTime().After(bestTime) {
			best, bestTime = m, st.ModTime()
		}
	}
	return best
}

// ConfigError is a Google OAuth client that cannot finish a sign-in. It names what is missing,
// never the values.
type ConfigError struct{ Missing []string }

func (e ConfigError) Error() string {
	return "the Google OAuth client " + strings.Join(e.Missing, " and ") + " is missing. Import the Desktop client JSON again " +
		"(Inbox page, or scoutjobs gmail setup --file <client_secret_….json>), or set both gmail_client_id and " +
		"gmail_client_secret in config.yaml ($SCOUTJOBS_GMAIL_CLIENT_SECRET also works), then connect again"
}

// CheckConfig refuses a client without an ID or secret: Google rejects the code exchange of a Desktop
// client without its secret, after the user has already been through the consent screen.
func (c *Client) CheckConfig() error {
	var missing []string
	if c.ID == "" {
		missing = append(missing, "ID")
	}
	if c.Secret == "" {
		missing = append(missing, "secret")
	}
	if len(missing) > 0 {
		return ConfigError{missing}
	}
	return nil
}

// Pending is an authorization in flight: what the callback needs to finish it. Feature is what the
// user was connecting ("gmail" or "calendar"), so a failure is reported where it started.
type Pending struct {
	State, Verifier, Redirect, Feature string
}

// Begin returns Google's consent URL for a redirect on loopback. PKCE makes an intercepted code useless.
func (c *Client) Begin(redirect string) (string, Pending, error) {
	c.defaults()
	if c.ID == "" && c.Secret == "" {
		return "", Pending{}, errors.New("no Google OAuth client yet: run scoutjobs gmail setup")
	}
	if err := c.CheckConfig(); err != nil {
		return "", Pending{}, err
	}
	p := Pending{State: randomString(16), Verifier: randomString(48), Redirect: redirect}
	sum := sha256.Sum256([]byte(p.Verifier))
	q := url.Values{"client_id": {c.ID}, "redirect_uri": {redirect}, "response_type": {"code"}, "scope": {Scopes},
		"access_type": {"offline"}, "prompt": {"consent"}, "state": {p.State},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"}}
	return c.AuthURL + "?" + q.Encode(), p, nil
}

// Code checks the callback's query against the pending authorization and returns the code.
func (p Pending) Code(q url.Values) (string, error) {
	switch {
	case q.Get("state") != p.State:
		return "", errors.New("oauth state mismatch")
	case q.Get("error") != "":
		return "", fmt.Errorf("google refused: %s", q.Get("error"))
	case q.Get("code") == "":
		return "", errors.New("google sent no code")
	}
	return q.Get("code"), nil
}

// Finish trades the code from the callback for tokens, looks up the address and saves the token.
func (c *Client) Finish(ctx context.Context, p Pending, code string) (Token, error) {
	c.defaults()
	if err := c.CheckConfig(); err != nil {
		return Token{}, err
	}
	t, err := c.grant(ctx, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {p.Redirect}, "code_verifier": {p.Verifier}})
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

// LoopbackHost reports whether a host:port is one Google accepts in a Desktop client's redirect.
func LoopbackHost(hostport string) error {
	h, _, err := net.SplitHostPort(hostport)
	if err != nil {
		h = hostport
	}
	if ip := net.ParseIP(h); h == "localhost" || (ip != nil && ip.IsLoopback()) {
		return nil
	}
	return fmt.Errorf("a Desktop OAuth client only redirects back to this machine: open the dashboard on http://127.0.0.1:<port> to connect Gmail (now %s)", hostport)
}
