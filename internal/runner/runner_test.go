package runner

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/apply"
	"github.com/zoolcoder/scoutjobs/internal/search"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

// fakeAgent is a custom agent_command: it keeps the prompt and writes the drafts the prompt asks for.
const fakeAgent = `#!/bin/sh
cat > prompt.txt
grep -q "Step [0-9], tailored resume" prompt.txt && printf '# Jane Doe\n\nGo engineer with eight years of backend work, Postgres, Kubernetes and distributed systems at scale.\n%.0s' 1 2 3 > resume.md
grep -q "Step [0-9], cover letter" prompt.txt && printf 'Dear hiring team,\n\nI build reliable Go services and would like to bring that to Acme as your next backend engineer.\n%.0s' 1 2 3 > cover-letter.md
echo "wrote the drafts"
`

func setup(t *testing.T, script string) (*Runner, store.Job) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("SCOUTJOBS_HOME", home)
	s, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	j, _, err := s.UpsertJob(store.Job{Company: "Acme", Title: "Go Engineer"})
	if err != nil {
		t.Fatal(err)
	}
	agent := filepath.Join(home, "agent.sh")
	if err := os.WriteFile(agent, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return &Runner{Store: s, Exe: "scoutjobs", Custom: agent, Agent: "custom"}, j
}

func run(t *testing.T, rn *Runner, j store.Job, req Request) Status {
	t.Helper()
	r, ag, err := rn.Plan(j, req)
	if err != nil {
		t.Fatal(err)
	}
	rn.Exec(context.Background(), r, ag, j, nil)
	return r.Snapshot()
}

func states(r Status) string {
	var out []string
	for _, s := range r.Steps {
		out = append(out, s.Name+"="+s.State)
	}
	return strings.Join(out, " ")
}

func TestRunWritesDraftsAndSkipsThemNextTime(t *testing.T) {
	rn, j := setup(t, fakeAgent)
	got := run(t, rn, j, Request{Steps: []string{"tailor", "cover"}})
	if got.State != Done || states(got) != "tailor=done cover=done" || got.Agent != "custom" {
		t.Fatalf("%s %s %s %v", got.State, got.Error, states(got), got.Log)
	}
	if !strings.Contains(strings.Join(got.Log, "\n"), "wrote the drafts") {
		t.Errorf("agent output not logged: %v", got.Log)
	}
	prompt, _ := os.ReadFile(filepath.Join(apply.Dir(rn.Store, j), "prompt.txt"))
	if !strings.Contains(string(prompt), "Never send e-mail") || !strings.Contains(string(prompt), "===== skill resume-tailor") || strings.Contains(string(prompt), "skill fit-review") {
		t.Errorf("prompt:\n%.400s", prompt)
	}
	events, _ := rn.Store.Events(j.ID, 50)
	kinds := map[string]bool{}
	for _, e := range events {
		kinds[e.Kind] = true
	}
	if !kinds["run:tailor"] || !kinds["run:cover"] || !kinds["run:done"] {
		t.Errorf("events %v", kinds)
	}
	again := run(t, rn, j, Request{Steps: []string{"tailor", "cover"}})
	if states(again) != "tailor=skipped cover=skipped" || again.Agent != "" {
		t.Errorf("not idempotent: %s agent %q", states(again), again.Agent)
	}
}

func TestGatesBlockSendingNotPreparation(t *testing.T) {
	rn, j := setup(t, fakeAgent)
	got := run(t, rn, j, Request{Steps: []string{"tailor", "cover", "export", "check", "draft"}})
	if got.State != Done || states(got) != "tailor=done cover=done export=warn check=warn draft=done" {
		t.Fatalf("%s %q %s", got.State, got.Error, states(got))
	}
	if d := got.Steps[3].Detail; !strings.Contains(d, "sending blocked") || !strings.Contains(d, "no reviewer verdict") {
		t.Errorf("check detail %q", d)
	}
	dir := apply.Dir(rn.Store, j)
	for _, f := range []string{"resume.pdf", apply.DraftName} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s not prepared: %v", f, err)
		}
	}
	events, _ := rn.Store.Events(j.ID, 200)
	if rep := apply.Check(rn.Store, j, events, apply.CheckOptions{}); rep.OK() {
		t.Error("the send gate must still fail")
	}
}

