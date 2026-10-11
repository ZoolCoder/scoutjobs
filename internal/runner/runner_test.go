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

func TestRunStopsAtTheFirstFailure(t *testing.T) {
	rn, j := setup(t, fakeAgent)
	got := run(t, rn, j, Request{Steps: []string{"tailor", "cover", "export", "check", "draft"}})
	if got.State != Failed || !strings.HasPrefix(got.Error, "export: ") || !strings.Contains(got.Error, "no reviewer verdict") {
		t.Fatalf("%s %q", got.State, got.Error)
	}
	if states(got) != "tailor=done cover=done export=failed check=skipped draft=skipped" {
		t.Errorf("%s", states(got))
	}
	if _, err := os.Stat(filepath.Join(apply.Dir(rn.Store, j), apply.DraftName)); err == nil {
		t.Error("drafted after a failed step")
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
