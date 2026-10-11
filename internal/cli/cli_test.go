package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func run(t *testing.T, args ...string) string {
	t.Helper()
	var b bytes.Buffer
	if err := Run(args, &b); err != nil {
		t.Fatalf("%v: %v\n%s", args, err, b.String())
	}
	return b.String()
}

func TestEndToEnd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SCOUTJOBS_HOME", home)
	run(t, "init")
	for _, f := range []string{"profile.md", "preferences.md", "config.yaml", "scoutjobs.db"} {
		if _, err := os.Stat(filepath.Join(home, f)); err != nil {
			t.Errorf("init did not create %s", f)
		}
	}
	out := run(t, "job", "add", "--company", "Acme", "--title", "Go Dev", "--tags", "go", "--json")
	var added struct {
		Created bool `json:"created"`
		Job     struct {
			ID int64 `json:"id"`
		} `json:"job"`
	}
	if err := json.Unmarshal([]byte(out), &added); err != nil || !added.Created || added.Job.ID != 1 {
		t.Fatalf("job add json: %v %s", err, out)
	}
	run(t, "job", "set", "1", "--score", "88", "--status", "applied", "--note", "sent via portal")
	run(t, "job", "add", "--company", "Globex", "--title", "Java Lead")
	run(t, "job", "skip", "2", "--reason", "java shop", "--signals", `{"stack":["java"]}`)
	run(t, "interview", "add", "1", "--stage", "screen", "--at", "2999-01-01")
	run(t, "interview", "set", "1", "--prep-status", "ready")
	run(t, "session", "log", "--kind", "mock_behavioral", "--score", "70")
	run(t, "learn", "add", "--skill", "Kubernetes")
	run(t, "learn", "set", "1", "--hours", "2")
	run(t, "pref", "set", "remote", "remote", "--source", "stated")
	run(t, "decision", "log", "--kind", "override_score", "--reason", "scorer too harsh", "--job", "acme")

	list := run(t, "job", "list")
	if !strings.Contains(list, "interview") || !strings.Contains(list, "skipped") || !strings.Contains(list, "88") {
		t.Errorf("job list: %s", list)
	}
	prefs, _ := os.ReadFile(filepath.Join(home, "preferences.md"))
	if !strings.Contains(string(prefs), "**remote**: remote") {
		t.Errorf("preferences.md not rendered: %s", prefs)
	}
	var stats map[string]any
	if err := json.Unmarshal([]byte(run(t, "stats", "--json")), &stats); err != nil || stats["total"].(float64) != 2 {
		t.Errorf("stats json: %v %v", err, stats)
	}
	var brief map[string]any
	if err := json.Unmarshal([]byte(run(t, "brief")), &brief); err != nil || brief["upcoming_interviews"] == nil {
		t.Errorf("brief: %v", err)
	}
	var sum map[string]any
	if err := json.Unmarshal([]byte(run(t, "decision", "summary", "--json")), &sum); err != nil {
		t.Fatal(err)
	}
	if sum["counts"].(map[string]any)["skip"].(float64) != 1 {
		t.Errorf("summary: %v", sum["counts"])
	}
	if err := Run([]string{"job", "skip", "1"}, &bytes.Buffer{}); err == nil {
		t.Error("skip without reason should fail")
	}
}

func TestConfig(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, "config.yaml"), []byte("follow_up_days: 3 # fast\ncurrency: USD\nbogus: 1\n"), 0o600)
	c := LoadConfig(home)
	if c.FollowUpDays != 3 || c.Currency != "USD" || c.StaleDays != 60 {
		t.Errorf("config: %+v", c)
	}
}

