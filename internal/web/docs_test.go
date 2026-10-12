package web

import (
	"archive/zip"
	"context"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	if !strings.Contains(page, `id="docs"`) || !strings.Contains(page, "Not written yet") {
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
	h.ServeHTTP(page, httptest.NewRequest("GET", "/jobs/1?tab=mail", nil))
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
	// the queue may not have started the run yet; it must not outlive the temp dir
	for i := 0; i < 300 && (m.Latest(1) == nil || len(m.Waiting()) > 0); i++ {
		time.Sleep(10 * time.Millisecond)
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
	list := page[strings.Index(page, `id="checklist"`):]
	list = list[:strings.Index(list, "</section>")]
	for _, want := range []string{
		`<li class="done"><span class="tick" aria-hidden="true"></span><div><b>Tailored resume</b>`,
		`<li class="next"><span class="tick" aria-hidden="true"></span><div><b>Cover letter</b>`,
		`<li class="warn"><span class="tick" aria-hidden="true"></span><div><b>Draft review</b><small title="drop the Kafka claim">drop the Kafka claim`,
		`<li class="blocked"><span class="tick" aria-hidden="true"></span><div><b>Final checks</b><small title="to fix`,
	} {
		if !strings.Contains(list, want) {
			t.Errorf("checklist lacks %q:\n%s", want, list)
		}
	}
	if strings.Count(list, `class="next"`)+strings.Count(list, `class="ns next"`) != 1 {
		t.Error("exactly one item is next")
	}
	if !strings.Contains(page, `<p class="navh">Apply</p>`) || !strings.Contains(page, "Find jobs") {
		t.Error("navigation is not grouped")
	}
}

func TestProceedWithGapsFlow(t *testing.T) {
	s := testStore(t)
	h, err := Handler(s, Config{FollowUpDays: 10, StaleDays: 60, WeeklyApplyTarget: 5})
	if err != nil {
		t.Fatal(err)
	}
	j, _ := s.JobByID(1)
	dir := apply.Dir(s, j)
	body := strings.Repeat("Shipped services in Go with Postgres behind them. ", 6)
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "resume.md"), []byte("# Ada\n\n"+body), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "cover-letter.md"), []byte("Dear team,\n\n"+body), 0o600)
	review := apply.FitReview{Score: 60, Requirements: []apply.Requirement{{Requirement: "Commissioning and handover", Kind: "must", Strength: "gap"}}}
	if _, err := apply.SaveReview(dir, review); err != nil {
		t.Fatal(err)
	}
	get := func() string {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/jobs/1", nil))
		return rec.Body.String()
	}
	post := func(path, form string) int {
		req := httptest.NewRequest("POST", path, strings.NewReader(form))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	page := get()
	if !strings.Contains(page, "1 must-have gap remains. Your documents do not claim this experience.") || !strings.Contains(page, `name="gap" value="Commissioning and handover" required`) {
		t.Fatal("no acknowledgement form")
	}
	post("/jobs/1/acknowledge", "notes=adjacent")
	if ev, _ := s.Events(1, 20); hasKind(ev, apply.AckEvent) {
		t.Fatal("acknowledged without ticking the gap")
	}
	if code := post("/jobs/1/acknowledge", "gap="+url.QueryEscape("Commissioning and handover")+"&notes=adjacent"); code != 303 {
		t.Fatalf("acknowledge %d", code)
	}
	page = get()
	if !strings.Contains(page, "Finish the application first") || strings.Contains(page, "Proceed with gaps</button>") {
		t.Error("other failing gates must hold the portal link back")
	}
	if !strings.Contains(page, "Sending is blocked") {
		t.Error("e-mail sending stays blocked")
	}
	if code := post("/jobs/1/applied", "note=ref+42"); code != 303 {
		t.Fatalf("applied %d", code)
	}
	if got, _ := s.JobByID(1); got.Status != "applied" {
		t.Errorf("status %s", got.Status)
	}
}

func hasKind(events []store.Event, kind string) bool {
	for _, e := range events {
		if e.Kind == kind {
			return true
		}
	}
	return false
}