// reviewAgent writes the review as a result file and never calls scoutjobs, like an agent that forgets.
const reviewAgent = `#!/bin/sh
cat > prompt.txt
grep -q "Step [0-9], draft review" prompt.txt && printf '{"verdict":"fix","notes":"claims a Kafka migration not in profile.md"}' > review-result.json
grep -q "Step [0-9], ats check" prompt.txt && printf '{"score":71,"ats":82,"summary":"clean","gaps":["add Terraform"]}' > ats-result.json
true
`

func TestRunnerRecordsAgentResultsAndKeepsGoing(t *testing.T) {
	rn, j := setup(t, reviewAgent)
	dir := apply.Dir(rn.Store, j)
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "resume.md"), []byte("# Jane\n\nGo engineer."), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "cover-letter.md"), []byte("Dear team, I build Go services."), 0o600)
	time.Sleep(1100 * time.Millisecond) // events and file times have second precision

	got := run(t, rn, j, Request{Steps: []string{"review", "export"}})
	if got.State != Failed || !strings.Contains(got.Error, "Kafka") || !strings.Contains(got.Error, "keep going") {
		t.Fatalf("a fix verdict stops by default: %s %q", got.State, got.Error)
	}
	got = run(t, rn, j, Request{Steps: []string{"review", "export", "ats"}, Force: true, KeepGoing: true})
	if got.State != Done || states(got) != "review=warn export=warn ats=done" {
		t.Fatalf("keep going: %s %q %s", got.State, got.Error, states(got))
	}
	checks, _ := rn.Store.CVChecks(5)
	if len(checks) != 1 || checks[0].ATSScore != 82 || checks[0].JobID != j.ID || checks[0].Gaps != "add Terraform" {
		t.Errorf("ats result not recorded: %+v", checks)
	}
}

func TestPlanMergesALegacyFolder(t *testing.T) {
	rn, j := setup(t, fakeAgent)
	dir := apply.Dir(rn.Store, j)
	legacy := filepath.Join(filepath.Dir(dir), "acme-go-engineer")
	_ = os.MkdirAll(legacy, 0o755)
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(legacy, "resume.md"), []byte("# Jane\n\nold folder"), 0o600)
	_ = os.WriteFile(filepath.Join(legacy, "notes.md"), []byte("legacy notes"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "notes.md"), []byte("newer notes"), 0o600)

	got := run(t, rn, j, Request{Steps: []string{"tailor"}})
	if states(got) != "tailor=skipped" || !strings.Contains(strings.Join(got.Log, "\n"), "acme-go-engineer/resume.md") {
		t.Fatalf("legacy resume not picked up: %s %v", states(got), got.Log)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "notes.md")); string(b) != "newer notes" {
		t.Errorf("newer file overwritten: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(legacy, "notes.md")); string(b) != "legacy notes" {
		t.Errorf("conflicting legacy file lost: %q", b)
	}
	events, _ := rn.Store.Events(j.ID, 50)
	var conflict bool
	for _, e := range events {
		conflict = conflict || e.Kind == "files:conflict"
	}
	if !conflict {
		t.Error("conflict not reported")
	}
}

func TestRunFailsWhenTheAgentWritesNothing(t *testing.T) {
	rn, j := setup(t, "#!/bin/sh\ncat >/dev/null\necho thinking\n")
	got := run(t, rn, j, Request{Steps: []string{"tailor"}})
	if got.State != Failed || !strings.Contains(got.Error, "finished without resume.md") {
		t.Fatalf("%s %q", got.State, got.Error)
	}
	rn, j = setup(t, "#!/bin/sh\ncat >/dev/null\necho 'quota exceeded' >&2\nexit 3\n")
	if got := run(t, rn, j, Request{Steps: []string{"cover"}}); !strings.Contains(got.Error, "quota exceeded") {
		t.Errorf("stderr lost: %q", got.Error)
	}
}

