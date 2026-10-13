package gmail

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

// Kind names one way a Google connection fails; each has its own fix.
type Kind string

const (
	KindClient      Kind = "client"       // the OAuth client ID or secret is missing or refused
	KindAPIDisabled Kind = "api-disabled" // Gmail or Calendar API is off in the Cloud project
	KindScope       Kind = "scope"        // a box was left unticked on Google's consent screen
	KindTesting     Kind = "testing"      // a Testing-mode app's 7-day refresh token ran out
	KindRevoked     Kind = "revoked"      // access removed at Google, or the password changed
	KindDenied      Kind = "denied"       // stopped on Google's screen, often at the unverified-app warning
	KindRateLimit   Kind = "rate-limit"   // Gmail or Calendar quota for the account
	KindTransient   Kind = "transient"    // Google answered 5xx or the network failed
	KindOther       Kind = "other"
)

// Failure is a classified Google error: what went wrong, the step that fixes it,
// and whether trying again unchanged is safe and may work.
type Failure struct {
	Kind  Kind
	Fix   string
	Retry bool
	Err   error
}

func (f *Failure) Error() string {
	if f.Fix == "" {
		return f.Err.Error()
	}
	return f.Err.Error() + ". " + f.Fix
}

func (f *Failure) Unwrap() error { return f.Err }

// Console pages the fixes link to.
const (
	BrandingURL = "https://console.cloud.google.com/auth/branding"
	AudienceURL = "https://console.cloud.google.com/auth/audience"
	ClientsURL  = "https://console.cloud.google.com/auth/clients"
)

var serverError = regexp.MustCompile(`: 5\d\d `)

// Classify sorts an error from setup, connect, refresh or an API call into a Kind with its fix.
// Fixes name console pages and commands, never a token or secret.
func Classify(err error) *Failure {
	if err == nil {
		return nil
	}
	var f *Failure
	if errors.As(err, &f) {
		return f
	}
	var ce ConfigError
	m := err.Error()
	switch {
	case errors.As(err, &ce):
		return &Failure{Kind: KindClient, Err: err}
	case strings.Contains(m, "invalid_client") || strings.Contains(m, "client_secret is missing") || strings.Contains(m, "unauthorized_client"):
		return &Failure{Kind: KindClient, Err: err, Fix: "Google did not accept the OAuth client: download the Desktop client JSON again at " + ClientsURL + " and import it"}
	case strings.Contains(m, "Testing mode"):
		return &Failure{Kind: KindTesting, Err: err}
	case strings.Contains(m, "Google ended the connection"):
		return &Failure{Kind: KindRevoked, Err: err}
	case strings.Contains(m, "is not enabled in your Google Cloud project"):
		return &Failure{Kind: KindAPIDisabled, Err: err, Retry: true}
	case strings.Contains(m, "did not grant this permission"):
		return &Failure{Kind: KindScope, Err: err}
	case strings.Contains(m, "access_denied"):
		return &Failure{Kind: KindDenied, Err: err, Retry: true, Fix: "If Google said it has not verified the app, that is expected for your own client: " +
			"choose Advanced, then Go to ScoutJobs (unsafe), and tick every box"}
	case errors.Is(err, ErrRateLimited) || rateLimited(m):
		return &Failure{Kind: KindRateLimit, Err: err, Retry: true}
	case serverError.MatchString(m) || strings.Contains(m, "token endpoint: 5") || errors.Is(err, context.DeadlineExceeded) || isNetwork(m):
		return &Failure{Kind: KindTransient, Err: err, Retry: true, Fix: "Google had a temporary problem: nothing was changed, try again in a minute"}
	}
	return &Failure{Kind: KindOther, Err: err}
}

func isNetwork(m string) bool {
	for _, s := range []string{"connection refused", "no such host", "i/o timeout", "connection reset", "TLS handshake timeout"} {
		if strings.Contains(m, s) {
			return true
		}
	}
	return false
}

// Step is one item of a console checklist.
type Step struct{ Title, URL, Do string }

// Defaults for the consent screen of a personal ScoutJobs client.
const (
	HomePage   = "https://zoolcoder.github.io/scoutjobs"
	PrivacyURL = "https://github.com/zoolcoder/scoutjobs#security-and-privacy"
)

// PublishSteps move a Testing-mode app to In production. Production removes the 7-day expiry of the
// refresh token; Google still shows its unverified-app warning, which only verification removes.
var PublishSteps = []Step{
	{"Branding", BrandingURL, "User support email: your address. App home page: " + HomePage + ". Privacy policy: " + PrivacyURL +
		". Authorised domains: github.com and zoolcoder.github.io. Developer contact: your address. Save."},
	{"Audience", AudienceURL, "User type External. Press Publish app, then Confirm: Publishing status reads In production."},
	{"Reconnect", "", "Reconnect Google once, so the Testing token is replaced by one that does not expire."},
}

// Check is the result of one light request after connecting.
type Check struct {
	Name    string   `json:"name"` // account | mail | calendar
	OK      bool     `json:"ok"`
	Detail  string   `json:"detail"`
	Failure *Failure `json:"-"`
	Kind    Kind     `json:"kind,omitempty"`
	Retry   bool     `json:"retry,omitempty"`
}

// Verify proves the connection with light reads: the account, the Gmail profile and the primary
// calendar. Nothing is written, and each integration is reported on its own.
func (c *Client) Verify(ctx context.Context) []Check {
	c.defaults()
	tok, err := c.access(ctx)
	if err != nil {
		return []Check{failed("account", err)}
	}
	out := []Check{{Name: "account", OK: true, Detail: orNone(c.Account())}}
	switch {
	case !c.HasMail():
		out = append(out, failed("mail", &Failure{Kind: KindScope, Err: errors.New("reading mail was not allowed"), Fix: "Reconnect Google and tick every box"}))
	default:
		var prof struct {
			EmailAddress string `json:"emailAddress"`
		}
		if err := c.call(ctx, tok, "GET", "/profile", nil, &prof); err != nil {
			out = append(out, failed("mail", err))
		} else {
			out = append(out, Check{Name: "mail", OK: true, Detail: "Gmail answers for " + orNone(prof.EmailAddress)})
		}
	}
	switch {
	case !c.HasCalendar():
		out = append(out, failed("calendar", &Failure{Kind: KindScope, Err: errors.New("the calendar was not allowed"), Fix: "Reconnect Google and tick the Calendar box"}))
	default:
		var cal struct {
			ID string `json:"id"`
		}
		if err := c.callURL(ctx, tok, "GET", c.CalBase+"/calendars/primary", nil, &cal); err != nil {
			out = append(out, failed("calendar", err))
		} else {
			d := "Google Calendar answers"
			if !c.CanWriteCalendar() {
				d += ", read-only: reconnect and allow editing events to sync interviews"
			}
			out = append(out, Check{Name: "calendar", OK: true, Detail: d})
		}
	}
	return out
}

func failed(name string, err error) Check {
	f := Classify(err)
	return Check{Name: name, Detail: f.Error(), Failure: f, Kind: f.Kind, Retry: f.Retry}
}

func orNone(s string) string {
	if s == "" {
		return "an unknown account"
	}
	return s
}
