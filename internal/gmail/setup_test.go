package gmail

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseCredentials(t *testing.T) {
	c, err := ParseCredentials([]byte(`{"installed":{"client_id":"1-a.apps.googleusercontent.com","client_secret":"s","redirect_uris":["http://localhost"]}}`))
	if err != nil || c.ID != "1-a.apps.googleusercontent.com" || c.Secret != "s" {
		t.Fatalf("installed: %+v %v", c, err)
	}
	if c, err := ParseCredentials([]byte(`{"client_id":"2-b.apps.googleusercontent.com"}`)); err != nil || c.ID == "" {
		t.Errorf("flat: %+v %v", c, err)
	}
	if _, err := ParseCredentials([]byte(`{"web":{"client_id":"3-c.apps.googleusercontent.com"}}`)); err == nil || !strings.Contains(err.Error(), "Desktop app") {
		t.Errorf("web client accepted: %v", err)
	}
	if _, err := ParseCredentials([]byte(`not json`)); err == nil {
		t.Error("garbage accepted")
	}
}

func TestSaveLoadCredentials(t *testing.T) {
	home := t.TempDir()
	if err := SaveCredentials(home, Credentials{ID: "nope"}); err == nil {
		t.Error("saved a client id that is not Google's")
	}
	want := Credentials{ID: "1-a.apps.googleusercontent.com", Secret: "s"}
	if err := SaveCredentials(home, want); err != nil {
		t.Fatal(err)
	}
	if got := LoadCredentials(home); got != want {
		t.Errorf("got %+v", got)
	}
	if st, _ := os.Stat(filepath.Join(home, ClientFile)); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode())
	}
}

func TestFindDownload(t *testing.T) {
	dir := t.TempDir()
	if FindDownload(dir, time.Time{}) != "" {
		t.Error("found a file in an empty dir")
	}
	old, newer := filepath.Join(dir, "client_secret_old.json"), filepath.Join(dir, "client_secret_new.json")
	for i, p := range []string{old, newer} {
		_ = os.WriteFile(p, []byte("{}"), 0o600)
		ts := time.Now().Add(time.Duration(i-2) * time.Hour)
		_ = os.Chtimes(p, ts, ts)
	}
	_ = os.WriteFile(filepath.Join(dir, "other.json"), []byte("{}"), 0o600)
	if got := FindDownload(dir, time.Time{}); got != newer {
		t.Errorf("got %q", got)
	}
}

func TestBeginAndCode(t *testing.T) {
	c := &Client{ID: "cid", AuthURL: "https://auth.test/o"}
	u, p, err := c.Begin("http://127.0.0.1:7070/gmail/callback")
	if err != nil {
		t.Fatal(err)
	}
	q, _ := url.Parse(u)
	if v := q.Query(); v.Get("state") != p.State || v.Get("redirect_uri") != p.Redirect || v.Get("code_challenge_method") != "S256" {
		t.Errorf("auth url %s", u)
	}
	if _, err := p.Code(url.Values{"state": {"x"}, "code": {"c"}}); err == nil {
		t.Error("accepted a foreign state")
	}
	if _, err := p.Code(url.Values{"state": {p.State}, "error": {"access_denied"}}); err == nil {
		t.Error("accepted a refusal")
	}
	if code, err := p.Code(url.Values{"state": {p.State}, "code": {"c1"}}); err != nil || code != "c1" {
		t.Errorf("code %q %v", code, err)
	}
	if _, _, err := (&Client{}).Begin("http://127.0.0.1/cb"); err == nil {
		t.Error("began without a client id")
	}
}

func TestLoopbackHost(t *testing.T) {
	for _, h := range []string{"127.0.0.1:7070", "localhost:7070", "[::1]:7070"} {
		if err := LoopbackHost(h); err != nil {
			t.Errorf("%s: %v", h, err)
		}
	}
	for _, h := range []string{"192.168.1.5:7070", "scout.example.com"} {
		if LoopbackHost(h) == nil {
			t.Errorf("%s accepted", h)
		}
	}
}
