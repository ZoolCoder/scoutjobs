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