func TestJobPageShowsProvenanceAndStaleResults(t *testing.T) {
	s := testStore(t)
	m := runner.NewManager(&runner.Runner{Store: s, Exe: "scoutjobs", Custom: "/bin/true"})
	h, _ := Handler(s, Config{FollowUpDays: 10, StaleDays: 60, WeeklyApplyTarget: 5, Runner: m, APIToken: "secret"})
	j, _ := s.JobByID(1)
	dir := apply.Dir(s, j)
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "resume.md"), []byte("# Jane\n\n"+strings.Repeat("Go services. ", 40)), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "cover-letter.md"), []byte("Dear team, "+strings.Repeat("I build Go services. ", 20)), 0o600)
	// the agent left its verdict but the run stopped before recording it: the page load records it
	_ = os.WriteFile(filepath.Join(dir, apply.ReviewResult), []byte(`{"verdict":"send","notes":"all claims in profile.md"}`), 0o600)
	page := func() string {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/jobs/1", nil))
		return rec.Body.String()
	}
	b := page()
	sum := apply.Short(apply.LoadProvenance(dir).Review.Files["resume.md"])
	if !strings.Contains(b, "all claims in profile.md") || !strings.Contains(b, "resume.md "+sum) {
		t.Fatalf("recorded review and its hashes: %s", b)
	}
	_ = os.WriteFile(filepath.Join(dir, "resume.pdf"), []byte("%PDF-1.7 the styled final CV"), 0o600)
	if b := page(); !strings.Contains(b, "stale, changed since the review: resume.pdf") || !strings.Contains(b, "stale: changed since the review: resume.pdf") {
		t.Errorf("a replaced final file must reopen the review: %s", b)
	}
	req := httptest.NewRequest("GET", "/api/jobs/1", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if b := rec.Body.String(); !strings.Contains(b, `"review_changed":["resume.pdf"]`) || !strings.Contains(b, `"resume.md":"`) {
		t.Errorf("api provenance: %s", b)
	}
	if j, _ := s.JobByID(1); j.Status == "applied" {
		t.Error("reconciling never marks a job applied")
	}
}

func TestJobPageShowsTheCoverLetterTemplateCheck(t *testing.T) {
	s := testStore(t)
	t.Setenv("SCOUTJOBS_SOFFICE", "off")
	h, _ := Handler(s, Config{FollowUpDays: 10, StaleDays: 60, WeeklyApplyTarget: 5})
	j, _ := s.JobByID(1)
	dir := apply.Dir(s, j)
	_ = os.MkdirAll(dir, 0o755)
	tmpl := filepath.Join(s.Home, "cv", "letter.docx")
	_ = os.MkdirAll(filepath.Dir(tmpl), 0o755)
	f, _ := os.Create(tmpl)
	w := zip.NewWriter(f)
	fw, _ := w.Create("word/document.xml")
	fw.Write([]byte(`<w:document xmlns:w="w"><w:body><w:p><w:r><w:t xml:space="preserve">Dear {{salutation}},</w:t></w:r></w:p><w:p><w:r><w:t>{{body_paragraph_1}}</w:t></w:r></w:p></w:body></w:document>`))
	w.Close()
	f.Close()
	_ = os.WriteFile(filepath.Join(dir, apply.SlotsName), []byte(`{"salutation":"Hiring Team","body_paragraph_1":"`+strings.Repeat("I build Go services. ", 20)+`"}`), 0o600)
	if _, err := apply.BuildCover(context.Background(), s, j, tmpl); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/jobs/1", nil))
	b := rec.Body.String()
	for _, want := range []string{"Cover letter built from <code>letter.docx</code>", "template warn", "install LibreOffice", "Cover letter DOCX", "/download/cover-letter.docx"} {
		if !strings.Contains(b, want) {
			t.Errorf("job page lacks %q", want)
		}
	}
	if strings.Contains(b, `/files/cover-letter.docx"`) {
		t.Error("a DOCX has no preview")
	}
	_ = os.WriteFile(filepath.Join(dir, "cover-letter.md"), []byte("Dear team, edited by hand"), 0o600)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/jobs/1", nil))
	if b := rec.Body.String(); !strings.Contains(b, "stale, changed after the build: cover-letter.md") {
		t.Errorf("an edited letter no longer matches its DOCX: %s", b)
	}
}

func TestCVGapsKeepsCommasInsideFindings(t *testing.T) {
	in := "Structural: add email, phone and URLs, Tailor: lead with systems (Parcelo, Ledgerly) end to end, Profile gap (needs the user, do not invent): no contract work"
	got := cvGaps(in)
	want := []string{
		"Structural: add email, phone and URLs",
		"Tailor: lead with systems (Parcelo, Ledgerly) end to end",
		"Profile gap (needs the user, do not invent): no contract work",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("cvGaps = %q, want %q", got, want)
	}
	if cvGaps("  ") != nil {
		t.Fatal("blank gaps should be nil")
	}
}

func TestMarkdownNumberedList(t *testing.T) {
	got := string(mdHTML("Next week\n1. Apply to 4 jobs\n2) Two mock sessions\n- a bullet"))
	want := "<p>Next week</p>\n<ol>\n<li>Apply to 4 jobs</li>\n<li>Two mock sessions</li>\n</ol>\n<ul>\n<li>a bullet</li>\n</ul>\n"
	if got != want {
		t.Errorf("mdHTML = %q", got)
	}
}
