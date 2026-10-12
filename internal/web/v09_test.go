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

func TestCompanyFileAndQuestionBank(t *testing.T) {
	s := testStore(t)
	h := mustHandler(t, s, Config{})
	for _, p := range []string{"/jobs/1/company/paste", "/jobs/1/questions", "/jobs/1/questions/1", "/jobs/1/mock", "/jobs/1/mock/1", "/interviews/1/debrief"} {
		if crossSite(h, p) != http.StatusForbidden {
			t.Errorf("cross-site %s", p)
		}
	}
	b := get(h, "/jobs/1?tab=interviews").Body.String()
	for _, want := range []string{`id="company"`, "never logs in to or scrapes Glassdoor", `id="questions"`, "No questions yet"} {
		if !strings.Contains(b, want) {
			t.Errorf("interviews tab lacks %q", want)
		}
	}
	if strings.Contains(get(h, "/jobs/1").Body.String(), `id="questions"`) {
		t.Error("question bank on the overview")
	}
	loc := post(h, "/jobs/1/company/paste", url.Values{"source": {"Glassdoor"}, "text": {"Nice people.\n- How do you handle on-call?\n- Why Acme?"}}).Header().Get("Location")
	if !strings.Contains(loc, "msg=") || !strings.Contains(loc, "#company") {
		t.Fatalf("paste: %s", loc)
	}
	if loc := post(h, "/jobs/1/company/paste", url.Values{"text": {" "}}).Header().Get("Location"); !strings.Contains(loc, "err=") {
		t.Errorf("empty paste: %s", loc)
	}
	_ = post(h, "/jobs/1/questions", url.Values{"text": {"Walk me through a Postgres outage?"}, "kind": {"technical"}})
	j, _ := s.JobByID(1)
	qs, _ := s.Questions(j.Company)
	if len(qs) != 2 {
		t.Fatalf("bank: %+v", qs)
	}
	if b := get(h, "/jobs/1/company/notes.md").Body.String(); !strings.Contains(b, "Nice people.") {
		t.Error("company notes page")
	}
	if rec := get(h, "/jobs/1/company/..%2Fscoutjobs.db"); rec.Code != http.StatusNotFound {
		t.Errorf("company file outside the list: %d", rec.Code)
	}
	other, _, _ := s.UpsertJob(store.Job{Company: "Hooli", Title: "Eng"})
	if rec := post(h, fmt.Sprintf("/jobs/%d/questions/%d", other.ID, qs[0].ID), url.Values{"rating": {"5"}}); rec.Code != http.StatusNotFound {
		t.Errorf("rated another company's question: %d", rec.Code)
	}
	for _, q := range qs {
		_ = post(h, fmt.Sprintf("/jobs/1/questions/%d", q.ID), url.Values{"rehearsed": {"1"}, "rating": {"4"}})
	}
	ivs, _ := s.Interviews(1, false)
	if ivs[0].PrepDone("stories") {
		t.Error("stories ticked with two rehearsed answers")
	}
	_, _ = s.AddQuestion(j, store.Question{Text: "Why now?"})
	qs, _ = s.Questions(j.Company)
	_ = post(h, fmt.Sprintf("/jobs/1/questions/%d", qs[0].ID), url.Values{"rehearsed": {"1"}})
	if ivs, _ = s.Interviews(1, false); !ivs[0].PrepDone("stories") {
		t.Error("three rehearsed answers do not tick the stories item")
	}
	if b := get(h, "/jobs/1?tab=interviews").Body.String(); !strings.Contains(b, "rehearsed 3/3") {
		t.Error("rehearsed count")
	}
	_ = post(h, fmt.Sprintf("/jobs/1/questions/%d", qs[0].ID), url.Values{"delete": {"1"}})
	if qs, _ = s.Questions(j.Company); len(qs) != 2 {
		t.Errorf("delete: %d left", len(qs))
	}
}

