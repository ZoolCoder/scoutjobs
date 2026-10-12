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
	"github.com/zoolcoder/scoutjobs/internal/search"
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
	if !strings.Contains(b, "Connect your email &amp; calendar") || !strings.Contains(b, `href="/connections#mail"`) {
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

func TestConnections(t *testing.T) {
	fakeCodex(t)
	s := testStore(t)
	m := runner.NewManager(&runner.Runner{Store: s, Exe: "scoutjobs"})
	h, _ := Handler(s, Config{Runner: m})
	post := func(path, body string) string {
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		req.Host = "127.0.0.1:7070"
		req.Header.Set("Origin", "http://127.0.0.1:7070")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Header().Get("Location")
	}

	b := get(h, "/connections").Body.String()
	for _, want := range []string{"AI assistant", "Mail", "Calendar", "Job sources", `name="via" value="codex"`, "Remotive", `data-test="mail"`} {
		if !strings.Contains(b, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if loc := post("/connections/agent", "via=nope"); !strings.Contains(loc, "err=") || s.Setting(runner.DefaultAgent) != "" {
		t.Errorf("unknown agent accepted: %s", loc)
	}
	if loc := post("/connections/agent", "via=codex"); !strings.Contains(loc, "msg=") || s.Setting(runner.DefaultAgent) != "codex" {
		t.Errorf("agent not saved: %s", loc)
	}
	if post("/connections/calendar", "via=agent"); s.Setting(runner.CalendarVia) != "agent" {
		t.Error("calendar via agent not saved")
	}
	if b := get(h, "/connections").Body.String(); !strings.Contains(b, "Sync now") {
		t.Error("no Sync now once the agent syncs the calendar")
	}
	if !strings.Contains(get(h, "/connections").Body.String(), `<option value="14" selected>`) {
		t.Error("sync period does not default to 14 days")
	}
	if post("/connections/calendar/days", "days=7"); s.Setting(calendarDays) != "7" {
		t.Error("sync period not saved")
	}
	if loc := post("/connections/calendar/days", "days=365"); !strings.Contains(loc, "err=") || s.Setting(calendarDays) != "7" {
		t.Errorf("odd sync period accepted: %s", loc)
	}
	if loc := post("/connections/calendar/sync", ""); !strings.Contains(loc, "calendar+server") && !strings.Contains(loc, "calendar%20server") {
		t.Errorf("sync without a calendar connector: %s", loc)
	}
	if post("/connections/calendar", "via=off"); s.Setting(runner.CalendarVia) != "" {
		t.Error("calendar via agent not turned off")
	}
	if post("/connections/calendar", "via=apple"); !strings.Contains(get(h, "/connections").Body.String(), "/calendar.ics") {
		t.Error("no subscribe link once Apple Calendar is picked")
	}
	if loc := post("/connections/mail", "via=imap"); loc != "/inbox?connect=1#imap" {
		t.Errorf("imap not set up yet should open its setup: %s", loc)
	}
	if post("/connections/mail", "via=agent"); s.Setting(runner.MailVia) != "agent" || s.Setting(runner.MailAgent) != "codex" {
		t.Error("mail via the agent not saved")
	}
	if post("/connections/sources", "source=remoteok"); s.Setting(search.OffSetting) != "remoteok" {
		t.Errorf("remoteok not turned off: %q", s.Setting(search.OffSetting))
	}
	if post("/connections/sources", "source=remoteok&on=1"); s.Setting(search.OffSetting) != "" {
		t.Errorf("remoteok not turned back on: %q", s.Setting(search.OffSetting))
	}
	if b := get(h, "/connections/test?card=ai").Body.String(); !strings.Contains(b, `"ok":true`) || !strings.Contains(b, "gmail") {
		t.Errorf("ai test: %s", b)
	}
}
