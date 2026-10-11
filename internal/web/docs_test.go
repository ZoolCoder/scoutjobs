package web

import (
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zoolcoder/scoutjobs/internal/apply"
	"github.com/zoolcoder/scoutjobs/internal/runner"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

func TestDocumentsPreviewAndDownload(t *testing.T) {
	s := testStore(t)
	h, err := Handler(s, Config{FollowUpDays: 10, StaleDays: 60, WeeklyApplyTarget: 5})
	if err != nil {
		t.Fatal(err)
	}
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		return rec
	}
	page := get("/jobs/1").Body.String()
	if !strings.Contains(page, `id="docs"`) || !strings.Contains(page, "not written yet") {
		t.Fatalf("documents panel missing: %s", page)
	}
	if strings.Contains(page, `href="/jobs/1/files/resume.md"`) {
		t.Error("a missing file must not be a link")
	}
	dir := filepath.Join(s.Home, "applications", "acme_go-engineer")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "resume.md"), []byte("# Ada\n\n## Experience\n- Built **Go** services <script>x</script>\n"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "resume.pdf"), []byte("%PDF-1.4"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "notes.json"), []byte(`{"a":1}`), 0o600)

	if page := get("/jobs/1").Body.String(); !strings.Contains(page, `href="/jobs/1/files/resume.md"`) || !strings.Contains(page, `href="/jobs/1/download/resume.pdf"`) {
		t.Errorf("existing files should link to preview and download")
	}
	md := get("/jobs/1/files/resume.md").Body.String()
	if !strings.Contains(md, `<article class="paper">`) || !strings.Contains(md, "<li>Built <b>Go</b> services &lt;script&gt;") || strings.Contains(md, "<textarea") {
		t.Errorf("markdown preview: %s", md)
	}
	if !strings.Contains(get("/jobs/1/files/resume.md?edit=1").Body.String(), "<textarea") {
		t.Error("edit view lacks the editor")
	}
	if pdf := get("/jobs/1/files/resume.pdf").Body.String(); !strings.Contains(pdf, `<iframe class="pdfview" src="/jobs/1/view/resume.pdf"`) {
		t.Errorf("pdf preview: %s", pdf)
	}
	if rec := get("/jobs/1/view/resume.pdf"); rec.Code != 200 || rec.Header().Get("Content-Type") != "application/pdf" || rec.Header().Get("Content-Security-Policy") != "" {
		t.Errorf("inline pdf: %d %v", rec.Code, rec.Header())
	}
	if rec := get("/jobs/1/view/resume.md"); rec.Code != 404 {
		t.Errorf("view serves only pdf: %d", rec.Code)
	}
	if rec := get("/jobs/1/files/nothere.md"); rec.Code != 404 {
		t.Errorf("missing file: %d", rec.Code)
	}
	if rec := get("/jobs/1/download/resume.md"); rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Disposition"), "attachment") {
		t.Errorf("download md: %d %v", rec.Code, rec.Header())
	}
	for _, p := range []string{"/jobs/1/download/notes.json", "/jobs/1/download/..%2F..%2Fscoutjobs.db"} {
		if rec := get(p); rec.Code != 404 {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}
	if j := get("/jobs/1/files/notes.json").Body.String(); strings.Contains(j, "/download/notes.json") {
		t.Error("json must not offer a download")
	}

	// main CV: missing is not a link, written one previews and saves
	if p := get("/profile").Body.String(); strings.Contains(p, `href="/profile/files/resume.md"`) {
		t.Error("missing main CV must not be a link")
	}
	_ = os.MkdirAll(filepath.Join(s.Home, "cv"), 0o755)
	_ = os.WriteFile(filepath.Join(s.Home, "cv", "resume.md"), []byte("# Ada"), 0o600)
	if p := get("/profile").Body.String(); !strings.Contains(p, `href="/profile/files/resume.md"`) {
		t.Error("main CV should link to its preview")
	}
	if p := get("/profile/files/resume.md").Body.String(); !strings.Contains(p, "<h1>Ada</h1>") {
		t.Errorf("cv preview: %s", p)
	}
	if rec := post(h, "/profile/files/resume.md", url.Values{"body": {"# Ada Lovelace"}}); rec.Code != 303 {
		t.Errorf("save main cv: %d", rec.Code)
	}
	if b, _ := os.ReadFile(filepath.Join(s.Home, "cv", "resume.md")); string(b) != "# Ada Lovelace" {
		t.Errorf("main cv on disk: %q", b)
	}
	if rec := post(h, "/profile/files/profile.md", url.Values{"body": {"x"}}); rec.Code != 404 {
		t.Errorf("only the main documents are writable here: %d", rec.Code)
	}
}