func TestDryRunAndCancel(t *testing.T) {
	rn, j := setup(t, "#!/bin/sh\ncat >/dev/null\nexec sleep 30\n")
	got := run(t, rn, j, Request{DryRun: true})
	if got.State != Done || len(got.Steps) != len(Steps) || !strings.Contains(strings.Join(got.Log, "\n"), "would run fit") {
		t.Errorf("dry run: %s %v", got.State, got.Log)
	}
	events, _ := rn.Store.Events(j.ID, 50)
	for _, e := range events {
		if strings.HasPrefix(e.Kind, "run:") {
			t.Errorf("dry run logged %v", e)
		}
	}
	m := NewManager(rn)
	r, err := m.Start(j, Request{Steps: []string{"tailor"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(j, Request{Steps: []string{"tailor"}}); err == nil {
		t.Error("second run of the same job started")
	}
	m.Cancel(r.ID)
	deadline := time.Now().Add(5 * time.Second)
	for r.Snapshot().State == Running && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := r.Snapshot(); got.State != "cancelled" || got.Steps[0].State != Skipped {
		t.Errorf("after cancel: %s %s", got.State, states(got))
	}
}

func TestParse(t *testing.T) {
	if all, _ := Parse("safe"); len(all) != len(Steps) {
		t.Errorf("safe = %v", all)
	}
	if got, err := Parse("tailor, cover"); err != nil || strings.Join(got, ",") != "tailor,cover" {
		t.Errorf("%v %v", got, err)
	}
	if _, err := Parse("tailor,send"); err == nil || !strings.Contains(err.Error(), "unknown step") {
		t.Errorf("send accepted: %v", err)
	}
}

func TestReplacedFinalFileReopensReviewAndATS(t *testing.T) {
	rn, j := setup(t, "#!/bin/sh\ncat >/dev/null\n")
	dir := apply.Dir(rn.Store, j)
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "resume.md"), []byte("# Jane\n\nGo engineer."), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "cover-letter.md"), []byte("Dear team, I build Go services."), 0o600)
	_ = rn.Store.AddEvent(j.ID, "review:send", "checked")
	_ = apply.StampReview(dir)
	if _, _, err := apply.ExportPDF(dir, "resume.md", ""); err != nil {
		t.Fatal(err)
	}
	_, _ = rn.Store.AddCVCheck(store.CVCheck{File: "resume.pdf", Score: 80, ATSScore: 85, JobID: j.ID})
	_ = apply.StampATS(dir, "resume.pdf")
	if f := rn.Finished(j); f["review"] == "" || f["export"] == "" || f["ats"] == "" || len(rn.Stale(j)) > 0 {
		t.Fatalf("all current: %v %v", f, rn.Stale(j))
	}

	_ = os.WriteFile(filepath.Join(dir, "resume.pdf"), []byte("%PDF-1.7 the styled final CV"), 0o600)
	r, _, err := rn.Plan(j, Request{Steps: []string{"review", "export", "ats"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := states(r.Snapshot()); got != "review=pending export=skipped ats=pending" {
		t.Errorf("the final PDF must be reviewed and scored, never overwritten: %s", got)
	}
	stale := rn.Stale(j)
	if !strings.Contains(stale["review"], "resume.pdf") || !strings.Contains(stale["ats"], "resume.pdf") || stale["export"] != "" {
		t.Errorf("stale: %v", stale)
	}
	if !strings.Contains(rn.Finished(j)["export"], "your own file") {
		t.Errorf("export: %v", rn.Finished(j))
	}
	if p := Prompt(j, dir, "scoutjobs", rn.withSources(dir, Steps[3:4])); !strings.Contains(p, "Also review resume.pdf") {
		t.Errorf("the reviewer must read the final PDF: %s", p)
	}
}

func TestFormattedCVSourceIsNeverRenderedFromMarkdown(t *testing.T) {
	rn, j := setup(t, "#!/bin/sh\ncat >/dev/null\n")
	rn.CVSource, rn.CoverTemplate = "cv/Jane Doe CV.docx", "cv/cover-template.md"
	dir := apply.Dir(rn.Store, j)
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "resume.md"), []byte("# Jane\n\nGo engineer."), 0o600)
	p := Prompt(j, dir, "scoutjobs", rn.withSources(dir, Steps[1:3]))
	if !strings.Contains(p, "Jane Doe CV.docx: tailor from it") || !strings.Contains(p, "Do not write resume.pdf") || !strings.Contains(p, "cover-template.md: use it") {
		t.Errorf("sources not in the prompt: %s", p)
	}
	if ok, why := rn.done(j, "export"); ok || !strings.Contains(why, "Jane Doe CV.docx") {
		t.Errorf("export from a formatted source: %v %q", ok, why)
	}
	detail, warn, err := rn.local(j, "export")
	if err != nil || !warn || !strings.Contains(detail, "not rendered from Markdown") {
		t.Errorf("local export: %q %v %v", detail, warn, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "resume.pdf")); err == nil {
		t.Error("no PDF may be rendered from the Markdown draft")
	}
}

func TestWordCoverTemplateIsFilledNotRewritten(t *testing.T) {
	rn, j := setup(t, "#!/bin/sh\ncat >prompt.txt\nprintf '%s' '{\"employer_name\":\"Acme\",\"salutation\":\"Hiring Team\",\"body_paragraph_1\":\"I build payment systems in Go.\",\"signoff\":\"Kind regards\",\"candidate_name\":\"Jane Doe\"}' > cover-letter.slots.json\n")
	t.Setenv("SCOUTJOBS_SOFFICE", "off")
	rn.CoverTemplate = "cv/letter.docx"
	tmpl := filepath.Join(rn.Store.Home, rn.CoverTemplate)
	_ = os.MkdirAll(filepath.Dir(tmpl), 0o755)
	f, _ := os.Create(tmpl)
	w := zip.NewWriter(f)
	fw, _ := w.Create("word/document.xml")
	fw.Write([]byte(`<w:document xmlns:w="w"><w:body><w:p><w:r><w:t>{{employer_name}}</w:t></w:r></w:p><w:p><w:r><w:t xml:space="preserve">Dear {{salutation}},</w:t></w:r></w:p>` +
		`<w:p><w:r><w:t>{{body_paragraph_1}}</w:t></w:r></w:p><w:p><w:r><w:t>{{signoff}}</w:t></w:r></w:p><w:p><w:r><w:t>{{candidate_name}}</w:t></w:r></w:p></w:body></w:document>`))
	w.Close()
	f.Close()

	if ok, why := rn.done(j, "cover"); ok || !strings.Contains(why, "letter.docx") {
		t.Errorf("no build yet: %v %q", ok, why)
	}
	got := run(t, rn, j, Request{Steps: []string{"cover"}})
	if got.State != Done || states(got) != "cover=done" {
		t.Fatalf("%s %s %s %v", got.State, got.Error, states(got), got.Log)
	}
	dir := apply.Dir(rn.Store, j)
	prompt, _ := os.ReadFile(filepath.Join(dir, "prompt.txt"))
	if !strings.Contains(string(prompt), "body_paragraph_1 (required") || !strings.Contains(string(prompt), "scoutjobs cv cover") {
		t.Errorf("the agent writes slots, not a letter:\n%.600s", prompt)
	}
	md, _ := os.ReadFile(filepath.Join(dir, "cover-letter.md"))
	if !strings.Contains(string(md), "Dear Hiring Team,") || !exists(dir, "cover-letter.docx") {
		t.Errorf("letter not built from the slots: %s", md)
	}
	if again := run(t, rn, j, Request{Steps: []string{"cover"}}); states(again) != "cover=skipped" {
		t.Errorf("a built letter is done: %s", states(again))
	}
}

func TestRunSavesThePostingBeforeTheAgentReadsIt(t *testing.T) {
	rn, j := setup(t, fakeAgent)
	j.URL = "https://remotive.com/remote-jobs/software-development/go-engineer-1"
	fetched := 0
	orig := fetchPosting
	t.Cleanup(func() { fetchPosting = orig })
	fetchPosting = func(_ context.Context, u string) (search.Posting, error) {
		fetched++
		return search.Posting{Description: "Must have: Go, Postgres."}, nil
	}
	run(t, rn, j, Request{Steps: []string{"tailor"}})
	b, err := os.ReadFile(filepath.Join(apply.Dir(rn.Store, j), "posting.md"))
	if err != nil || !strings.Contains(string(b), "Must have: Go, Postgres.") || !strings.Contains(string(b), j.URL) {
		t.Fatalf("posting.md: %q %v", b, err)
	}
	if err := os.WriteFile(filepath.Join(apply.Dir(rn.Store, j), "posting.md"), []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, rn, j, Request{Steps: []string{"cover"}, Force: true})
	if b, _ := os.ReadFile(filepath.Join(apply.Dir(rn.Store, j), "posting.md")); string(b) != "edited" || fetched != 1 {
		t.Errorf("a saved posting is kept: %q, fetched %d times", b, fetched)
	}
}

const prepAgent = `#!/bin/sh
cat > prompt.txt
grep -q "Step 1, interview prep pack" prompt.txt && printf '# Prep\n' > interview-prep.md
echo "wrote the prep"
`

func TestPrepStepIsAskedForByName(t *testing.T) {
	all, _ := Parse("all")
	if strings.Contains(strings.Join(all, ","), "prep") {
		t.Fatalf("all includes prep: %v", all)
	}
	rn, j := setup(t, prepAgent)
	id, err := rn.Store.AddInterview(store.Interview{JobID: j.ID, Stage: "hr", At: "2099-01-02T10:00"})
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-2 * time.Hour).Format("2006-01-02T15:04") // over, outcome not logged yet
	if _, err := rn.Store.AddInterview(store.Interview{JobID: j.ID, Stage: "screen", At: past}); err != nil {
		t.Fatal(err)
	}
	got := run(t, rn, j, Request{Steps: []string{"prep"}})
	if got.State != Done || states(got) != "prep=done" {
		t.Fatalf("%s %s %s %v", got.State, got.Error, states(got), got.Log)
	}
	ivs, _ := rn.Store.Interviews(j.ID, false)
	if len(ivs) != 2 || ivs[1].ID != id || ivs[1].Prep != "company,posting,questions" || ivs[1].PrepStatus != "in_progress" || ivs[0].Prep != "" {
		t.Fatalf("prep items not ticked on the coming interview: %+v", ivs)
	}
	if got = run(t, rn, j, Request{Steps: []string{"prep"}}); states(got) != "prep=skipped" {
		t.Errorf("second run: %s", states(got))
	}
}