func TestCVImportWebhookCompact(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SCOUTJOBS_HOME", home)
	run(t, "init")
	if _, err := os.Stat(filepath.Join(home, "cv", "README.md")); err != nil {
		t.Error("init did not create cv/README.md")
	}
	if err := Run([]string{"search", "--remote=false"}, io.Discard); err == nil {
		t.Error("search with no boards and no remote should fail before any network call")
	}
	var hits, sigs []string
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		hits = append(hits, string(b))
		sigs = append(sigs, r.Header.Get("X-ScoutJobs-Signature")+" "+r.Header.Get("X-ScoutJobs-Timestamp"))
	}))
	defer hook.Close()
	os.WriteFile(filepath.Join(home, "config.yaml"), []byte("webhook_url: "+hook.URL+"\nwebhook_secret: s3cret\nsearch_queries: go, rust\nsearch_boards: [greenhouse:acme]\ntheme: light\n"), 0o600)
	cfg := LoadConfig(home)
	if len(cfg.SearchQueries) != 2 || cfg.SearchBoards[0] != "greenhouse:acme" || cfg.Theme != "light" || cfg.WebhookURL != hook.URL {
		t.Errorf("config: %+v", cfg)
	}

	imp := filepath.Join(home, "in.jsonl")
	os.WriteFile(imp, []byte(`{"company":"Acme","title":"Go Dev","url":"https://a","remote":"remote","source":"remotive"}`+"\n"+`{"company":"Acme","title":"Go Dev"}`+"\n"+`{"company":"","title":"skip me"}`+"\n"), 0o600)
	out := run(t, "job", "import", "--file", imp, "--json")
	if !strings.Contains(out, `"created": 1`) || !strings.Contains(out, `"existing": 1`) {
		t.Errorf("import: %s", out)
	}
	if len(hits) != 1 || !strings.Contains(hits[0], `"event":"added"`) || !strings.Contains(hits[0], `"company":"Acme"`) {
		t.Errorf("webhook after import: %v", hits)
	}
	sig, ts, _ := strings.Cut(sigs[0], " ")
	if ts == "" || sig != "sha256="+webhookSignature("s3cret", ts, []byte(hits[0])) {
		t.Errorf("webhook signature: %q", sigs[0])
	}

	os.WriteFile(filepath.Join(home, "cv", "resume.md"), []byte("# Me"), 0o600)
	out = run(t, "cv", "files", "--json")
	if !strings.Contains(out, `"resume.md"`) || strings.Contains(out, "README") {
		t.Errorf("cv files: %s", out)
	}
	run(t, "cv", "log", "--file", "resume.md", "--score", "72", "--ats", "64", "--summary", "ok", "--gaps", "add k8s,quantify", "--job", "1")
	out = run(t, "cv", "list", "--json")
	if !strings.Contains(out, `"ats_score": 64`) || !strings.Contains(out, `"job_id": 1`) {
		t.Errorf("cv list: %s", out)
	}
	if len(hits) != 2 || !strings.Contains(hits[1], `"event":"cv_check"`) {
		t.Errorf("webhook after cv log: %v", hits)
	}

	out = run(t, "job", "list", "--json", "--compact")
	if strings.Contains(out, "fit_notes") || !strings.Contains(out, `"company": "Acme"`) {
		t.Errorf("compact list: %s", out)
	}
	out = run(t, "brief", "--compact")
	if strings.Contains(out, "recent_activity") || !strings.Contains(out, `"target_week"`) {
		t.Errorf("compact brief: %s", out)
	}
}

