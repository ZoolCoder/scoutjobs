package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/apply"
	"github.com/zoolcoder/scoutjobs/internal/autopilot"
	"github.com/zoolcoder/scoutjobs/internal/mail"
	"github.com/zoolcoder/scoutjobs/internal/runner"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

func TestDailySearchToggleAndAutomationPanel(t *testing.T) {
	s := testStore(t)
	h, _ := Handler(s, Config{FollowUpDays: 10})
	id, err := s.SaveSearch(store.SavedSearch{Name: "go remote", Queries: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if b := get(h, "/search").Body.String(); !strings.Contains(b, `aria-pressed="false"`) || !strings.Contains(b, ">Run daily<") {
		t.Fatal("saved search lacks the daily toggle")
	}
	if rec := post(h, "/search/saved/"+itoa(id)+"/daily", url.Values{"on": {"1"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("toggle: %d", rec.Code)
	}
	if b := get(h, "/search").Body.String(); !strings.Contains(b, `aria-pressed="true"`) {
		t.Error("daily not shown as on")
	}
	b := get(h, "/preferences").Body.String()
	for _, want := range []string{"Automation", "1 saved search runs every day", `aria-label="Draft follow-ups"`, "Needs an AI assistant", "scoutjobs daily"} {
		if !strings.Contains(b, want) {
			t.Errorf("preferences lacks %q", want)
		}
	}
	if rec := post(h, "/settings/automation", url.Values{"key": {"followups"}, "on": {"1"}}); rec.Code != http.StatusSeeOther || s.Setting(autopilot.FollowUpsSetting) != "on" {
		t.Errorf("switch on: %d %q", rec.Code, s.Setting(autopilot.FollowUpsSetting))
	}
	if rec := post(h, "/settings/automation", url.Values{"key": {"followups"}}); rec.Code != http.StatusSeeOther || s.Setting(autopilot.FollowUpsSetting) != "off" {
		t.Errorf("switch off: %q", s.Setting(autopilot.FollowUpsSetting))
	}
	if rec := post(h, "/settings/automation", url.Values{"key": {"api_token"}, "on": {"1"}}); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown key: %d", rec.Code)
	}
	req := httptest.NewRequest("POST", "/settings/automation", strings.NewReader("key=prep&on=1"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || s.Setting(autopilot.PrepSetting) == "on" {
		t.Errorf("cross-origin: %d", rec.Code)
	}
	if rec := post(h, "/settings/automation/run", nil); rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "Nothing+was+sent") {
		t.Errorf("run now: %d %s", rec.Code, rec.Header().Get("Location"))
	}
}

func TestTodayShowsDigestAndDraftedFollowUp(t *testing.T) {
	s := testStore(t)
	h, _ := Handler(s, Config{FollowUpDays: 10, StaleDays: 60})
	j, _, _ := s.UpsertJob(store.Job{Company: "Globex", Title: "Go Engineer", Contact: "hr@globex.test"})
	_ = s.AddEvent(j.ID, autopilot.DigestEvent, "go remote")
	applied := "applied"
	_ = s.UpdateJob(j.ID, store.JobPatch{Status: &applied})
	quiet := time.Now().UTC().AddDate(0, 0, -15).Format(time.RFC3339)
	if _, err := s.DB.Exec(`UPDATE jobs SET updated_at=? WHERE id=?`, quiet, j.ID); err != nil {
		t.Fatal(err)
	}
	b := get(h, "/").Body.String()
	if !strings.Contains(b, "1 new job from your daily searches") || !strings.Contains(b, "Follow up with Globex") {
		t.Fatalf("today before the draft:\n%s", between(b, "Today", "Upcoming"))
	}
	j, _ = s.JobByID(j.ID)
	if err := apply.SaveProposal(s, j, "followup", mail.Message{To: "hr@globex.test", Subject: "Following up", Body: "Hello"}); err != nil {
		t.Fatal(err)
	}
	if b = get(h, "/").Body.String(); !strings.Contains(b, "Review follow-up to Globex") || !strings.Contains(b, "?tab=mail#emails") {
		t.Errorf("drafted follow-up not offered for review:\n%s", between(b, "Today", "Upcoming"))
	}
	if _, err := s.LogMail(store.Mail{MessageID: "m1", JobID: j.ID, Dir: "out", Subject: "Following up", Date: time.Now().Add(time.Minute).UTC().Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
	if b = get(h, "/").Body.String(); strings.Contains(b, "Review follow-up to Globex") {
		t.Error("sent follow-up still waits for review")
	}
}

func between(s, from, to string) string {
	if i := strings.Index(s, from); i >= 0 {
		s = s[i:]
	}
	if i := strings.Index(s, to); i >= 0 {
		s = s[:i]
	}
	return s
}

func TestBuildPrepPack(t *testing.T) {
	s := testStore(t)
	if b := get(mustHandler(t, s, Config{}), "/interviews").Body.String(); strings.Contains(b, "Build prep pack") || !strings.Contains(b, "/scoutjobs:interview 1") {
		t.Error("prep pack button without an assistant")
	}
	agent := filepath.Join(s.Home, "agent.sh")
	_ = os.WriteFile(agent, []byte("#!/bin/sh\ncat >/dev/null\nprintf '# Prep\\n' > interview-prep.md\n"), 0o700)
	m := runner.NewManager(&runner.Runner{Store: s, Exe: "scoutjobs", Custom: agent})
	h := mustHandler(t, s, Config{Runner: m})
	if b := get(h, "/interviews").Body.String(); !strings.Contains(b, `action="/jobs/1/run"`) || !strings.Contains(b, `name="redo" value="prep"`) {
		t.Fatal("interviews page lacks the prep pack button")
	}
	if rec := post(h, "/jobs/1/run", url.Values{"redo": {"prep"}, "agent": {"custom"}}); rec.Code != http.StatusSeeOther || strings.Contains(rec.Header().Get("Location"), "runerr") {
		t.Fatalf("start: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	m.Latest(1).Wait()
	ivs, _ := s.Interviews(1, false)
	if len(ivs) != 1 || ivs[0].Prep != "company,posting,questions" {
		t.Errorf("prep not ticked: %+v", ivs)
	}
}

func mustHandler(t *testing.T, s *store.Store, cfg Config) http.Handler {
	t.Helper()
	h, err := Handler(s, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return h
}
