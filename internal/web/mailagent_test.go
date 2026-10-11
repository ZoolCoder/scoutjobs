package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/runner"
)

// fakeCodex is a codex CLI that has a signed-in Gmail server and does nothing when run.
func fakeCodex(t *testing.T) {
	bin := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1\" = mcp ]; then echo '[{\"name\":\"gmail\",\"enabled\":true,\"auth_status\":\"o_auth\"},{\"name\":\"context7\",\"enabled\":true}]'; exit 0; fi\n/bin/cat >/dev/null\n"
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	saved := runner.ExtraDirs
	runner.ExtraDirs = func() []string { return nil }
	t.Cleanup(func() { runner.ExtraDirs = saved })
}

func TestInboxAgentConnect(t *testing.T) {
	fakeCodex(t)
	s := testStore(t)
	m := runner.NewManager(&runner.Runner{Store: s, Exe: "scoutjobs"})
	status := func() (string, string) {
		if s.Setting(runner.MailVia) == "agent" {
			return "agent", s.Setting(runner.MailAgent)
		}
		return "", ""
	}
	h, _ := Handler(s, Config{Runner: m, Mail: MailHooks{Status: status}})
	post := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		req.Host = "127.0.0.1:7070"
		req.Header.Set("Origin", "http://127.0.0.1:7070")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	b := get(h, "/inbox").Body.String()
	if !strings.Contains(b, "Connect your email &amp; calendar") || !strings.Contains(b, `data-mail-check="/mail/agent/check?agent=codex"`) || !strings.Contains(b, "Use codex") {
		t.Fatalf("inbox does not offer the agent:\n%s", b)
	}
	if b := get(h, "/mail/agent/check?agent=codex").Body.String(); !strings.Contains(b, `"name":"gmail"`) || !strings.Contains(b, `"ready":true`) || strings.Contains(b, "context7") || strings.Contains(b, "hint") {
		t.Errorf("check: %s", b)
	}
	if rec := post("/mail/agent", "agent=claude"); !strings.Contains(rec.Header().Get("Location"), "not+installed") && !strings.Contains(rec.Header().Get("Location"), "not%20installed") {
		t.Errorf("missing agent accepted: %s", rec.Header().Get("Location"))
	}
	req := httptest.NewRequest("POST", "/mail/agent", strings.NewReader("agent=codex"))
	req.Header.Set("Origin", "https://evil.test")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-origin choice: %d", rec.Code)
	}
	post("/mail/agent", "agent=codex")
	if s.Setting(runner.MailVia) != "agent" || s.Setting(runner.MailAgent) != "codex" {
		t.Fatal("agent not chosen")
	}
	if b := get(h, "/inbox").Body.String(); !strings.Contains(b, "reads your mail") || strings.Contains(b, `id="connect"`) || !strings.Contains(b, "Sync now") {
		t.Error("inbox does not show the agent in use")
	}

	if rec := post("/inbox/sync", "back=/inbox"); rec.Header().Get("Location") != "/inbox#agent" {
		t.Fatalf("sync: %s", rec.Header().Get("Location"))
	}
	run := m.Latest(runner.MailID)
	if run == nil {
		t.Fatal("no mail run")
	}
	run.Wait()
	if st := run.Snapshot(); st.State != runner.Done {
		t.Errorf("run: %+v", st)
	}
	for i := 0; i < 50 && s.Setting(agentSynced) == ""; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if s.Setting(agentSynced) == "" {
		t.Error("finished sync not remembered")
	}
	if b := get(h, "/mail/agent/run").Body.String(); !strings.Contains(b, "read new replies with gmail") {
		t.Errorf("run log: %s", b)
	}

	post("/mail/agent/off", "")
	if s.Setting(runner.MailVia) != "" {
		t.Error("agent not stopped")
	}
}
