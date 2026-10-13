package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/gmail"
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
		"":           "Next step",
		"interviews": `name="prep_form"`,
		"files":      "Files",
		"activity":   `name="note"`,
		"nonsense":   "Next step",
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

func TestHealthz(t *testing.T) {
	s := testStore(t)
	h, _ := Handler(s, Config{Version: "0.3.2"})
	var got Health
	if err := json.Unmarshal(get(h, "/healthz").Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.App != "scoutjobs" || got.Version != "0.3.2" || got.Home != s.Home || got.PID != os.Getpid() {
		t.Errorf("%+v", got)
	}
}

func TestPreferenceReview(t *testing.T) {
	s := testStore(t)
	h, _ := Handler(s, Config{APIToken: "secret"})
	if err := s.ProposePreference(store.Proposal{Key: "stack_avoid", Value: "java", Support: 5, Confidence: 0.7, Evidence: "5 skips mention Java"}); err != nil {
		t.Fatal(err)
	}
	if err := s.ProposePreference(store.Proposal{Key: "on_call", Value: "avoid", Support: 3}); err != nil {
		t.Fatal(err)
	}
	b := get(h, "/preferences").Body.String()
	for _, want := range []string{"Awaiting your review", "5 skips mention Java", "<b>5</b> decisions support it", `value="confirm"`} {
		if !strings.Contains(b, want) {
			t.Errorf("review panel lacks %q", want)
		}
	}
	if rec := post(h, "/preferences/review", url.Values{"key": {"stack_avoid"}, "action": {"edit"}, "value": {"java, spring"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("edit: %d %s", rec.Code, rec.Body)
	}
	if rec := post(h, "/preferences/review", url.Values{"key": {"on_call"}, "action": {"reject"}, "reason": {"fine with it"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("reject: %d %s", rec.Code, rec.Body)
	}
	b = get(h, "/preferences").Body.String()
	if strings.Contains(b, "Awaiting your review") || !strings.Contains(b, "1 rejected proposal") || !strings.Contains(b, "fine with it") {
		t.Error("review panel did not update")
	}
	prefs, _ := s.Preferences()
	got := map[string]store.Preference{}
	for _, p := range prefs {
		got[p.Key] = p
	}
	if p := got["stack_avoid"]; p.Value != "java, spring" || p.Source != "stated" {
		t.Errorf("edited: %+v", p)
	}
	if _, ok := got["on_call"]; ok {
		t.Error("a rejected proposal became a preference")
	}

	req := httptest.NewRequest("PUT", "/api/preferences/stack_avoid", strings.NewReader(`{"value":"go","source":"learned"}`))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 409 {
		t.Errorf("learned PUT over stated: %d", rec.Code)
	}
	req = httptest.NewRequest("POST", "/api/preferences/proposals/nope", strings.NewReader(`{"action":"confirm"}`))
	req.Header.Set("Authorization", "Bearer secret")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Errorf("confirm unknown: %d", rec.Code)
	}
}

func TestGmailConnectFromInbox(t *testing.T) {
	s := testStore(t)
	client := func() *gmail.Client {
		c := gmail.LoadCredentials(s.Home)
		return &gmail.Client{ID: c.ID, Secret: c.Secret, Home: s.Home, AuthURL: "https://auth.test/o"}
	}
	h, _ := Handler(s, Config{Mail: MailHooks{Gmail: client}})
	downloads := t.TempDir()
	defer func(old func() string) { downloadsDir = old }(downloadsDir)
	downloadsDir = func() string { return downloads }
	send := func(method, path, body, origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Host = "127.0.0.1:7070"
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if b := get(h, "/inbox").Body.String(); !strings.Contains(b, "Connect your email &amp; calendar") || !strings.Contains(b, "Your own Google client") || !strings.Contains(b, `action="/gmail/client"`) || !strings.Contains(b, "Watching Downloads") {
		t.Fatal("inbox lacks the Google setup wizard")
	}
	if b := get(h, "/gmail/detect").Body.String(); !strings.Contains(b, `"file":""`) || !strings.Contains(b, `"blind":false`) {
		t.Errorf("detect before download: %s", b)
	}
	downloadsDir = func() string { return filepath.Join(downloads, "missing") }
	if b := get(h, "/inbox").Body.String(); !strings.Contains(b, "cannot look in your Downloads") || strings.Contains(b, "Watching Downloads") || !strings.Contains(b, `class="manual" open`) {
		t.Error("unreadable Downloads still shows the watcher")
	}
	downloadsDir = func() string { return downloads }
	if err := os.WriteFile(filepath.Join(downloads, "client_secret_9.json"), []byte(`{"installed":{"client_id":"9-z.apps.googleusercontent.com","client_secret":"s9"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if b := get(h, "/gmail/detect").Body.String(); !strings.Contains(b, "client_secret_9.json") || strings.Contains(b, "s9") {
		t.Errorf("detect after download: %s", b)
	}
	if b := get(h, "/inbox").Body.String(); !strings.Contains(b, `name="detected"`) {
		t.Error("inbox does not offer the download")
	}
	if rec := send("POST", "/gmail/client", "detected=1", "http://127.0.0.1:7070"); !strings.Contains(rec.Header().Get("Location"), "client_secret_9.json") {
		t.Fatalf("import download: %s", rec.Header().Get("Location"))
	}
	if gmail.LoadCredentials(s.Home).ID != "9-z.apps.googleusercontent.com" {
		t.Fatal("download not imported")
	}
	clientForm := url.Values{"client": {`{"installed":{"client_id":"1-a.apps.googleusercontent.com","client_secret":"s"}}`}}.Encode()
	if rec := send("POST", "/gmail/client", clientForm, "https://evil.test"); rec.Code != http.StatusForbidden {
		t.Errorf("cross-origin client save: %d", rec.Code)
	}
	if rec := send("POST", "/gmail/client", clientForm, "http://127.0.0.1:7070"); !strings.Contains(rec.Header().Get("Location"), "msg=") {
		t.Fatalf("client save: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if gmail.LoadCredentials(s.Home).ID != "1-a.apps.googleusercontent.com" {
		t.Fatal("client not stored")
	}
	if b := get(h, "/inbox").Body.String(); !strings.Contains(b, `action="/gmail/connect"`) {
		t.Error("no Connect button once the client is saved")
	}
	rec := send("POST", "/gmail/connect", "", "")
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if loc.Host != "auth.test" || loc.Query().Get("redirect_uri") != "http://127.0.0.1:7070/gmail/callback" {
		t.Fatalf("connect redirect %q", rec.Header().Get("Location"))
	}
	if rec := send("GET", "/gmail/callback?state=forged&code=c", "", ""); !strings.Contains(rec.Header().Get("Location"), "err=") {
		t.Errorf("forged state: %s", rec.Header().Get("Location"))
	}
	if rec := send("GET", "/gmail/callback?state="+loc.Query().Get("state")+"&error=access_denied", "", ""); !strings.Contains(rec.Header().Get("Location"), "access_denied") {
		t.Errorf("refusal: %s", rec.Header().Get("Location"))
	}

	// a reconnect from the Calendar page reports there
	rec = send("POST", "/gmail/connect", "for=calendar", "")
	loc, _ = url.Parse(rec.Header().Get("Location"))
	back, _ := url.Parse(send("GET", "/gmail/callback?state="+loc.Query().Get("state")+"&error=access_denied", "", "").Header().Get("Location"))
	if back.Path != "/calendar" || !strings.HasPrefix(back.Query().Get("err"), "Google was not connected") {
		t.Errorf("calendar refusal: %s", back)
	}

	// without the secret nobody is sent to Google
	_ = gmail.SaveCredentials(s.Home, gmail.Credentials{ID: "1-a.apps.googleusercontent.com"})
	back, _ = url.Parse(send("POST", "/gmail/connect", "for=calendar", "").Header().Get("Location"))
	if back.Path != "/calendar" || !strings.Contains(back.Query().Get("err"), "Google was not connected: the Google OAuth client secret is missing") {
		t.Errorf("missing secret: %s", back)
	}
}

func TestPreferenceForms(t *testing.T) {
	s := testStore(t)
	h, _ := Handler(s, Config{})
	if rec := post(h, "/preferences", url.Values{"key": {" Company size "}, "value": {"50-500"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("set: %d %s", rec.Code, rec.Body)
	}
	pref := func(key string) (store.Preference, bool) {
		prefs, _ := s.Preferences()
		for _, p := range prefs {
			if p.Key == key {
				return p, true
			}
		}
		return store.Preference{}, false
	}
	if p, ok := pref("company_size"); !ok || p.Source != "stated" || p.Value != "50-500" {
		t.Fatalf("company_size = %+v", p)
	}
	if b := get(h, "/preferences").Body.String(); !strings.Contains(b, "<b>company size</b>") || !strings.Contains(b, "You set it") {
		t.Error("preference row missing")
	}
	if rec := post(h, "/preferences", url.Values{"key": {"salary"}}); rec.Code == http.StatusSeeOther {
		t.Error("empty value accepted")
	}
	if rec := post(h, "/preferences/delete", url.Values{"key": {"company_size"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("delete: %d", rec.Code)
	}
	if _, ok := pref("company_size"); ok {
		t.Error("not deleted")
	}
}

func TestGoogleReconnectFromConnections(t *testing.T) {
	s := testStore(t)
	calendarDown := false
	google := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_, _ = w.Write([]byte(`{"access_token":"at","refresh_token":"rt","expires_in":3600,"refresh_token_expires_in":604800,
				"scope":"` + gmail.Scopes + `"}`))
		case "/api/profile":
			_, _ = w.Write([]byte(`{"emailAddress":"me@x.test"}`))
		case "/cal/calendars/primary":
			if calendarDown {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":{"status":"PERMISSION_DENIED","message":"Calendar API has not been used in project 1"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":"me@x.test"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer google.Close()
	_ = gmail.SaveCredentials(s.Home, gmail.Credentials{ID: "1-a.apps.googleusercontent.com", Secret: "s"})
	client := func() *gmail.Client {
		c := gmail.LoadCredentials(s.Home)
		return &gmail.Client{ID: c.ID, Secret: c.Secret, Home: s.Home, IDFrom: gmail.ClientFile, HTTP: google.Client(), AuthURL: "https://auth.test/o",
			TokenURL: google.URL + "/token", APIBase: google.URL + "/api", CalBase: google.URL + "/cal"}
	}
	h, _ := Handler(s, Config{Mail: MailHooks{Gmail: client}})
	send := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Host = "127.0.0.1:7070"
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	connect := func() *url.URL {
		loc, _ := url.Parse(send("POST", "/gmail/connect", "for=connections").Header().Get("Location"))
		back, _ := url.Parse(send("GET", "/gmail/callback?code=c&state="+loc.Query().Get("state"), "").Header().Get("Location"))
		return back
	}

	if b := get(h, "/connections").Body.String(); !strings.Contains(b, `id="google"`) || !strings.Contains(b, "Connect Google") || !strings.Contains(b, `name="for" value="connections"`) {
		t.Fatal("connections lacks the Google setup")
	}
	back := connect()
	if back.Path != "/connections" || back.Fragment != "google" || !strings.Contains(back.Query().Get("msg"), "checked: Gmail and Google Calendar both answer") ||
		!strings.Contains(back.Query().Get("msg"), "Testing mode") {
		t.Fatalf("connect: %s", back)
	}
	b := get(h, "/connections").Body.String()
	for _, want := range []string{"Reconnect Google", gmail.BrandingURL, gmail.AudienceURL, gmail.HomePage, "zoolcoder.github.io", "has not verified the app", `data-test="google"`} {
		if !strings.Contains(b, want) {
			t.Errorf("connections lacks %q", want)
		}
	}
	if b := get(h, "/connections/test?card=google").Body.String(); !strings.Contains(b, `"ok":true`) || !strings.Contains(b, "Gmail answers for me@x.test") {
		t.Errorf("verify: %s", b)
	}

	// reconnecting needs no disconnect, and the post-connect check reports Calendar on its own
	calendarDown = true
	back = connect()
	if e := back.Query().Get("err"); !strings.Contains(e, "Google connected as me@x.test, but calendar:") || strings.Contains(e, "mail:") {
		t.Errorf("reconnect check: %s", back)
	}
	if b := get(h, "/connections/test?card=google").Body.String(); !strings.Contains(b, `"ok":false`) || !strings.Contains(b, "enable Gmail and Calendar") {
		t.Errorf("verify with Calendar off: %s", b)
	}
}