func TestProposedEmails(t *testing.T) {
	s := testStore(t)
	var drafted string
	h, _ := Handler(s, Config{FollowUpDays: 10, StaleDays: 60, WeeklyApplyTarget: 5, Mail: MailHooks{Draft: func(id int64, kind string) (string, error) {
		drafted = kind
		return "https://mail.google.com/mail/#drafts/1", nil
	}}})
	st := "offer"
	_ = s.UpdateJob(1, store.JobPatch{Status: &st})
	page := httptest.NewRecorder()
	h.ServeHTTP(page, httptest.NewRequest("GET", "/jobs/1", nil))
	if b := page.Body.String(); !strings.Contains(b, `id="emails"`) || !strings.Contains(b, "/jobs/1/emails/offer-accept") {
		t.Fatalf("offer replies missing: %s", b)
	}
	form := url.Values{"to": {"hr@acme.test"}, "subject": {"Re: offer"}, "body": {"Thank you, I accept."}}
	if rec := post(h, "/jobs/1/emails/offer-accept", form); rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "msg=") {
		t.Errorf("save: %d %v", rec.Code, rec.Header())
	}
	j, _ := s.JobByID(1)
	if m, err := apply.LoadProposal(apply.Dir(s, j), "offer-accept"); err != nil || m.Body != "Thank you, I accept." {
		t.Errorf("saved proposal: %+v %v", m, err)
	}
	form.Set("action", "gmail")
	if rec := post(h, "/jobs/1/emails/offer-accept", form); drafted != "offer-accept" || !strings.Contains(rec.Header().Get("Location"), "drafted=") {
		t.Errorf("gmail draft: %q %v", drafted, rec.Header())
	}
	req := httptest.NewRequest("POST", "/jobs/1/emails/offer-accept", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Errorf("cross-origin save: %d", rec.Code)
	}
	if rec := post(h, "/jobs/1/emails/nope", form); !strings.Contains(rec.Header().Get("Location"), "err=") {
		t.Errorf("unknown kind: %v", rec.Header())
	}
	if gmailLink("javascript:alert(1)") != "" || gmailLink("https://mail.google.com/x") == "" {
		t.Error("gmailLink filter")
	}
}

func TestPrepareAllAndAutoPrepare(t *testing.T) {
	s := testStore(t)
	agent := filepath.Join(s.Home, "agent.sh")
	_ = os.WriteFile(agent, []byte("#!/bin/sh\ncat >/dev/null\nsleep 1\n"), 0o700)
	m := runner.NewManager(&runner.Runner{Store: s, Exe: "scoutjobs", Custom: agent})
	h, _ := Handler(s, Config{FollowUpDays: 10, StaleDays: 60, WeeklyApplyTarget: 5, Runner: m, FitApplyFloor: 60})
	p := httptest.NewRecorder()
	h.ServeHTTP(p, httptest.NewRequest("GET", "/profile", nil))
	if b := p.Body.String(); !strings.Contains(b, "Prepare all (1)") || !strings.Contains(b, "Turn auto-prepare on") {
		t.Fatalf("profile main panel: %s", b)
	}
	if rec := post(h, "/settings/auto-prepare", url.Values{"on": {"1"}}); rec.Code != 303 || s.Setting("auto_prepare") != "on" {
		t.Errorf("auto-prepare on: %d %q", rec.Code, s.Setting("auto_prepare"))
	}
	if rec := post(h, "/jobs/prepare-all", url.Values{"back": {"/profile"}}); rec.Code != 303 {
		t.Errorf("prepare all: %d", rec.Code)
	}
	page := httptest.NewRecorder()
	h.ServeHTTP(page, httptest.NewRequest("GET", "/jobs/1", nil))
	if b := page.Body.String(); !strings.Contains(b, "data-run") || !strings.Contains(b, "<progress") {
		t.Errorf("job page should show live progress: %s", b)
	}
	if run := m.Latest(1); run != nil {
		m.Cancel(run.ID)
		run.Wait()
	}
}

func TestRunFormHidesFinishedSteps(t *testing.T) {
	s := testStore(t)
	m := runner.NewManager(&runner.Runner{Store: s, Exe: "scoutjobs", Custom: "/bin/true"})
	h, _ := Handler(s, Config{FollowUpDays: 10, StaleDays: 60, WeeklyApplyTarget: 5, Runner: m})
	dir := filepath.Join(s.Home, "applications", "acme_go-engineer")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "resume.md"), []byte(strings.Repeat("Go services. ", 40)), 0o600)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/jobs/1", nil))
	b := rec.Body.String()
	if strings.Contains(b, `name="step" value="tailor"`) || !strings.Contains(b, `name="redo" value="tailor"`) || !strings.Contains(b, "Already done: Tailored resume") {
		t.Errorf("finished tailor step should only be offered to redo: %s", b)
	}
	if !strings.Contains(b, `name="step" value="cover"`) {
		t.Error("pending cover step missing")
	}
}

func TestJobChecklistAndGroupedNav(t *testing.T) {
	s := testStore(t)
	h, err := Handler(s, Config{FollowUpDays: 10, StaleDays: 60, WeeklyApplyTarget: 5})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(s.Home, "applications", "acme_go-engineer")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "resume.md"), []byte("# Ada\n"), 0o600)
	_ = s.AddEvent(1, "review:fix", "drop the Kafka claim")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/jobs/1", nil))
	page := rec.Body.String()
	list := page[strings.Index(page, `<ol class="checklist">`):]
	list = list[:strings.Index(list, "</ol>")]
	for _, want := range []string{
		`<li class="done"><span class="tick" aria-hidden="true"></span><div><b>Tailored resume</b>`,
		`<li class="next"><span class="tick" aria-hidden="true"></span><div><b>Cover letter</b>`,
		`<li class="warn"><span class="tick" aria-hidden="true"></span><div><b>Draft review</b><small title="drop the Kafka claim">drop the Kafka claim`,
		`<li class="blocked"><span class="tick" aria-hidden="true"></span><div><b>Quality gates</b><small title="sending blocked`,
	} {
		if !strings.Contains(list, want) {
			t.Errorf("checklist lacks %q:\n%s", want, list)
		}
	}
	if strings.Count(list, `class="next"`) != 1 {
		t.Error("exactly one item is next")
	}
	if !strings.Contains(page, `<p class="navh">Apply</p>`) || !strings.Contains(page, "Find jobs") {
		t.Error("navigation is not grouped")
	}
}
