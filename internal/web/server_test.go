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

	"github.com/zoolcoder/scoutjobs/internal/store"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	t.Setenv("SCOUTJOBS_HOME", t.TempDir())
	s, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	j, _, err := s.UpsertJob(store.Job{Company: "Acme", Title: "Go Engineer", Remote: "remote"})
	if err != nil {
		t.Fatal(err)
	}
	score := int64(80)
	if err := s.UpdateJob(j.ID, store.JobPatch{FitScore: &score}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddInterview(store.Interview{JobID: j.ID, Stage: "technical", At: "2999-01-01T10:00"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertLearning(store.Learning{Skill: "Kubernetes"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPreference(store.Preference{Key: "remote", Value: "remote", Source: "stated", Confidence: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddDecision(j.ID, "apply", "good fit", `{"stack":["go"]}`); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRoutesRender(t *testing.T) {
	s := testStore(t)
	h, err := Handler(s, Config{FollowUpDays: 10, StaleDays: 60, WeeklyApplyTarget: 5})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/", "/pipeline", "/pipeline?q=acme", "/jobs/1", "/interviews", "/learning", "/preferences", "/profile", "/activity", "/reviews", "/pipeline?due=1", "/jobs/new", "/search", "/style.css", "/logo.svg", "/favicon.svg", "/api/jobs", "/api/jobs/1", "/api/stats", "/api/brief", "/api/events"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if rec.Code != 200 {
			t.Errorf("%s: %d %s", p, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "<no value>") {
			t.Errorf("%s: template rendered <no value>", p)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/jobs/999", nil))
	if rec.Code != 404 {
		t.Errorf("missing job: %d", rec.Code)
	}
	for _, p := range []string{"/jobs/1/files/resume.md", "/jobs/1/files/..%2F..%2Fprofile.md", "/jobs/1/files/resume.pdf"} {
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if rec.Code != 404 {
			t.Errorf("%s: expected 404 for missing or non-text draft, got %d", p, rec.Code)
		}
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if b := rec.Body.String(); !strings.Contains(b, `data-theme="light"`) || !strings.Contains(b, "<meter") || strings.Contains(b, "style=") {
		t.Errorf("overview: theme/meter/inline-style check failed")
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/theme?set=dark&back=/pipeline", nil))
	if rec.Code != 303 || !strings.Contains(rec.Header().Get("Set-Cookie"), "theme=dark") || rec.Header().Get("Location") != "/pipeline" {
		t.Errorf("theme switch: %d %v", rec.Code, rec.Header())
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: "theme", Value: "dark"})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), `data-theme="dark"`) {
		t.Error("theme cookie not applied")
	}
}

func TestDraftEditDueAndReviews(t *testing.T) {
	s := testStore(t)
	h, _ := Handler(s, Config{FollowUpDays: 10, StaleDays: 60, WeeklyApplyTarget: 5})
	form := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		return rec
	}
	if rec := form("/jobs/1/files/cover-letter.md", "body=Dear+Acme%2C%0D%0Ahire+me."); rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "saved=1") {
		t.Errorf("save draft: %d %s", rec.Code, rec.Body.String())
	}
	if b, err := os.ReadFile(filepath.Join(s.Home, "applications", "acme_go-engineer", "cover-letter.md")); err != nil || string(b) != "Dear Acme,\nhire me." {
		t.Errorf("draft on disk: %q %v", b, err)
	}
	if rec := get("/jobs/1/files/cover-letter.md?saved=1"); !strings.Contains(rec.Body.String(), "<textarea") || !strings.Contains(rec.Body.String(), "hire me.") {
		t.Errorf("file page not editable: %s", rec.Body.String())
	}
	if rec := form("/jobs/1/files/email.eml", "body=x"); rec.Code != 400 {
		t.Errorf("eml must not be writable: %d", rec.Code)
	}
	if rec := get("/api/jobs/1"); !strings.Contains(rec.Body.String(), "draft:edited") {
		t.Errorf("edit event missing")
	}
	// due filter: job 1 has no deadline and is "new", so nothing is due; a deadline this week puts it back
	if rec := get("/pipeline?due=1"); !strings.Contains(rec.Body.String(), "No match here") || !strings.Contains(rec.Body.String(), "Due this week ×") {
		t.Errorf("due filter should hide job 1: %s", rec.Body.String())
	}
	dl := time.Now().AddDate(0, 0, 3).Format("2006-01-02")
	_ = s.UpdateJob(1, store.JobPatch{Deadline: &dl})
	if rec := get("/pipeline?due=1"); !strings.Contains(rec.Body.String(), "Acme") {
		t.Errorf("due filter should show job with deadline: %s", rec.Body.String())
	}
	// reviews: empty, then one file
	if rec := get("/reviews"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "No review yet") {
		t.Errorf("empty reviews: %d", rec.Code)
	}
	os.MkdirAll(filepath.Join(s.Home, "reviews"), 0o755)
	os.WriteFile(filepath.Join(s.Home, "reviews", "2026-41.md"), []byte("# Week 41\n\nApplied 4 of 5."), 0o600)
	os.WriteFile(filepath.Join(s.Home, "reviews", "2026-40.md"), []byte("# Week 40"), 0o600)
	if rec := get("/reviews"); !strings.Contains(rec.Body.String(), "Applied 4 of 5") || !strings.Contains(rec.Body.String(), "2026-40.md") || !strings.Contains(rec.Body.String(), "<meter") {
		t.Errorf("reviews page: %s", rec.Body.String())
	}
	if rec := get("/reviews?f=2026-40.md"); !strings.Contains(rec.Body.String(), "# Week 40") {
		t.Errorf("pick review: %s", rec.Body.String())
	}
	if rec := get("/reviews?f=..%2Fprofile.md"); strings.Contains(rec.Body.String(), "Name") && !strings.Contains(rec.Body.String(), "Applied 4 of 5") {
		t.Errorf("traversal in reviews")
	}
}

func TestAPI(t *testing.T) {
	s := testStore(t)
	h, _ := Handler(s, Config{WeeklyApplyTarget: 5, APIToken: "secret"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/stats", nil))
	if rec.Code != 401 {
		t.Fatalf("no token: %d", rec.Code)
	}
	do := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer secret")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := do("GET", "/api/stats", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"open":1`) {
		t.Errorf("stats: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do("POST", "/api/jobs", `{"company":"Beta","title":"SRE","url":"https://b/1","remote":"remote"}`); rec.Code != 201 {
		t.Errorf("add: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do("POST", "/api/jobs", `{"company":"Beta","title":"SRE"}`); rec.Code != 200 {
		t.Errorf("upsert existing should be 200: %d", rec.Code)
	}
	if rec := do("POST", "/api/jobs", `{"company":"Beta"}`); rec.Code != 400 {
		t.Errorf("missing title: %d", rec.Code)
	}
	if rec := do("POST", "/api/jobs/2/status", `{"status":"applied"}`); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"status":"applied"`) {
		t.Errorf("status: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do("POST", "/api/jobs/2/status", `{"status":"nope"}`); rec.Code != 400 {
		t.Errorf("bad status: %d", rec.Code)
	}
	if rec := do("POST", "/api/jobs/2/note", `{"note":"via n8n"}`); rec.Code != 201 {
		t.Errorf("note: %d", rec.Code)
	}
	if rec := do("GET", "/api/jobs/2", ""); !strings.Contains(rec.Body.String(), "via n8n") {
		t.Errorf("job detail: %s", rec.Body.String())
	}
	if rec := do("GET", "/api/jobs?status=applied", ""); !strings.Contains(rec.Body.String(), "Beta") || strings.Contains(rec.Body.String(), "Acme") {
		t.Errorf("filter: %s", rec.Body.String())
	}
	if rec := do("GET", "/api/brief", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), "target_week") {
		t.Errorf("brief: %d", rec.Code)
	}
	if rec := do("GET", "/api/jobs/999", ""); rec.Code != 404 {
		t.Errorf("missing: %d", rec.Code)
	}
	// drafts through the API: the remote-agent path for CV and cover letter
	if rec := do("PUT", "/api/jobs/1/files/cover-letter.md", `{"body":"Dear Acme,\nhire me."}`); rec.Code != 201 {
		t.Errorf("write draft: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do("PUT", "/api/jobs/1/files/cover-letter.md", `{"body":"Dear Acme,\nhire me now."}`); rec.Code != 200 {
		t.Errorf("rewrite draft should be 200: %d", rec.Code)
	}
	if rec := do("PUT", "/api/jobs/1/files/resume.pdf", `{"body":"x"}`); rec.Code != 400 {
		t.Errorf("binary write must be refused: %d", rec.Code)
	}
	if rec := do("PUT", "/api/jobs/1/files/..%2F..%2Fprofile.md", `{"body":"x"}`); rec.Code != 400 && !strings.Contains(rec.Body.String(), "profile.md") {
		t.Errorf("traversal: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do("GET", "/api/jobs/1/files/cover-letter.md", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), "hire me now") {
		t.Errorf("read draft: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do("GET", "/api/jobs/1/files", ""); !strings.Contains(rec.Body.String(), `"cover":true`) || !strings.Contains(rec.Body.String(), `"resume":false`) {
		t.Errorf("kit: %s", rec.Body.String())
	}
	if rec := do("POST", "/api/jobs/1/export", ""); rec.Code != 400 {
		t.Errorf("export without resume.md must fail: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do("PUT", "/api/jobs/1/files/resume.md", `{"body":"# Jane\n\n## Skills\n- Go"}`); rec.Code != 201 {
		t.Errorf("write resume: %d", rec.Code)
	}
	if rec := do("POST", "/api/jobs/1/export", ""); rec.Code != 201 || !strings.Contains(rec.Body.String(), `resume.pdf`) {
		t.Errorf("export: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do("GET", "/api/jobs/1/files", ""); !strings.Contains(rec.Body.String(), `"resume":true`) || !strings.Contains(rec.Body.String(), `"name":"resume.pdf"`) {
		t.Errorf("kit after export: %s", rec.Body.String())
	}
	rec = httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/jobs/1/export", strings.NewReader("file=resume.md"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(rec, req)
	if rec.Code != 303 {
		t.Errorf("dashboard export: %d", rec.Code)
	}
	if rec := do("GET", "/api/jobs/1", ""); !strings.Contains(rec.Body.String(), "cv:export") {
		t.Errorf("export event missing: %s", rec.Body.String())
	}
	if rec := do("POST", "/api/jobs/1/email/draft", `{"to":"jobs@acme.test"}`); rec.Code != 201 || !strings.Contains(rec.Body.String(), `"subject":"Application: Go Engineer at Acme"`) {
		t.Errorf("email draft: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do("GET", "/api/jobs/1", ""); !strings.Contains(rec.Body.String(), `"contact":"jobs@acme.test"`) {
		t.Errorf("contact from draft: %s", rec.Body.String())
	}
	if rec := do("POST", "/api/cv/checks", `{"file":"resume.pdf","score":70,"ats_score":65,"summary":"ok","gaps":"add Go","job_id":1}`); rec.Code != 201 {
		t.Errorf("cv check: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do("GET", "/api/profile", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"ats_score":65`) {
		t.Errorf("profile: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do("GET", "/api/cv/checks", ""); !strings.Contains(rec.Body.String(), "add Go") {
		t.Errorf("cv checks: %s", rec.Body.String())
	}
}

func TestNotifyOnEvent(t *testing.T) {
	s := testStore(t)
	var got []store.Event
	s.Notify = func(e store.Event) { got = append(got, e) }
	h, _ := Handler(s, Config{})
	post(h, "/jobs/1/note", url.Values{"note": {"ping"}})
	if len(got) != 1 || got[0].Kind != "note" || got[0].Company != "Acme" {
		t.Errorf("notify: %+v", got)
	}
}

func post(h http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestPosts(t *testing.T) {
	s := testStore(t)
	h, _ := Handler(s, Config{FollowUpDays: 10, StaleDays: 60, WeeklyApplyTarget: 5})
	if rec := post(h, "/jobs/1/status", url.Values{"status": {"skipped"}, "reason": {"meh"}, "back": {"/pipeline"}}); rec.Code != 303 || rec.Header().Get("Location") != "/pipeline" {
		t.Fatalf("status: %d %s", rec.Code, rec.Body.String())
	}
	j, _ := s.JobByID(1)
	if j.Status != "skipped" {
		t.Errorf("status not applied: %s", j.Status)
	}
	decs, _ := s.Decisions(10)
	if len(decs) != 2 || decs[0].Kind != "skip" {
		t.Errorf("skip decision not logged: %+v", decs)
	}
	if rec := post(h, "/jobs/1/status", url.Values{"status": {"bogus"}}); rec.Code != 400 {
		t.Errorf("bad status accepted: %d", rec.Code)
	}
	if rec := post(h, "/jobs/1/status", url.Values{"status": {"applied"}, "back": {"https://evil.example"}}); rec.Header().Get("Location") != "/pipeline" {
		t.Errorf("open redirect: %s", rec.Header().Get("Location"))
	}
	post(h, "/jobs/1/note", url.Values{"note": {"called recruiter"}})
	ev, _ := s.Events(1, 5)
	if ev[0].Note != "called recruiter" {
		t.Errorf("note missing: %+v", ev[0])
	}
	post(h, "/interviews/1", url.Values{"prep_status": {"ready"}, "outcome": {"passed"}})
	ivs, _ := s.Interviews(1, false)
	if ivs[0].PrepStatus != "ready" || ivs[0].Outcome != "passed" {
		t.Errorf("interview patch: %+v", ivs[0])
	}
	post(h, "/learning/1", url.Values{"hours": {"2.5"}, "status": {"in_progress"}})
	post(h, "/learning/1", url.Values{"hours": {"1"}})
	ls, _ := s.LearningItems()
	if ls[0].Hours != 3.5 || ls[0].Status != "in_progress" {
		t.Errorf("learning patch: %+v", ls[0])
	}
}

func TestAPIParity(t *testing.T) {
	s := testStore(t)
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"jobs":[{"id":7,"title":"Senior Go Engineer","absolute_url":"https://gh/7","content":"go and sql","location":{"name":"Berlin"}}]}`))
	}))
	defer gh.Close()
	h, _ := Handler(s, Config{WeeklyApplyTarget: 5, APIToken: "secret", SearchBaseURL: map[string]string{"greenhouse": gh.URL}})
	do := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer secret")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	want := func(rec *httptest.ResponseRecorder, code int, substr string) {
		t.Helper()
		if rec.Code != code || !strings.Contains(rec.Body.String(), substr) {
			t.Errorf("want %d containing %q, got %d %s", code, substr, rec.Code, rec.Body.String())
		}
	}
	// interviews
	want(do("POST", "/api/interviews", `{"job_id":1,"stage":"technical","at":"2099-01-01T10:00"}`), 201, `"company":"Acme"`)
	want(do("POST", "/api/interviews", `{"job_id":1}`), 400, "stage")
	want(do("POST", "/api/interviews", `{"job_id":99,"stage":"screen"}`), 404, "not found")
	want(do("GET", "/api/jobs/1", ""), 200, `"status":"interview"`)
	want(do("PATCH", "/api/interviews/1", `{"outcome":"passed","feedback":"strong"}`), 200, `"feedback":"strong"`)
	want(do("GET", "/api/interviews?all=1", ""), 200, `"outcome":"passed"`)
	// sessions
	want(do("POST", "/api/sessions", `{"kind":"mock_coding","topic":"graphs","score":80}`), 201, "ok")
	want(do("POST", "/api/sessions", `{"topic":"x"}`), 400, "kind")
	want(do("GET", "/api/sessions", ""), 200, `"topic":"graphs"`)
	// learning
	want(do("POST", "/api/learning", `{"skill":"Kubernetes","priority":1}`), 201, "ok")
	want(do("GET", "/api/learning", ""), 200, `"skill":"kubernetes"`)
	want(do("PATCH", "/api/learning/1", `{"hours":1.5,"status":"in_progress"}`), 200, `"status":"in_progress"`)
	want(do("PATCH", "/api/learning/1", `{"hours":1}`), 200, `"hours":2.5`)
	// preferences
	want(do("PUT", "/api/preferences/remote", `{"value":"remote","confidence":0.9,"source":"stated"}`), 200, `"key":"remote"`)
	want(do("GET", "/api/preferences", ""), 200, `"value":"remote"`)
	if rec := do("DELETE", "/api/preferences/remote", ""); rec.Code != 204 {
		t.Errorf("delete pref: %d", rec.Code)
	}
	if rec := do("GET", "/api/preferences", ""); strings.Contains(rec.Body.String(), `"key":"remote"`) {
		t.Errorf("pref not deleted")
	}
	// decisions and skip
	want(do("POST", "/api/decisions", `{"job_id":1,"kind":"override_score","reason":"old stack","signals":{"stack":["php"]}}`), 201, "ok")
	want(do("GET", "/api/decisions", ""), 200, `"kind":"override_score"`)
	want(do("POST", "/api/jobs/1/skip", `{}`), 400, "reason")
	want(do("POST", "/api/jobs/1/skip", `{"reason":"onsite only","signals":{"remote":"onsite"}}`), 200, `"status":"skipped"`)
	want(do("GET", "/api/decisions", ""), 200, `"reason":"onsite only"`)
	// search
	want(do("POST", "/api/search", `{"queries":["go"]}`), 400, "nothing to search")
	want(do("POST", "/api/search", `{"queries":["go"],"boards":["greenhouse:acme"],"add":true}`), 200, `"added":1`)
	want(do("GET", "/api/jobs?q=senior", ""), 200, `"url":"https://gh/7"`)
}

func TestAddJobForm(t *testing.T) {
	s := testStore(t)
	h, _ := Handler(s, Config{Currency: "EUR"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/jobs/new", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `value="EUR"`) {
		t.Fatalf("GET /jobs/new: %d %s", rec.Code, rec.Body.String())
	}
	rec = post(h, "/jobs", url.Values{"company": {" Globex "}, "title": {"SRE"}, "remote": {"hybrid"}, "salary_min": {"70000"}, "tags": {"go, sre"}})
	if rec.Code != 303 || rec.Header().Get("Location") != "/jobs/2" {
		t.Fatalf("POST /jobs: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	j, err := s.JobByID(2)
	if err != nil || j.Company != "Globex" || j.Remote != "hybrid" || j.SalaryMin != 70000 || j.Tags != "go, sre" {
		t.Errorf("stored job: %+v %v", j, err)
	}
	rec = post(h, "/jobs", url.Values{"company": {"Globex"}, "title": {"SRE"}, "back": {"/search"}})
	if rec.Code != 303 || rec.Header().Get("Location") != "/search" {
		t.Errorf("repeat add should follow back: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if jobs, _ := s.ListJobs(store.JobFilter{Query: "globex"}); len(jobs) != 1 {
		t.Errorf("duplicate created: %d", len(jobs))
	}
	if rec = post(h, "/jobs", url.Values{"title": {"no company"}}); rec.Code != 400 {
		t.Errorf("missing company: %d", rec.Code)
	}
}

func TestSearchForm(t *testing.T) {
	s := testStore(t)
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"jobs":[{"id":7,"title":"Senior Go Engineer","absolute_url":"https://gh/7","content":"go and sql","location":{"name":"Berlin"}}]}`))
	}))
	defer gh.Close()
	h, _ := Handler(s, Config{SearchQueries: []string{"go"}, SearchBoards: []string{"greenhouse:acme"}, SearchRemote: true, SearchBaseURL: map[string]string{"greenhouse": gh.URL}})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/search", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `value="greenhouse:acme"`) || !strings.Contains(rec.Body.String(), "checked") {
		t.Fatalf("GET /search: %d %s", rec.Code, rec.Body.String())
	}
	rec = post(h, "/search", url.Values{"queries": {"go"}, "boards": {""}})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "nothing to search") {
		t.Errorf("empty search: %d", rec.Code)
	}
	rec = post(h, "/search", url.Values{"queries": {"go"}, "boards": {"greenhouse:acme"}})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Senior Go Engineer") || !strings.Contains(rec.Body.String(), `name="company" value="acme"`) {
		t.Fatalf("search results: %d %s", rec.Code, rec.Body.String())
	}
	if jobs, _ := s.ListJobs(store.JobFilter{Query: "senior"}); len(jobs) != 0 {
		t.Errorf("search without add stored %d jobs", len(jobs))
	}
	rec = post(h, "/search", url.Values{"queries": {"go"}, "boards": {"greenhouse:acme"}, "add": {"1"}})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "1 added") {
		t.Errorf("search and add: %d", rec.Code)
	}
	if jobs, _ := s.ListJobs(store.JobFilter{Query: "senior"}); len(jobs) != 1 || jobs[0].Source != "greenhouse" {
		t.Errorf("added jobs: %+v", jobs)
	}
}