func TestDebrief(t *testing.T) {
	s := testStore(t)
	h := mustHandler(t, s, Config{})
	iv, _ := s.AddInterview(store.Interview{JobID: 1, Stage: "screen", At: "2001-01-01T10:00"})
	b := get(h, "/jobs/1?tab=interviews").Body.String()
	if !strings.Contains(b, fmt.Sprintf(`action="/interviews/%d/debrief"`, iv)) || strings.Count(b, "/debrief\"") != 1 {
		t.Fatal("debrief form only on the past interview")
	}
	rec := post(h, fmt.Sprintf("/interviews/%d/debrief", iv), url.Values{"asked": {"- Why Acme?\r\nDescribe your last project?\n"}, "went_well": {"Rapport"}, "weak": {"System design depth"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("debrief: %d", rec.Code)
	}
	ivs, _ := s.Interviews(1, false)
	var got store.Interview
	for _, i := range ivs {
		if i.ID == iv {
			got = i
		}
	}
	if got.WentWell != "Rapport" || got.Weak != "System design depth" || !strings.Contains(got.Asked, "Why Acme?") {
		t.Errorf("debrief not saved: %+v", got)
	}
	j, _ := s.JobByID(1)
	if qs, _ := s.Questions(j.Company); len(qs) != 2 || qs[0].Source != "debrief" {
		t.Errorf("asked questions not in the bank: %+v", qs)
	}
	if notes := apply.CompanyFile(s, j.Company, apply.CompanyNotes); !strings.Contains(notes, "Debrief, screen interview") || !strings.Contains(notes, "System design depth") {
		t.Errorf("company note: %s", notes)
	}
	events, _ := s.Events(1, 20)
	if !hasNote(events, "debrief", "2 new questions") {
		t.Errorf("debrief event: %+v", events)
	}
	if rec := post(h, "/interviews/9999/debrief", url.Values{"asked": {"x"}}); rec.Code != http.StatusNotFound {
		t.Errorf("unknown interview: %d", rec.Code)
	}
}

func TestMockPage(t *testing.T) {
	s := testStore(t)
	h := mustHandler(t, s, Config{})
	if loc := post(h, "/jobs/1/mock", nil).Header().Get("Location"); !strings.Contains(loc, "err=") {
		t.Errorf("mock with an empty bank: %s", loc)
	}
	j, _ := s.JobByID(1)
	_, _ = s.AddQuestion(j, store.Question{Text: "Why Acme?"})
	if loc := post(h, "/jobs/1/mock", nil).Header().Get("Location"); loc != "/jobs/1/mock/1" {
		t.Fatalf("start: %s", loc)
	}
	qs, _ := s.Questions(j.Company)
	b := get(h, "/jobs/1/mock/1").Body.String()
	if !strings.Contains(b, "Why Acme?") || !strings.Contains(b, fmt.Sprintf(`name="a%d"`, qs[0].ID)) || strings.Contains(b, "score with AI") {
		t.Error("mock page")
	}
	loc := post(h, "/jobs/1/mock/1", url.Values{fmt.Sprintf("a%d", qs[0].ID): {"Because anvils."}, "score": {"1"}}).Header().Get("Location")
	if !strings.Contains(loc, "msg=No+AI+assistant") {
		t.Errorf("score without a runner: %s", loc)
	}
	if b := get(h, "/jobs/1/mock/1").Body.String(); !strings.Contains(b, "Because anvils.") {
		t.Error("answer not saved")
	}
	if b := get(h, "/jobs/1?tab=interviews").Body.String(); !strings.Contains(b, `href="/jobs/1/mock/1"`) {
		t.Error("interviews tab does not link the last mock")
	}
	if rec := get(h, "/jobs/1/mock/7"); rec.Code != http.StatusNotFound {
		t.Errorf("missing mock: %d", rec.Code)
	}
}

func TestOfferPanel(t *testing.T) {
	s := testStore(t)
	h := mustHandler(t, s, Config{})
	if strings.Contains(get(h, "/jobs/1").Body.String(), `id="offer"`) {
		t.Error("offer panel before an offer")
	}
	st := "offer"
	_ = s.UpdateJob(1, store.JobPatch{Status: &st})
	j, _ := s.JobByID(1)
	dir := apply.Dir(s, j)
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, apply.OfferName), []byte("# Offer\n"), 0o600)
	b := get(h, "/jobs/1").Body.String()
	if !strings.Contains(b, `id="offer"`) || !strings.Contains(b, `href="/jobs/1/files/offer-prep.md"`) {
		t.Error("offer panel")
	}
}