func TestExtraReachesTheStepTask(t *testing.T) {
	rn, j := setup(t, fakeAgent)
	got := run(t, rn, j, Request{Steps: []string{"tailor"}, Extra: map[string]string{"tailor": "Work in: event streaming with Kafka.", "cover": "never shown"}})
	if got.State != Done {
		t.Fatalf("%s %s", got.State, got.Error)
	}
	prompt, _ := os.ReadFile(filepath.Join(apply.Dir(rn.Store, j), "prompt.txt"))
	if !strings.Contains(string(prompt), "Work in: event streaming with Kafka.") || strings.Contains(string(prompt), "never shown") {
		t.Errorf("prompt:\n%.600s", prompt)
	}
}

// interviewAgent writes what the intel, mock and offer steps ask for.
const interviewAgent = `#!/bin/sh
cat > prompt.txt
grep -q "Step [0-9], company research" prompt.txt && printf '# Acme\n\nSells anvils.\n' > company-intel.md
grep -q "Step [0-9], mock interview score" prompt.txt && printf 'Good.\n' > mock-1-feedback.md && printf '{"overall":4,"summary":"Solid.","answers":[]}' > mock-1-result.json
grep -q "Step [0-9], offer and negotiation" prompt.txt && printf '# Offer\n' > offer-prep.md
echo done
`

