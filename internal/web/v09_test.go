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

	"github.com/zoolcoder/scoutjobs/internal/apply"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

func apiDo(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// unbackedDrafts writes drafts whose figure the profile does not back, so the claims check fails.
func unbackedDrafts(t *testing.T, s *store.Store, j store.Job) {
	t.Helper()
	dir := apply.Dir(s, j)
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(s.Home, "profile.md"), []byte("- Roles: Go engineer\nShipped Go services.\n"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "resume.md"), []byte("# Ada\n\nCut costs by 70% across the fleet."), 0o600)
}

func TestMarkAppliedRunsChecks(t *testing.T) {
	s := testStore(t)
	h, _ := Handler(s, Config{APIToken: "secret"})
	manual, _, _ := s.UpsertJob(store.Job{Company: "Hooli", Title: "Eng"})
	if loc := post(h, fmt.Sprintf("/jobs/%d/status", manual.ID), url.Values{"status": {"applied"}}).Header().Get("Location"); strings.Contains(loc, "#gates") {
		t.Fatalf("a job without drafts is never held: %s", loc)
	}
	if ev, _ := s.Events(manual.ID, 10); !hasNote(ev, "quality", "no drafts") {
		t.Error("no-drafts application not recorded")
	}

	j, _ := s.JobByID(1)
	unbackedDrafts(t, s, j)
	loc := post(h, "/jobs/1/status", url.Values{"status": {"applied"}}).Header().Get("Location")
	if !strings.HasPrefix(loc, "/jobs/1?err=") || !strings.HasSuffix(loc, "#gates") {
		t.Fatalf("failing checks: %s", loc)
	}
	if got, _ := s.JobByID(1); got.Status == "applied" {
		t.Fatal("applied despite a failing check")
	}
	page := get(h, "/jobs/1?override=1").Body.String()
	for _, want := range []string{"Every number traces to your profile, CV or stories", `<details class="gdet" id="override" open>`, `name="override"`, `class="score fail"`} {
		if !strings.Contains(page, want) {
			t.Errorf("job page lacks %s", want)
		}
	}
	if rec := apiDo(h, "POST", "/api/jobs/1/status", `{"status":"applied"}`); rec.Code != http.StatusConflict {
		t.Errorf("API with failing checks: %d", rec.Code)
	}
	if rec := apiDo(h, "POST", "/api/jobs/1/status", `{"status":"applied","override":"figure from a side project"}`); rec.Code != 200 {
		t.Fatalf("API override: %d %s", rec.Code, rec.Body)
	}
	ev, _ := s.Events(1, 20)
	if !hasNote(ev, "check:override", "figure from a side project") || !hasNote(ev, "quality", "override score") {
		t.Errorf("override not logged: %+v", ev)
	}
}

func hasNote(events []store.Event, kind, part string) bool {
	for _, e := range events {
		if e.Kind == kind && strings.Contains(e.Note, part) {
			return true
		}
	}
	return false
}

func TestForecast(t *testing.T) {
	s := testStore(t)
	h, _ := Handler(s, Config{APIToken: "secret"})
	if b := get(h, "/jobs/1").Body.String(); !strings.Contains(b, "Not enough data yet: forecasts start after 15 applications") {
		t.Error("job page forecast before enough data")
	}
	for i := range 20 {
		j, _, _ := s.UpsertJob(store.Job{Company: fmt.Sprintf("Co%d", i), Title: "Eng", Source: "lever"})
		for _, st := range []string{"applied", map[bool]string{true: "interview", false: "applied"}[i < 5]} {
			_ = s.UpdateJob(j.ID, store.JobPatch{Status: &st})
		}
	}
	if b := get(h, "/jobs/1").Body.String(); !strings.Contains(b, "chance of an answer") {
		t.Error("job page lacks the forecast")
	}
	if b := get(h, "/reviews").Body.String(); !strings.Contains(b, "Next 30 days") || !strings.Contains(b, "applications are still waiting") {
		t.Error("reviews lacks the outlook")
	}
	var out struct {
		Outlook  store.Outlook           `json:"outlook"`
		BySource map[string]store.Chance `json:"by_source"`
	}
	rec := apiDo(h, "GET", "/api/forecast", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || !out.Outlook.Enough || out.Outlook.Waiting != 15 || !out.BySource["lever"].Enough {
		t.Errorf("api forecast: %d %+v %v", rec.Code, out, err)
	}
}
