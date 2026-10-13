package gmail

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		err   error
		kind  Kind
		retry bool
	}{
		{ConfigError{[]string{"secret"}}, KindClient, false},
		{errors.New("token endpoint: invalid_request client_secret is missing."), KindClient, false},
		{errors.New("token endpoint: invalid_client Unauthorized"), KindClient, false},
		{errors.New(expiredProblem(Token{RefreshExpiry: time.Now()})), KindTesting, false},
		{errors.New(expiredProblem(Token{})), KindRevoked, false},
		{apiError(errors.New("google GET /x: 403 Forbidden SERVICE_DISABLED")), KindAPIDisabled, true},
		{apiError(errors.New("google GET /x: 403 Forbidden ACCESS_TOKEN_SCOPE_INSUFFICIENT")), KindScope, false},
		{errors.New("google refused: access_denied"), KindDenied, true},
		{ErrRateLimited, KindRateLimit, true},
		{fmt.Errorf("sync: %w", ErrRateLimited), KindRateLimit, true},
		{errors.New("google GET /profile: 503 Service Unavailable {}"), KindTransient, true},
		{errors.New("token endpoint: 500 Internal Server Error"), KindTransient, true},
		{errors.New("dial tcp: lookup oauth2.googleapis.com: no such host"), KindTransient, true},
		{errors.New("something else"), KindOther, false},
	}
	for _, c := range cases {
		f := Classify(c.err)
		if f.Kind != c.kind || f.Retry != c.retry {
			t.Errorf("%v: got %s retry=%v, want %s retry=%v", c.err, f.Kind, f.Retry, c.kind, c.retry)
		}
		if !errors.Is(f, c.err) && !errors.As(f, new(ConfigError)) {
			t.Errorf("%v: cause lost", c.err)
		}
	}
	if Classify(nil) != nil {
		t.Error("nil classified")
	}
	if f := Classify(errors.New("token endpoint: invalid_client")); strings.Contains(f.Error(), "s3cr3t") || !strings.Contains(f.Error(), ClientsURL) {
		t.Errorf("client fix: %s", f)
	}
}

func TestVerifyReportsEachIntegration(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/profile":
			_, _ = w.Write([]byte(`{"emailAddress":"me@x.test"}`))
		case "/cal/calendars/primary":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"status":"PERMISSION_DENIED","details":[{"reason":"SERVICE_DISABLED"}]}}`))
		default: // the token endpoint too: a refused refresh
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := &Client{ID: "cid", Home: t.TempDir(), HTTP: srv.Client(), APIBase: srv.URL + "/api", CalBase: srv.URL + "/cal"}
	_ = c.save(Token{AccessToken: "at", Expiry: time.Now().Add(time.Hour), Email: "me@x.test", Scope: Scopes})
	got := c.Verify(context.Background())
	if len(got) != 3 || !got[0].OK || !got[1].OK || got[2].OK || got[2].Kind != KindAPIDisabled || !got[2].Retry {
		t.Fatalf("checks %+v", got)
	}

	_ = c.save(Token{AccessToken: "at", Expiry: time.Now().Add(time.Hour), Scope: MailScope})
	if got := c.Verify(context.Background()); got[2].OK || got[2].Kind != KindScope {
		t.Errorf("calendar not granted: %+v", got[2])
	}

	_ = c.save(Token{RefreshToken: "rt", RefreshExpiry: time.Now(), Expiry: time.Now().Add(-time.Hour)})
	c.TokenURL = srv.URL + "/token" // 404: refused, not invalid_grant
	if got := c.Verify(context.Background()); len(got) != 1 || got[0].OK || got[0].Name != "account" {
		t.Errorf("no access: %+v", got)
	}
}