func TestInterviewSteps(t *testing.T) {
	rn, j := setup(t, interviewAgent)
	got := run(t, rn, j, Request{Steps: []string{"intel", "offer"}})
	if got.State != Done || states(got) != "intel=done offer=done" {
		t.Fatalf("%s %s %s %v", got.State, got.Error, states(got), got.Log)
	}
	if !strings.Contains(apply.CompanyFile(rn.Store, j.Company, apply.CompanyIntel), "Sells anvils.") {
		t.Error("intel not kept on the company")
	}
	if prompt, _ := os.ReadFile(filepath.Join(apply.Dir(rn.Store, j), "prompt.txt")); !strings.Contains(string(prompt), "companies/acme/notes.md") {
		t.Error("{company} not replaced in the task")
	}
	if got = run(t, rn, j, Request{Steps: []string{"mock"}}); got.State == Done {
		t.Error("mock step done without an answered mock")
	}
	_, _ = rn.Store.AddQuestion(j, store.Question{Text: "Why Acme?"})
	m, err := apply.StartMock(rn.Store, j)
	if err != nil {
		t.Fatal(err)
	}
	dir := apply.Dir(rn.Store, j)
	_ = apply.SaveAnswers(dir, m.N, map[int64]string{m.Questions[0].ID: "Anvils."})
	got = run(t, rn, j, Request{Steps: []string{"mock"}, Force: true, Extra: map[string]string{"mock": "The mock file is mock-1.json."}})
	if got.State != Done {
		t.Fatalf("mock: %s %s %v", got.State, got.Error, got.Log)
	}
	prompt, _ := os.ReadFile(filepath.Join(dir, "prompt.txt"))
	if !strings.Contains(string(prompt), "The mock file is mock-1.json.") {
		t.Error("mock file not named in the task")
	}
	if sessions, _ := rn.Store.Sessions(0); len(sessions) != 1 || sessions[0].Score != 80 {
		t.Errorf("mock session: %+v", sessions)
	}
}

