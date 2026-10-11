package gmail

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeGoogle serves the token endpoint and the few Gmail API calls the client makes.
func fakeGoogle(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.URL.Path == "/token" {
			_ = r.ParseForm()
			if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "rt-1" || r.Form.Get("client_id") != "cid" {
				w.WriteHeader(400)
				_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"at-2","expires_in":3600}`))
			return
		}
		if r.Header.Get("Authorization") != "Bearer at-2" {
			w.WriteHeader(401)
			return
		}
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/messages":
			if !strings.Contains(r.URL.Query().Get("q"), "in:inbox") {
				t.Errorf("query %q", r.URL.Query().Get("q"))
			}
			_, _ = w.Write([]byte(`{"messages":[{"id":"m1"}]}`))
		case r.Method == "GET" && r.URL.Path == "/api/messages/m1":
			_, _ = w.Write([]byte(`{"threadId":"t1","snippet":"We would like to invite you","internalDate":"1791700000000",
				"payload":{"headers":[{"name":"From","value":"Acme HR <hr@acme.test>"},{"name":"Subject","value":"Interview"},
				{"name":"Message-ID","value":"<m1@acme.test>"},{"name":"In-Reply-To","value":"<out@me>"}]}}`))
		case r.Method == "POST" && r.URL.Path == "/api/drafts":
			var body struct {
				Message struct{ Raw, ThreadID string } `json:"message"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			raw, _ := base64.URLEncoding.DecodeString(body.Message.Raw)
			if !strings.Contains(string(raw), "Subject: Re: Interview") {
				t.Errorf("draft raw %q", raw)
			}
			_, _ = w.Write([]byte(`{"id":"d1","message":{"id":"dm1","threadId":"` + body.Message.ThreadID + `"}}`))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func testClient(t *testing.T) (*Client, *[]string) {
	srv, calls := fakeGoogle(t)
	c := &Client{ID: "cid", Home: t.TempDir(), HTTP: srv.Client(), TokenURL: srv.URL + "/token", APIBase: srv.URL + "/api", RevokeURL: srv.URL + "/revoke"}
	expired := Token{AccessToken: "at-1", RefreshToken: "rt-1", Expiry: time.Now().Add(-time.Hour), Email: "me@x.test"}
	if err := c.save(expired); err != nil {
		t.Fatal(err)
	}
	return c, calls
}

func TestFetchRefreshesAndReadsMetadata(t *testing.T) {
	c, calls := testClient(t)
	ms, err := c.Fetch(context.Background(), time.Now().AddDate(0, 0, -7), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 1 {
		t.Fatalf("got %d mails", len(ms))
	}
	m := ms[0]
	if m.From != "hr@acme.test" || m.Subject != "Interview" || m.Thread != "t1" || m.MessageID != "<m1@acme.test>" || m.InReplyTo != "<out@me>" || !strings.HasPrefix(m.Date, "2026-") {
		t.Errorf("mail %+v", m)
	}
	if (*calls)[0] != "POST /token" {
		t.Errorf("expired token should refresh first: %v", *calls)
	}
	t2, err := c.load()
	if err != nil || t2.AccessToken != "at-2" || t2.RefreshToken != "rt-1" || time.Until(t2.Expiry) < 50*time.Minute {
		t.Errorf("refreshed token not saved: %+v %v", t2, err)
	}
	if fi, _ := os.Stat(filepath.Join(c.Home, TokenFile)); fi.Mode().Perm() != 0o600 {
		t.Errorf("token file mode %v", fi.Mode().Perm())
	}
	// a fresh token is reused, no second refresh
	n := len(*calls)
	if _, err := c.Fetch(context.Background(), time.Now(), 10); err != nil {
		t.Fatal(err)
	}
	for _, call := range (*calls)[n:] {
		if call == "POST /token" {
			t.Error("valid token refreshed again")
		}
	}
}

func TestCreateDraftNeverSends(t *testing.T) {
	c, calls := testClient(t)
	d, err := c.CreateDraft(context.Background(), []byte("To: hr@acme.test\r\nSubject: Re: Interview\r\n\r\nThanks"), "t1")
	if err != nil {
		t.Fatal(err)
	}
	if d.ID != "d1" || d.MessageID != "dm1" || d.Thread != "t1" {
		t.Errorf("draft %+v", d)
	}
	for _, call := range *calls {
		if strings.Contains(call, "send") {
			t.Errorf("client called %s", call)
		}
	}
}

func TestNotConnected(t *testing.T) {
	c := &Client{ID: "cid", Home: t.TempDir()}
	if c.Connected() {
		t.Error("no token file, yet connected")
	}
	if _, err := c.Fetch(context.Background(), time.Now(), 1); err == nil || !strings.Contains(err.Error(), "gmail connect") {
		t.Errorf("want connect hint, got %v", err)
	}
	if err := c.Disconnect(context.Background()); err != nil {
		t.Errorf("disconnect without token: %v", err)
	}
}
