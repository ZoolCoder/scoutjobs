package runner

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Agent is an AI coding CLI the runner can drive headless: claude, codex, or a command from config.yaml.
type Agent struct {
	Name string   // claude | codex | custom
	Path string   // executable
	Args []string // custom only: arguments before the prompt, which arrives on stdin
}

// Known agents, in the order Detect prefers them.
var known = []string{"claude", "codex"}

// ExtraDirs covers a dashboard started by launchd or systemd, whose PATH lacks the user's shell setup.
var ExtraDirs = func() []string {
	h, _ := os.UserHomeDir()
	return []string{filepath.Join(h, ".local", "bin"), filepath.Join(h, ".claude", "local"), "/opt/homebrew/bin", "/usr/local/bin",
		filepath.Join(h, ".npm-global", "bin"), filepath.Join(h, ".bun", "bin"), filepath.Join(h, ".volta", "bin")}
}

func lookPath(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	for _, d := range ExtraDirs() {
		p := filepath.Join(d, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}

// Detect lists the agents installed here; a custom command (config.yaml agent_command) comes first.
func Detect(custom string) []Agent {
	var out []Agent
	if f := strings.Fields(custom); len(f) > 0 {
		if p := lookPath(f[0]); p != "" {
			out = append(out, Agent{Name: "custom", Path: p, Args: f[1:]})
		}
	}
	for _, n := range known {
		if p := lookPath(n); p != "" {
			out = append(out, Agent{Name: n, Path: p})
		}
	}
	return out
}

// Pick returns the agent called name, or the first one detected when name is "" or "auto".
func Pick(agents []Agent, name string) (Agent, bool) {
	for _, a := range agents {
		if name == "" || name == "auto" || a.Name == name {
			return a, true
		}
	}
	return Agent{}, false
}

// command builds the argv that runs the agent unattended in dir, with the scoutjobs MCP server as its
// only tool for the job data and file writes limited to the application folder. Nothing it is given can send.
func (a Agent) command(exe, home, dir string) []string {
	switch a.Name {
	case "claude":
		mcp, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"scoutjobs": map[string]any{
			"command": exe, "args": []string{"mcp"}, "env": map[string]string{"SCOUTJOBS_HOME": home, "SCOUTJOBS_RUNNER": "1"}}}})
		// --restricted ignores the user's own permission settings and confines file tools to the working
		// directories: the data folder is readable, writes are allowed only in the application folder.
		abs := "/" + dir // permission rules write absolute paths with a leading //
		return []string{a.Path, "-p", "--restricted", "--output-format", "stream-json", "--verbose", "--no-session-persistence",
			"--permission-mode", "dontAsk", "--mcp-config", string(mcp), "--strict-mcp-config", "--add-dir", home,
			"--allowedTools", "mcp__scoutjobs", "Read", "Glob", "Grep", "Edit(" + abs + "/**)", "Write(" + abs + "/**)",
			"--disallowedTools", "Bash", "WebFetch", "WebSearch"}
	case "codex":
		env := `{SCOUTJOBS_HOME=` + strconv.Quote(home) + `,SCOUTJOBS_RUNNER="1"}`
		return []string{a.Path, "exec", "--json", "--skip-git-repo-check", "--cd", dir, "--sandbox", "workspace-write",
			"-c", "mcp_servers.scoutjobs.command=" + strconv.Quote(exe), "-c", `mcp_servers.scoutjobs.args=["mcp"]`,
			"-c", "mcp_servers.scoutjobs.env=" + env, "-c", `approval_policy="never"`, "-"}
	}
	return append([]string{a.Path}, a.Args...)
}

// summarize turns one line of the agent's output into a short log line, "" for noise. It knows Claude's
// stream-json and Codex's --json events; any other line (a custom agent) is kept as it is.
func summarize(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}
	var ev map[string]any
	if json.Unmarshal([]byte(line), &ev) != nil {
		return clip(line)
	}
	str := func(m map[string]any, k string) string { s, _ := m[k].(string); return s }
	switch str(ev, "type") {
	case "assistant": // claude
		msg, _ := ev["message"].(map[string]any)
		parts, _ := msg["content"].([]any)
		var out []string
		for _, p := range parts {
			c, _ := p.(map[string]any)
			switch str(c, "type") {
			case "text":
				out = append(out, clip(str(c, "text")))
			case "tool_use":
				in, _ := c["input"].(map[string]any)
				out = append(out, tool(str(c, "name"), in))
			}
		}
		return strings.Join(out, "\n")
	case "result": // repeats the last assistant text; only an error is new
		if ev["is_error"] == true {
			return "error: " + clip(str(ev, "result"))
		}
	case "item.completed": // codex
		it, _ := ev["item"].(map[string]any)
		switch str(it, "type") {
		case "agent_message":
			return clip(str(it, "text"))
		case "mcp_tool_call":
			if srv := str(it, "server"); srv != "" && srv != "scoutjobs" {
				return srv + " " + str(it, "tool")
			}
			return "scoutjobs " + str(it, "tool")
		case "file_change":
			ch, _ := it["changes"].([]any)
			var names []string
			for _, c := range ch {
				m, _ := c.(map[string]any)
				names = append(names, filepath.Base(str(m, "path")))
			}
			return "wrote " + strings.Join(names, ", ")
		case "command_execution":
			return "ran " + clip(str(it, "command"))
		}
	case "turn.failed", "error":
		if e, ok := ev["error"].(map[string]any); ok {
			return "error: " + clip(str(e, "message"))
		}
		return "error: " + clip(str(ev, "message"))
	}
	return ""
}

func tool(name string, in map[string]any) string {
	if p, ok := in["file_path"].(string); ok {
		return name + " " + filepath.Base(p)
	}
	if strings.HasPrefix(name, "mcp__scoutjobs__") {
		name = "scoutjobs " + strings.TrimPrefix(name, "mcp__scoutjobs__")
		if args, ok := in["args"].([]any); ok {
			for _, a := range args {
				if s, ok := a.(string); ok {
					name += " " + s
				}
			}
		}
	}
	return name
}

func clip(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 240 {
		return string(r[:240]) + "…"
	}
	return s
}
