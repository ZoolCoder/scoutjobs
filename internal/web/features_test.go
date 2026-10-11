package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/search"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec
}

func TestMailInboxTodayAndAPI(t *testing.T) {
	s := testStore(t)
	h, _ := Handler(s, Config{APIToken: "secret"})
	do := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer secret")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if b := get(h, "/inbox").Body.String(); !strings.Contains(b, "No replies logged yet") {
		t.Errorf("empty inbox should explain setup: %s", b)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	body := `{"from":"Acme HR <hr@acme.test>","subject":"Next steps","snippet":"Tuesday?","thread":"t1","kind":"interview","date":"` + now + `"}`
	if rec := do("POST", "/api/jobs/1/mail", body); rec.Code != 201 {
		t.Fatalf("log mail: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do("POST", "/api/jobs/1/mail", body); rec.Code != 200 {
		t.Errorf("repeat log should dedupe: %d", rec.Code)
	}
	if rec := do("POST", "/api/jobs/1/mail", `{"from":"x@y"}`); rec.Code != 400 {
		t.Errorf("no subject: %d", rec.Code)
	}
	if rec := do("POST", "/api/jobs/99/mail", body); rec.Code != 404 {
		t.Errorf("unknown job: %d", rec.Code)
	}
	rec := do("GET", "/api/mail?job=1", "")
	var ms []store.Mail
	if err := json.Unmarshal(rec.Body.Bytes(), &ms); err != nil || len(ms) != 1 || ms[0].From != "hr@acme.test" || ms[0].Source != "connector" {
		t.Errorf("api mail: %s", rec.Body.String())
	}

	b := get(h, "/inbox").Body.String()
	for _, want := range []string{"Next steps", `href="/jobs/1?tab=mail"`, "badge k-interview"} {
		if !strings.Contains(b, want) {
			t.Errorf("inbox lacks %q", want)
		}
	}
	if b := get(h, "/inbox?kind=rejected").Body.String(); !strings.Contains(b, "No rejected messages") {
		t.Error("kind filter should hide the interview mail")
	}
	if b := get(h, "/jobs/1?tab=mail").Body.String(); !strings.Contains(b, "Next steps") || !strings.Contains(b, `class="on" aria-current="page" href="/jobs/1?tab=mail"`) {
		t.Errorf("job mail tab: %s", b)
	}

	// a fresh reply shows on the overview's Today list
	if b := get(h, "/").Body.String(); !strings.Contains(b, "Answer Acme: Next steps") {
		t.Error("today list lacks the reply")
	}
}

func TestPrepChecklist(t *testing.T) {
	s := testStore(t)
	h, _ := Handler(s, Config{APIToken: "secret"})
	b := get(h, "/interviews").Body.String()
	if !strings.Contains(b, `name="prep_form" value="1"`) || !strings.Contains(b, `name="prep" value="mock"`) {
		t.Fatalf("interviews page lacks the checklist")
	}
	rec := post(h, "/interviews/1", url.Values{"prep_form": {"1"}, "prep": {"company", "mock", "bogus"}, "back": {"/interviews"}})
	if rec.Code != 303 {
		t.Fatalf("save prep: %d %s", rec.Code, rec.Body.String())
	}
	is, _ := s.Interviews(1, false)
	if len(is) != 1 || is[0].PrepCount() != 2 || !is[0].PrepDone("mock") || is[0].PrepStatus != "in_progress" {
		t.Fatalf("after save %+v", is)
	}
	// unticking everything clears the list; the hidden prep_form field tells an empty form from no form
	post(h, "/interviews/1", url.Values{"prep_form": {"1"}})
	if is, _ = s.Interviews(1, false); is[0].PrepCount() != 0 {
		t.Errorf("cleared prep: %+v", is[0])
	}
	// an outcome form without prep_form leaves prep alone
	post(h, "/interviews/1", url.Values{"prep_form": {"1"}, "prep": {"stories"}})
	post(h, "/interviews/1", url.Values{"outcome": {"pending"}})
	if is, _ = s.Interviews(1, false); !is[0].PrepDone("stories") || is[0].Outcome != "pending" {
		t.Errorf("outcome form touched prep: %+v", is[0])
	}

	req := httptest.NewRequest("PATCH", "/api/interviews/1", strings.NewReader(`{"prep":"company,posting,stories,questions,mock,logistics"}`))
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("api prep: %d %s", rec.Code, rec.Body.String())
	}
	if is, _ = s.Interviews(1, false); is[0].PrepCount() != 6 || is[0].PrepStatus != "ready" {
		t.Errorf("api prep: %+v", is[0])
	}

	// a past interview without an outcome asks how it went
	if _, err := s.AddInterview(store.Interview{JobID: 1, Stage: "screen", At: "2020-01-01T10:00"}); err != nil {
		t.Fatal(err)
	}
	if b := get(h, "/interviews").Body.String(); !strings.Contains(b, "How did it go?") || !strings.Contains(b, `name="outcome" value="passed"`) {
		t.Error("needs-outcome section missing")
	}
}