func TestScoringStepsRunOnTheCheapModel(t *testing.T) {
	rn, j := setup(t, "")
	// a Gemini CLI stand-in: it logs its arguments and writes what each session's prompt asks for
	bin := t.TempDir()
	gemini := filepath.Join(bin, "gemini")
	script := fakeAgent + `echo "$*" >> calls.log
grep -q "skill fit-review" prompt.txt && echo '{"requirements":[{"requirement":"Go","kind":"must","strength":"direct"}],"score":70}' > fit-review.json
true
`
	if err := os.WriteFile(gemini, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	rn.Custom = gemini + " --yolo"
	got := run(t, rn, j, Request{Steps: []string{"fit", "tailor", "cover"}})
	if got.State != Done {
		t.Fatalf("%s %s %s %v", got.State, got.Error, states(got), got.Log)
	}
	calls, _ := os.ReadFile(filepath.Join(apply.Dir(rn.Store, j), "calls.log"))
	if string(calls) != "--yolo -m flash\n--yolo\n" {
		t.Errorf("want the fit review on flash and the drafts on the usual model in one session, got:\n%s", calls)
	}
	rn.CheapModel = "off"
	_ = os.Remove(filepath.Join(apply.Dir(rn.Store, j), "calls.log"))
	_ = os.Remove(filepath.Join(apply.Dir(rn.Store, j), apply.ReviewName))
	run(t, rn, j, Request{Steps: []string{"fit"}})
	if calls, _ := os.ReadFile(filepath.Join(apply.Dir(rn.Store, j), "calls.log")); string(calls) != "--yolo\n" {
		t.Errorf("agent_cheap_model: off still picked a model: %q", calls)
	}
}
