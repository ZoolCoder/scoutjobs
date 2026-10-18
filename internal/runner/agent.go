package runner

import (
	"encoding/json"
	"github.com/zoolcoder/scoutjobs/internal/store"
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
	// Model is the claude model the sessions use; empty keeps the CLI's own default, which may be the priciest
	Model string
	// Effort is the claude reasoning effort (low, medium, high); empty keeps the CLI's default
	Effort string
	// Budget stops a claude session once it has spent this many US dollars; 0 sets no cap
	Budget float64
	// Cheap is the agent's cheaper model, which scoring steps run on; empty runs them on the usual one
	Cheap string
	// light runs this session on Cheap
	light bool
}

// CheapSteps need reading and scoring more than writing, so a cheap model does them about as well.
var CheapSteps = map[string]bool{"fit": true, "ats": true}

// Light is the agent set to run a session of cheap steps, or itself when it has no cheap model.
func (a Agent) Light() Agent {
	a.light = a.Cheap != ""
	return a
}

// CheapModel is the model scoring steps run on: the configured one ("off" turns it off), else the
// agent's own cheap tier. Custom agents keep their command as written, except Gemini CLI.
func CheapModel(a Agent, configured string) string {
	switch {
	case configured == "off":
		return ""
	case configured != "":
		return configured
	}
	switch a.Name {
	case "claude":
		return "haiku"
	case "codex":
		return codexCheapModel()
	case "custom":
		if filepath.Base(a.Path) == "gemini" && !hasModelFlag(a.Args) {
			return "flash"
		}
	}
	return ""
}

// codexCheapModel is the first model Codex itself describes as affordable, from the model list
// it caches; names change too often to hard-code one.
func codexCheapModel() string {
	h, _ := os.UserHomeDir()
	data, err := os.ReadFile(filepath.Join(h, ".codex", "models_cache.json"))
	if err != nil {
		return ""
	}
	var cache struct {
		Models []struct {
			Slug        string `json:"slug"`
			Description string `json:"description"`
		} `json:"models"`
	}
	if json.Unmarshal(data, &cache) != nil {
		return ""
	}
	for _, m := range cache.Models {
		if strings.Contains(strings.ToLower(m.Description), "affordable") {
			return m.Slug
		}
	}
	return ""
}

func hasModelFlag(args []string) bool {
	for _, a := range args {
		if a == "-m" || a == "--model" || strings.HasPrefix(a, "--model=") {
			return true
		}
	}
	return false
}

// PrepTools are the scoutjobs MCP tools a preparation session sees. Each tool's schema rides along with
// every request, so the session gets the generic run tool and the two it calls most, not all of them.
const PrepTools = "run,job_show,job_set"

// claudeTuning is the model, effort and spending cap flags of a claude session.
func (a Agent) claudeTuning() []string {
	var out []string
	if a.light {
		out = append(out, "--model", a.Cheap)
	} else if a.Model != "" {
		out = append(out, "--model", a.Model)
	}
	if a.Effort != "" {
		out = append(out, "--effort", a.Effort)
	}
	if a.Budget > 0 {
		out = append(out, "--max-budget-usd", strconv.FormatFloat(a.Budget, 'f', 2, 64))
	}
	return out
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
			"command": exe, "args": []string{"mcp"}, "env": map[string]string{"SCOUTJOBS_HOME": home, "SCOUTJOBS_RUNNER": "1", "SCOUTJOBS_MCP_TOOLS": PrepTools}}}})
		// --restricted ignores the user's own permission settings and confines file tools to the working
		// directories: the data folder is readable, writes are allowed only in the application folder.
		abs := "/" + dir // permission rules write absolute paths with a leading //
		// --tools loads only these built-in tools, so the others' schemas are not sent with every request
		return append([]string{a.Path, "-p", "--restricted", "--output-format", "stream-json", "--verbose", "--no-session-persistence",
			"--permission-mode", "dontAsk", "--mcp-config", string(mcp), "--strict-mcp-config", "--add-dir", home,
			"--tools", "Read,Glob,Grep,Edit,Write",
			// the same system prompt for every job folder, so queued jobs reuse the prompt cache
			"--exclude-dynamic-system-prompt-sections",
			"--allowedTools", "mcp__scoutjobs", "Read", "Glob", "Grep", "Edit(" + abs + "/**)", "Write(" + abs + "/**)",
			"--disallowedTools", "Bash", "WebFetch", "WebSearch"}, a.claudeTuning()...)
	case "codex":
		env := `{SCOUTJOBS_HOME=` + strconv.Quote(home) + `,SCOUTJOBS_RUNNER="1",SCOUTJOBS_MCP_TOOLS=` + strconv.Quote(PrepTools) + `}`
		argv := []string{a.Path, "exec", "--json", "--skip-git-repo-check", "--cd", dir, "--sandbox", "workspace-write",
			"-c", "mcp_servers.scoutjobs.command=" + strconv.Quote(exe), "-c", `mcp_servers.scoutjobs.args=["mcp"]`,
			"-c", "mcp_servers.scoutjobs.env=" + env, "-c", `approval_policy="never"`}
		if a.light {
			argv = append(argv, "-m", a.Cheap)
		}
		return append(argv, "-")
	}
	argv := append([]string{a.Path}, a.Args...)
	if a.light {
		argv = append(argv, "-m", a.Cheap)
	}
	return argv
}

// CostEvent is the job event an agent session's spend is logged under, in US dollars first.
const CostEvent = store.SpendEvent

// Cost reads what a session spent from Claude's stream-json result event.
func Cost(line string) (float64, bool) {
	if !strings.Contains(line, `"total_cost_usd"`) {
		return 0, false
	}
	var ev struct {
		Type string  `json:"type"`
		Cost float64 `json:"total_cost_usd"`
	}
	if json.Unmarshal([]byte(line), &ev) != nil || ev.Type != "result" {
		return 0, false
	}
	return ev.Cost, true
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
		switch str(ev, "subtype") {
		case "error_max_budget_usd":
			return "error: stopped at the spend cap; raise agent_budget_usd in config.yaml to let it finish"
		case "error_max_turns":
			return "error: stopped at the turn limit before the work was done"
		}
		if ev["is_error"] == true || strings.HasPrefix(str(ev, "subtype"), "error") {
			msg := str(ev, "result")
			if errs, _ := ev["errors"].([]any); msg == "" && len(errs) > 0 {
				msg, _ = errs[0].(string)
			}
			if msg == "" {
				msg = str(ev, "subtype")
			}
			return "error: " + clip(msg)
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
	for _, k := range []string{"query", "q"} { // what a mail or calendar search looked for
		if q, ok := in[k].(string); ok && q != "" {
			return name + " " + strconv.Quote(clip(q))
		}
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
	if r := []rune(s); len(r) > 400 {
		return string(r[:400]) + "…"
	}
	return s
}
