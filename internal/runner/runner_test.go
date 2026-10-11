package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/apply"
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