func TestJobTabsAndCalendarPrefill(t *testing.T) {
	s := testStore(t)
	h, _ := Handler(s, Config{})
	for tab, want := range map[string]string{
		"":           "Get selected",
		"interviews": `name="prep_form"`,
		"files":      "Files",
		"activity":   `name="note"`,
		"nonsense":   "Get selected",
	} {
		path := "/jobs/1"
		if tab != "" {
			path += "?tab=" + tab
		}
		rec := get(h, path)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s: %d, want %q", path, rec.Code, want)
		}
	}
	b := get(h, "/jobs/1").Body.String()
	if !strings.Contains(b, `href="/calendar?new=`) || !strings.Contains(b, "&amp;job=1") {
		t.Error("job page lacks Schedule interview")
	}
	// a note from the activity tab returns there
	rec := post(h, "/jobs/1/note", url.Values{"note": {"called the recruiter"}, "back": {"/jobs/1?tab=activity"}})
	if rec.Code != 303 || rec.Header().Get("Location") != "/jobs/1?tab=activity" {
		t.Errorf("note redirect: %d %s", rec.Code, rec.Header().Get("Location"))
	}

	j2, _, _ := s.UpsertJob(store.Job{Company: "Beta", Title: "SRE"})
	b = get(h, "/calendar?new=2999-01-02&job="+itoa(j2.ID)).Body.String()
	if !strings.Contains(b, `<option value="`+itoa(j2.ID)+`" selected>`) {
		t.Errorf("calendar did not preselect job %d", j2.ID)
	}
}

