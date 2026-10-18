package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentCommands(t *testing.T) {
	claude := strings.Join(Agent{Name: "claude", Path: "/bin/claude"}.command("/bin/scoutjobs", "/h", "/h/applications/acme"), " ")
	for _, want := range []string{"-p", "--strict-mcp-config", `"SCOUTJOBS_RUNNER":"1"`, "Edit(//h/applications/acme/**)", "--disallowedTools Bash WebFetch WebSearch", "--permission-mode dontAsk"} {
		if !strings.Contains(claude, want) {
			t.Errorf("claude argv lacks %q: %s", want, claude)
		}
	}
	tuned := strings.Join(Agent{Name: "claude", Path: "/bin/claude", Model: "haiku", Effort: "low", Budget: 1.5}.command("/bin/scoutjobs", "/h", "/h/a"), " ")
	for _, want := range []string{"--tools Read,Glob,Grep,Edit,Write", `"SCOUTJOBS_MCP_TOOLS":"` + PrepTools + `"`, "--model haiku", "--effort low", "--max-budget-usd 1.50"} {
		if !strings.Contains(tuned, want) {
			t.Errorf("tuned claude argv lacks %q: %s", want, tuned)
		}
	}
	if strings.Contains(claude, "--max-budget-usd") || strings.Contains(claude, "--effort") {
		t.Errorf("untuned claude argv sets a cap or effort: %s", claude)
	}
	codex := strings.Join(Agent{Name: "codex", Path: "/bin/codex"}.command("/bin/scoutjobs", "/h", "/h/applications/acme"), " ")
	for _, want := range []string{"exec --json", "--cd /h/applications/acme", "--sandbox workspace-write", `mcp_servers.scoutjobs.command="/bin/scoutjobs"`, `approval_policy="never"`} {
		if !strings.Contains(codex, want) {
			t.Errorf("codex argv lacks %q: %s", want, codex)
		}
	}
	if got := (Agent{Name: "custom", Path: "/bin/aider", Args: []string{"--yes"}}).command("", "", ""); strings.Join(got, " ") != "/bin/aider --yes" {
		t.Errorf("custom %v", got)
	}
}

func TestDetectAndPick(t *testing.T) {
	dir := t.TempDir()
	tool := filepath.Join(dir, "myagent")
	_ = os.WriteFile(tool, []byte("#!/bin/sh\n"), 0o700)
	t.Setenv("PATH", dir)
	saved := ExtraDirs
	ExtraDirs = func() []string { return nil }
	t.Cleanup(func() { ExtraDirs = saved })
	found := Detect("myagent --flag")
	if len(found) == 0 || found[0].Name != "custom" || found[0].Path != tool || found[0].Args[0] != "--flag" {
		t.Fatalf("%+v", found)
	}
	if a, ok := Pick(found, "auto"); !ok || a.Name != "custom" {
		t.Errorf("auto picked %+v", a)
	}
	if _, ok := Pick(found, "nonesuch"); ok {
		t.Error("picked an agent that is not installed")
	}
}

func TestSummarize(t *testing.T) {
	cases := map[string]string{
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Write","input":{"file_path":"/x/resume.md"}}]}}`:              "Write resume.md",
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"mcp__scoutjobs__run","input":{"args":["job","show","3"]}}]}}`: "scoutjobs run job show 3",
		`{"type":"result","result":"All set"}`:                                                                           "",
		`{"type":"result","is_error":true,"result":"Credit low"}`:                                                        "error: Credit low",
		`{"type":"result","subtype":"error_max_budget_usd","is_error":true}`:                                             "error: stopped at the spend cap; raise agent_budget_usd in config.yaml to let it finish",
		`{"type":"result","subtype":"error_during_execution","errors":["Tool crashed"]}`:                                 "error: Tool crashed",
		`{"type":"result","subtype":"error_during_execution"}`:                                                           "error: error_during_execution",
		`{"type":"item.completed","item":{"type":"file_change","changes":[{"path":"/x/cover-letter.md","kind":"add"}]}}`: "wrote cover-letter.md",
		`{"type":"item.completed","item":{"type":"agent_message","text":"Drafts written."}}`:                             "Drafts written.",
		`{"type":"system","subtype":"init"}`:                                                                             "",
		`plain text from a custom agent`:                                                                                 "plain text from a custom agent",
	}
	for in, want := range cases {
		if got := summarize(in); got != want {
			t.Errorf("%s\n got %q want %q", in, got, want)
		}
	}
}

func TestCost(t *testing.T) {
	if c, ok := Cost(`{"type":"result","subtype":"success","total_cost_usd":0.0421}`); !ok || c != 0.0421 {
		t.Errorf("result event: %v %v", c, ok)
	}
	for _, line := range []string{`{"type":"assistant","total_cost_usd":1}`, `not json`, `{"type":"result"}`} {
		if _, ok := Cost(line); ok {
			t.Errorf("%s: want no cost", line)
		}
	}
}

func TestCheapModel(t *testing.T) {
	claude := Agent{Name: "claude", Path: "/bin/claude", Model: "sonnet"}
	claude.Cheap = CheapModel(claude, "")
	if argv := strings.Join(claude.Light().command("/bin/scoutjobs", "/h", "/h/a"), " "); !strings.Contains(argv, "--model haiku") || strings.Contains(argv, "sonnet") {
		t.Errorf("light claude session: %s", argv)
	}
	if argv := strings.Join(claude.command("/bin/scoutjobs", "/h", "/h/a"), " "); !strings.Contains(argv, "--model sonnet") {
		t.Errorf("a full session lost agent_model: %s", argv)
	}
	if CheapModel(claude, "off") != "" || CheapModel(claude, "opus") != "opus" {
		t.Error("agent_cheap_model is not honoured")
	}
	codex := Agent{Name: "codex", Path: "/bin/codex", Cheap: "mini"}
	if argv := codex.Light().command("/bin/scoutjobs", "/h", "/h/a"); strings.Join(argv[len(argv)-3:], " ") != "-m mini -" {
		t.Errorf("light codex session: %v", argv)
	}
	gemini := Agent{Name: "custom", Path: "/usr/bin/gemini", Args: []string{"--yolo"}}
	gemini.Cheap = CheapModel(gemini, "")
	if got := strings.Join(gemini.Light().command("", "", ""), " "); got != "/usr/bin/gemini --yolo -m flash" {
		t.Errorf("light gemini session: %s", got)
	}
	aider := Agent{Name: "custom", Path: "/bin/aider"}
	if CheapModel(aider, "") != "" || CheapModel(Agent{Name: "custom", Path: "/bin/gemini", Args: []string{"-m", "pro"}}, "") != "" {
		t.Error("a custom command got a model it did not ask for")
	}
}