func TestEmailDraftAndSend(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SCOUTJOBS_HOME", home)
	run(t, "init")
	run(t, "job", "add", "--company", "Acme", "--title", "Go Dev", "--contact", "jobs@acme.test")
	if err := Run([]string{"email", "draft", "1"}, io.Discard); err == nil || !strings.Contains(err.Error(), "cover letter") {
		t.Errorf("draft without cover letter should fail: %v", err)
	}
	dir := filepath.Join(home, "applications", "acme_go-dev")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "cover-letter.md"), []byte("Dear team,\nhire me."), 0o600)
	os.WriteFile(filepath.Join(dir, "resume.md"), []byte("# Me"), 0o600)
	os.WriteFile(filepath.Join(home, "config.yaml"), []byte("email_from: me@x.test\nsmtp_host: 127.0.0.1:1\n"), 0o600)
	out := run(t, "email", "draft", "1", "--json")
	if !strings.Contains(out, `"to": "jobs@acme.test"`) || !strings.Contains(out, `"subject": "Application: Go Dev at Acme"`) || !strings.Contains(out, "resume.md") {
		t.Errorf("draft: %s", out)
	}
	for _, f := range []string{"email.json", "email.eml"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("draft did not write %s", f)
		}
	}
	if err := Run([]string{"email", "send", "1"}, io.Discard); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Errorf("send without --yes must not send: %v", err)
	}
	if err := Run([]string{"email", "send", "1", "--yes"}, io.Discard); err == nil || !strings.Contains(err.Error(), "unresolved failure") || !strings.Contains(err.Error(), "grounding") {
		t.Errorf("send must stop on failed gates: %v", err)
	}
	if err := Run([]string{"email", "send", "1", "--yes", "--skip-checks"}, io.Discard); err == nil || !strings.Contains(err.Error(), "SCOUTJOBS_SMTP_PASSWORD") {
		t.Errorf("send without password: %v", err)
	}
	out = run(t, "job", "show", "1", "--json")
	if !strings.Contains(out, `"status": "new"`) || !strings.Contains(out, "email:draft") || strings.Contains(out, "email:sent") || !strings.Contains(out, "check:skipped") {
		t.Errorf("job after failed send: %s", out)
	}
	// a local PDF is preparation: it is written with a warning, and sending stays blocked
	if out := run(t, "cv", "export", "1"); !strings.Contains(out, "sending stays blocked") || !strings.Contains(out, "reviewer") {
		t.Errorf("export before a reviewer verdict: %s", out)
	}
	run(t, "job", "review", "1", "--verdict", "send", "--notes", "all claims in profile")
	out = run(t, "cv", "export", "1", "--json")
	if !strings.Contains(out, `resume.pdf`) {
		t.Errorf("cv export: %s", out)
	}
	if pdf, err := os.ReadFile(filepath.Join(dir, "resume.pdf")); err != nil || !strings.HasPrefix(string(pdf), "%PDF-") {
		t.Errorf("resume.pdf not written: %v", err)
	}
	if err := Run([]string{"cv", "export"}, io.Discard); err == nil || !strings.Contains(err.Error(), "resume.md") {
		t.Errorf("master export without cv/resume.md: %v", err)
	}
	os.WriteFile(filepath.Join(home, "cv", "resume.md"), []byte("# Me\n- Go"), 0o600)
	if out = run(t, "cv", "export"); !strings.Contains(out, filepath.Join("cv", "resume.pdf")) {
		t.Errorf("master export: %s", out)
	}
	run(t, "job", "set", "1", "--contact", "hr@acme.test")
	if out = run(t, "job", "show", "1", "--json"); !strings.Contains(out, `"contact": "hr@acme.test"`) {
		t.Errorf("contact not stored: %s", out)
	}
}

func TestDoctor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SCOUTJOBS_HOME", home)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SCOUTJOBS_SMTP_PASSWORD", "")
	run(t, "init")
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte("smtp_host: mail.test:587\nemail_from: me@test\nsmtp_hots: typo\ncurrency: EUR\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := run(t, "doctor")
	for _, want := range []string{"data dir", "unknown keys ignored: smtp_hots", "SCOUTJOBS_SMTP_PASSWORD not in the environment", "still the template", "no agent has the skills", "checks,"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor output lacks %q:\n%s", want, out)
		}
	}
	js := run(t, "doctor", "--json")
	if !strings.Contains(js, `"fails":`) || !strings.Contains(js, `"item": "smtp"`) {
		t.Errorf("json: %s", js)
	}
}

func TestDoctorOnline(t *testing.T) {
	t.Setenv("SCOUTJOBS_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"tag_name":"v9.9.0","html_url":"https://example.test/v9.9.0"}`)
	}))
	defer gh.Close()
	oldURL, oldVersion := releaseURL, Version
	releaseURL, Version = gh.URL, "0.1.0"
	defer func() { releaseURL, Version = oldURL, oldVersion }()

	out := run(t, "doctor", "--online")
	if !strings.Contains(out, "running 0.1.0, latest is v9.9.0") || !strings.Contains(out, "https://example.test/v9.9.0") {
		t.Errorf("update check missing:\n%s", out)
	}
	if strings.Contains(run(t, "doctor"), "latest is") {
		t.Error("doctor without --online must not check for updates")
	}
	Version = "9.9.0"
	if out := run(t, "doctor", "--online"); !strings.Contains(out, "ok    update         latest is v9.9.0") {
		t.Errorf("up to date not reported:\n%s", out)
	}
}

func TestSearchCommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SCOUTJOBS_HOME", home)
	run(t, "init")
	// init writes search_remote: true; the test must never reach the public boards
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte("search_remote: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<?xml version="1.0"?><rss version="2.0"><channel><title>Jobs</title><item><title>Go Engineer at Zed</title><link>https://zed.test/1</link><description>Go and k8s</description></item><item><title>Rust Dev at Ferris</title><link>https://ferris.test/2</link></item></channel></rss>`)
	}))
	defer feed.Close()
	if err := Run([]string{"search", "--query", "go"}, io.Discard); err == nil || !strings.Contains(err.Error(), "nothing to search") {
		t.Errorf("search without a source: %v", err)
	}
	out := run(t, "search", "--query", "go", "--rss", feed.URL+"/feed.xml")
	if !strings.Contains(out, "Go Engineer") || !strings.Contains(out, "Zed") || strings.Contains(out, "Rust Dev") || !strings.Contains(out, "1 postings") {
		t.Errorf("table: %s", out)
	}
	out = run(t, "search", "--rss", feed.URL+"/feed.xml", "--add", "--json", "--compact")
	if strings.Count(out, `"created":true`) != 2 || strings.Contains(out, "Go and k8s") {
		t.Errorf("json add: %s", out)
	}
	out = run(t, "search", "--rss", feed.URL+"/feed.xml", "--add")
	if !strings.Contains(out, "2 postings, 0 added") || !strings.Contains(out, "1=") {
		t.Errorf("rerun marks tracked rows: %s", out)
	}
	if jobs := run(t, "job", "list"); !strings.Contains(jobs, "Zed") {
		t.Errorf("added jobs missing: %s", jobs)
	}
}

func TestStatsAndExport(t *testing.T) {
	t.Setenv("SCOUTJOBS_HOME", t.TempDir())
	run(t, "init")
	run(t, "job", "add", "--company", "Acme", "--title", "Go Dev")
	run(t, "job", "set", "1", "--status", "applied")
	out := run(t, "stats")
	for _, want := range []string{"jobs: 1 total, 1 open", "applied         1", "applied this week: 1"} {
		if !strings.Contains(out, want) {
			t.Errorf("stats lacks %q:\n%s", want, out)
		}
	}
	if js := run(t, "stats", "--json"); !strings.Contains(js, `"total": 1`) {
		t.Errorf("stats json: %s", js)
	}
	var dump map[string]any
	if err := json.Unmarshal([]byte(run(t, "export")), &dump); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"jobs", "events", "decisions", "preferences", "interviews", "sessions", "learning"} {
		if _, ok := dump[k]; !ok {
			t.Errorf("export lacks %s", k)
		}
	}
	if jobs, _ := dump["jobs"].([]any); len(jobs) != 1 {
		t.Errorf("export jobs: %v", dump["jobs"])
	}
}

func TestEmailTestUnreachableSMTP(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SCOUTJOBS_HOME", home)
	t.Setenv("SCOUTJOBS_SMTP_PASSWORD", "")
	run(t, "init")
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte("email_from: me@x.test\nsmtp_host: 127.0.0.1:1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := Run([]string{"email", "test"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "smtp 127.0.0.1:1") || !strings.Contains(err.Error(), "password: ") {
		t.Errorf("unreachable smtp: %v", err)
	}
}

