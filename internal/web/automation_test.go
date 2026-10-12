package web

import (
	"encoding/json"
	"fmt"
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

func TestPrepareSelectedJobs(t *testing.T) {
	s := testStore(t)
	g, _, _ := s.UpsertJob(store.Job{Company: "Globex", Title: "SRE"})
	applied := "applied"
	_ = s.UpdateJob(g.ID, store.JobPatch{Status: &applied})
	if b := get(mustHandler(t, s, Config{}), "/pipeline?view=list").Body.String(); strings.Contains(b, "Prepare selected") {
		t.Error("batch form without an assistant")
	}
	agent := filepath.Join(s.Home, "agent.sh")
	_ = os.WriteFile(agent, []byte("#!/bin/sh\ncat >/dev/null\n"), 0o700)
	m := runner.NewManager(&runner.Runner{Store: s, Exe: "scoutjobs", Custom: agent})
	h := mustHandler(t, s, Config{Runner: m})
	b := get(h, "/pipeline?view=list").Body.String()
	if !strings.Contains(b, `form="batch" name="job" value="1"`) || strings.Contains(b, fmt.Sprintf(`name="job" value="%d"`, g.ID)) {
		t.Error("only open jobs get a checkbox")
	}
	req := httptest.NewRequest("POST", "/pipeline/prepare", strings.NewReader("job=1"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-site prepare: %d", rec.Code)
	}
	rec = post(h, "/pipeline/prepare", url.Values{"job": {"1", fmt.Sprint(g.ID), "x"}, "back": {"/pipeline?view=list"}})
	if loc := rec.Header().Get("Location"); rec.Code != http.StatusSeeOther || !strings.Contains(loc, "1+job+queued") || !strings.Contains(loc, "view=list") {
		t.Fatalf("prepare: %d %s", rec.Code, loc)
	}
	waitIdle(t, m)
	if m.Latest(1) == nil || m.Latest(g.ID) != nil {
		t.Error("wrong jobs prepared")
	}
	if rec := get(h, "/pipeline/queue"); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"waiting":0`) {
		t.Errorf("queue: %d %s", rec.Code, rec.Body.String())
	}
	if rec := post(h, "/pipeline/prepare", url.Values{}); !strings.Contains(rec.Header().Get("Location"), "Tick+at+least") {
		t.Errorf("empty pick: %s", rec.Header().Get("Location"))
	}
}

func waitIdle(t *testing.T, m *runner.Manager) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if m.Busy() == 0 && m.Latest(1) != nil {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("queue never drained")
}

func TestKeywordsPanelAndFix(t *testing.T) {
	s := testStore(t)
	j, _ := s.JobByID(1)
	dir := apply.Dir(s, j)
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "resume.md"), []byte("# Jane\n\nGo services.\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, apply.ReviewName), []byte(`{"score":70,"requirements":[
		{"requirement":"Kafka","kind":"must","strength":"adjacent","evidence":"profile: queues","wording":"event streaming with Kafka"},
		{"requirement":"Rust","kind":"must","strength":"gap"}]}`), 0o644)
	agent := filepath.Join(s.Home, "agent.sh")
	_ = os.WriteFile(agent, []byte("#!/bin/sh\ncat > prompt.txt\n"), 0o700)
	m := runner.NewManager(&runner.Runner{Store: s, Exe: "scoutjobs", Custom: agent})
	h := mustHandler(t, s, Config{Runner: m})
	b := get(h, "/jobs/1").Body.String()
	for _, want := range []string{"Keywords to add", "event streaming with Kafka", "Rust", "never added for you", `action="/jobs/1/keywords"`} {
		if !strings.Contains(b, want) {
			t.Errorf("job page lacks %q", want)
		}
	}
	if rec := post(h, "/jobs/1/keywords", url.Values{"agent": {"custom"}}); rec.Code != http.StatusSeeOther || strings.Contains(rec.Header().Get("Location"), "runerr") {
		t.Fatalf("fix: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	m.Latest(1).Wait()
	prompt, _ := os.ReadFile(filepath.Join(dir, "prompt.txt"))
	if !strings.Contains(string(prompt), `"event streaming with Kafka"`) || strings.Contains(string(prompt), `"Rust"`) {
		t.Errorf("prompt:\n%.800s", prompt)
	}
}

func TestStoryBankPageAndJobMapping(t *testing.T) {
	s := testStore(t)
	h := mustHandler(t, s, Config{})
	if b := get(h, "/stories").Body.String(); !strings.Contains(b, "No stories yet") || !strings.Contains(b, `href="/stories"`) {
		t.Error("empty story bank")
	}
	_ = os.MkdirAll(filepath.Join(s.Home, "stories"), 0o755)
	_ = os.WriteFile(filepath.Join(s.Home, "stories", "kafka.md"), []byte("---\nid: kafka\ntitle: Moved billing to Kafka\nthemes: [kafka, migration]\nmetrics:\n  - lag 6h to 4m\n---\n\n## Core Facts\nEvent streaming in Go.\n"), 0o644)
	j, _ := s.JobByID(1)
	dir := apply.Dir(s, j)
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, apply.ReviewName), []byte(`{"score":70,"requirements":[
		{"requirement":"Kafka event streaming","kind":"must","strength":"direct","evidence":"x"},
		{"requirement":"Rust","kind":"must","strength":"gap"}]}`), 0o644)
	b := get(h, "/stories").Body.String()
	for _, want := range []string{"Moved billing to Kafka", "lag 6h to 4m", `href="/jobs/1?tab=interviews#stories">Acme`, "1 must-have requirement has no story"} {
		if !strings.Contains(b, want) {
			t.Errorf("stories page lacks %q", want)
		}
	}
	b = get(h, "/jobs/1?tab=interviews").Body.String()
	for _, want := range []string{"Stories for this job", "Tell “Moved billing to Kafka”", "Rust", "No story yet"} {
		if !strings.Contains(b, want) {
			t.Errorf("job page lacks %q", want)
		}
	}
}

func TestWhatWorksPanelAndAPI(t *testing.T) {
	s := testStore(t)
	h := mustHandler(t, s, Config{})
	if b := get(h, "/reviews").Body.String(); strings.Contains(b, "What works") {
		t.Error("What works without applications")
	}
	for i, st := range []string{"interview", "rejected", "applied"} {
		j, _, _ := s.UpsertJob(store.Job{Company: fmt.Sprintf("Co%d", i), Title: "Eng", Source: "lever"})
		applied := "applied"
		_ = s.UpdateJob(j.ID, store.JobPatch{Status: &applied})
		_ = s.UpdateJob(j.ID, store.JobPatch{Status: &st})
	}
	b := get(h, "/reviews").Body.String()
	for _, want := range []string{"What works", "Your best source is <b>lever</b>: 66%", "By month applied"} {
		if !strings.Contains(b, want) {
			t.Errorf("reviews lacks %q", want)
		}
	}
	if rec := get(h, "/api/funnel"); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"group":"lever","applied":3,"responded":2`) {
		t.Errorf("api: %d %s", rec.Code, rec.Body.String())
	}
}

func TestPaletteJSONAndShortcutHelp(t *testing.T) {
	s := testStore(t)
	h := mustHandler(t, s, Config{})
	rec := get(h, "/palette.json")
	var jobs []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &jobs); err != nil || rec.Code != 200 || len(jobs) != 1 || jobs[0]["company"] != "Acme" || jobs[0]["id"] != float64(1) {
		t.Fatalf("palette.json: %d %s", rec.Code, rec.Body.String())
	}
	if _, ok := jobs[0]["notes"]; ok {
		t.Error("palette.json leaks more than it needs")
	}
	b := get(h, "/").Body.String()
	for _, want := range []string{`data-palette`, `id="palette"`, `role="combobox"`, `id="keys"`, "Keyboard shortcuts"} {
		if !strings.Contains(b, want) {
			t.Errorf("layout lacks %q", want)
		}
	}
}