func TestSavedSearchesAndFilters(t *testing.T) {
	s := testStore(t)
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/jobs") { // the per-job fetch for the description
			_, _ = w.Write([]byte(`{"content":"&lt;p&gt;Go and SQL&lt;/p&gt;"}`))
			return
		}
		_, _ = w.Write([]byte(`{"jobs":[
			{"id":1,"title":"Senior Go Engineer","absolute_url":"https://gh/1","content":"go and sql","location":{"name":"Remote"}},
			{"id":2,"title":"Go Engineer Intern","absolute_url":"https://gh/2","content":"go","location":{"name":"Remote"}},
			{"id":3,"title":"Go Platform Engineer","absolute_url":"https://gh/3","content":"go","location":{"name":"Berlin"}}]}`))
	}))
	defer gh.Close()
	h, _ := Handler(s, Config{SearchBaseURL: map[string]string{"greenhouse": gh.URL}})

	rec := post(h, "/search", url.Values{"queries": {"go"}, "boards": {"greenhouse:acme"}, "exclude": {"intern"}})
	b := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(b, "Senior Go Engineer") || strings.Contains(b, "Go Engineer Intern") || !strings.Contains(b, "1 hidden by filters") {
		t.Fatalf("exclude filter: %d %s", rec.Code, b)
	}
	if !strings.Contains(b, "<summary>Preview</summary>") {
		t.Error("result rows lack the description preview")
	}

	rec = post(h, "/search", url.Values{"queries": {"go"}, "boards": {"greenhouse:acme"}, "exclude": {"intern"}, "name": {"Go remote"}, "save": {"1"}})
	if rec.Code != 200 {
		t.Fatalf("save: %d", rec.Code)
	}
	saved, _ := s.SavedSearches()
	if len(saved) != 1 || saved[0].Name != "Go remote" || saved[0].Boards != "greenhouse:acme" || !strings.Contains(saved[0].Filters, "exclude=intern") {
		t.Fatalf("saved %+v", saved)
	}
	if b := get(h, "/search").Body.String(); !strings.Contains(b, `name="saved" value="`+itoa(saved[0].ID)+`"`) {
		t.Error("search page lacks the saved chip")
	}

	// running the saved search applies its filters, whatever the form says
	b = post(h, "/search", url.Values{"saved": {itoa(saved[0].ID)}}).Body.String()
	if !strings.Contains(b, "Results · Go remote") || strings.Contains(b, "Go Engineer Intern") || !strings.Contains(b, `value="intern"`) {
		t.Errorf("run saved: %s", b)
	}
	if rec := post(h, "/search", url.Values{"saved": {"999"}}); rec.Code != 404 {
		t.Errorf("unknown saved search: %d", rec.Code)
	}

	rec = post(h, "/search/saved/"+itoa(saved[0].ID)+"/delete", nil)
	if rec.Code != 303 {
		t.Errorf("delete: %d", rec.Code)
	}
	if saved, _ = s.SavedSearches(); len(saved) != 0 {
		t.Errorf("after delete %+v", saved)
	}
}

func TestResultFilterKeep(t *testing.T) {
	now := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	f := filterFromValues(url.Values{"remote_only": {"1"}, "days": {"7"}, "min_salary": {"80000"}, "exclude": {"PHP, intern"}})
	cases := []struct {
		p    search.Posting
		keep bool
	}{
		{search.Posting{Title: "Go Dev", Remote: "remote"}, true},
		{search.Posting{Title: "Go Dev", Remote: "onsite"}, false},
		{search.Posting{Title: "Senior PHP Dev", Remote: "remote"}, false},
		{search.Posting{Title: "Go Dev", Remote: "remote", PostedAt: "2026-09-01"}, false},
		{search.Posting{Title: "Go Dev", Remote: "remote", PostedAt: "2026-10-09T08:00:00Z"}, true},
		{search.Posting{Title: "Go Dev", Remote: "remote", SalaryMax: 60000}, false},
		{search.Posting{Title: "Go Dev", Remote: "remote", SalaryMax: 90000}, true},
	}
	for _, c := range cases {
		if got := f.keep(c.p, now); got != c.keep {
			t.Errorf("%+v: keep %v, want %v", c.p, got, c.keep)
		}
	}
	if enc := f.encode(); filterFromValues(mustQuery(t, enc)) != f {
		t.Errorf("round trip %q", enc)
	}
}

func mustQuery(t *testing.T, s string) url.Values {
	t.Helper()
	q, err := url.ParseQuery(s)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func TestVersionBadgeAndEndpoint(t *testing.T) {
	s := testStore(t)
	h, _ := Handler(s, Config{Version: "0.3.1"})
	b := get(h, "/").Body.String()
	if !strings.Contains(b, `data-version="0.3.1"`) || !strings.Contains(b, `class="ver"`) || !strings.Contains(b, `src="/app.js"`) {
		t.Error("layout lacks the version badge or app.js")
	}
	if rec := get(h, "/version"); rec.Body.String() != "0.3.1" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("/version: %q %q", rec.Body.String(), rec.Header().Get("Cache-Control"))
	}
	if rec := get(h, "/app.js"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "/version") {
		t.Errorf("/app.js: %d", rec.Code)
	}
}