func TestJobFitReviewAndCheck(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SCOUTJOBS_HOME", home)
	run(t, "init")
	run(t, "job", "add", "--company", "Acme", "--title", "Go Dev", "--contact", "jobs@acme.test")
	review := `{"requirements":[{"requirement":"5+ years Go","kind":"must","evidence":"profile.md: Go since 2018","strength":"direct","confidence":"high","wording":"six years of Go"},{"requirement":"Rust","kind":"nice","strength":"gap"}],"vetoes":[{"rule":"work permit","reason":"EU passport required"}],"score":70}`
	if err := Run([]string{"job", "fit", "1", "--review", `{"requirements":[{"requirement":"x","kind":"must","strength":"maybe"}],"score":1}`}, io.Discard); err == nil || !strings.Contains(err.Error(), "strength") {
		t.Errorf("bad review must fail: %v", err)
	}
	out := run(t, "job", "fit", "1", "--review", review)
	if !strings.Contains(out, "VETO: work permit: EU passport required") || !strings.Contains(out, "fit 70") {
		t.Errorf("fit: %s", out)
	}
	if _, err := os.Stat(filepath.Join(home, "applications", "acme_go-dev", "fit-review.json")); err != nil {
		t.Error("fit-review.json not written")
	}
	out = run(t, "job", "show", "1", "--json")
	if !strings.Contains(out, `"fit_score": 70`) || !strings.Contains(out, `"veto": "work permit: EU passport required"`) || !strings.Contains(out, "fit:review") || !strings.Contains(out, "1 direct, 1 gap") {
		t.Errorf("job after fit: %s", out)
	}
	if out = run(t, "job", "list"); !strings.Contains(out, "veto") {
		t.Errorf("list hides the veto: %s", out)
	}
	if err := Run([]string{"job", "check", "1"}, io.Discard); err == nil || !strings.Contains(err.Error(), "fit_review: hard veto") {
		t.Errorf("check with veto: %v", err)
	}
	run(t, "job", "set", "1", "--veto", "")
	out = run(t, "job", "show", "1", "--json")
	if !strings.Contains(out, `"veto": ""`) || !strings.Contains(out, "veto:cleared") {
		t.Errorf("clear veto: %s", out)
	}
	dir := filepath.Join(home, "applications", "acme_go-dev")
	body := strings.Repeat("Built Go services with Postgres and shipped them weekly. ", 5)
	os.WriteFile(filepath.Join(dir, "resume.md"), []byte("# Me\n\nSix years of Go.\n\n"+body), 0o600)
	os.WriteFile(filepath.Join(dir, "cover-letter.md"), []byte("Dear team,\n\n"+body), 0o600)
	os.WriteFile(filepath.Join(dir, "review.json"), []byte(strings.Replace(review, `"vetoes":[{"rule":"work permit","reason":"EU passport required"}],`, "", 1)), 0o600)
	if err := Run([]string{"job", "fit", "1", "--file", filepath.Join(dir, "review.json")}, io.Discard); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(dir, "review.json"))
	if err := Run([]string{"job", "review", "1", "--verdict", "maybe"}, io.Discard); err == nil {
		t.Error("verdict must be send or fix")
	}
	run(t, "job", "review", "1", "--verdict", "send", "--notes", "all claims in profile")
	out = run(t, "job", "check", "1", "--json")
	if !strings.Contains(out, `"ok": true`) {
		t.Errorf("check: %s", out)
	}
	out = run(t, "job", "check", "1")
	if !strings.Contains(out, "PASS  grounding") || !strings.Contains(out, "WARN  ats_text") {
		t.Errorf("check text: %s", out)
	}
}

func TestPrefReviewCommands(t *testing.T) {
	t.Setenv("SCOUTJOBS_HOME", t.TempDir())
	run(t, "init")
	run(t, "pref", "propose", "stack_avoid", "java", "--support", "5", "--evidence", "5 skips mention Java")
	if out := run(t, "pref", "review"); !strings.Contains(out, "stack_avoid") || !strings.Contains(out, "5/0") {
		t.Errorf("review: %s", out)
	}
	if out := run(t, "pref", "confirm", "stack_avoid"); !strings.Contains(out, "learned") {
		t.Errorf("confirm: %s", out)
	}
	var b bytes.Buffer
	if err := Run([]string{"pref", "reject", "stack_avoid"}, &b); err == nil {
		t.Error("rejected a proposal that is no longer pending")
	}
}

// Every key the config template offers, set or commented out, must be one doctor knows (#27).
func TestDoctorKnowsEveryTemplateKey(t *testing.T) {
	key := regexp.MustCompile(`^#? ?([a-z_]+):`)
	for _, line := range strings.Split(configTemplate, "\n") {
		if m := key.FindStringSubmatch(line); m != nil && !configKeys[m[1]] {
			t.Errorf("doctor would call %q unknown", m[1])
		}
	}
}
