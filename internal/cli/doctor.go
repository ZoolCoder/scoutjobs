package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/gmail"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

// doctor answers "why does the skill say the CLI is missing" before anyone opens an issue:
// binary on PATH, data dir, config keys, mail secrets in the environment, skills linked
// into each agent, Codex prompts, dashboard up. Read-only; it never touches the network
// except one TCP dial to the dashboard address, plus one GET to GitHub with --online.

type check struct {
	Level  string `json:"level"` // ok | warn | fail
	Item   string `json:"item"`
	Detail string `json:"detail,omitempty"`
	Fix    string `json:"fix,omitempty"`
}

var configKeys = map[string]bool{"follow_up_days": true, "stale_days": true, "weekly_apply_target": true, "fit_apply_floor": true, "currency": true, "dashboard_addr": true, "api_token": true, "webhook_url": true, "webhook_secret": true, "email_from": true, "smtp_host": true, "smtp_user": true, "imap_host": true, "imap_user": true, "gmail_client_id": true, "gmail_client_secret": true, "search_queries": true, "search_boards": true, "search_remote": true, "agent": true, "agent_command": true, "cv_source": true, "cover_template": true, "theme": true}

var agentSkillDirs = []struct{ name, dir string }{
	{"claude code", ".claude/skills"}, {"codex", ".codex/skills"}, {"agent skills", ".agents/skills"},
	{"cursor", ".cursor/skills"}, {"gemini", ".gemini/skills"}, {"copilot", ".copilot/skills"},
}

func runDoctor(rest []string, out io.Writer) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "")
	online := fs.Bool("online", false, "")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	home := store.Home()
	cfg := LoadConfig(home)
	var cs []check
	add := func(level, item, detail, fix string) { cs = append(cs, check{level, item, detail, fix}) }

	exe, _ := os.Executable()
	add("ok", "version", Version+" "+exe, "")
	if *online {
		cs = append(cs, updateCheck())
	}
	if p, err := exec.LookPath("scoutjobs"); err != nil {
		add("fail", "PATH", "scoutjobs is not on PATH; skills call it by name", "./install.sh, or add ~/.local/bin to PATH")
	} else {
		add("ok", "PATH", p, "")
	}

	if st, err := os.Stat(home); err != nil || !st.IsDir() {
		add("fail", "data dir", home+" missing", "scoutjobs init")
		return finishDoctor(out, *asJSON, cs)
	}
	add("ok", "data dir", home, "")
	cs = append(cs, configChecks(home, cfg)...)
	cs = append(cs, fileChecks(home)...)
	if s, err := store.Open(); err != nil {
		add("fail", "database", err.Error(), "scoutjobs init")
	} else {
		st, _ := s.Stats(cfg.FollowUpDays, cfg.StaleDays)
		s.Close()
		add("ok", "database", fmt.Sprintf("%d jobs tracked", st.Total), "")
	}
	cs = append(cs, mailChecks(cfg)...)
	cs = append(cs, gmailCheck(home, cfg))
	cs = append(cs, agentChecks(home)...)

	cs = append(cs, dashboardCheck(cfg.DashboardAddr, home))
	return finishDoctor(out, *asJSON, cs)
}

func configChecks(home string, cfg Config) []check {
	var cs []check
	p := filepath.Join(home, "config.yaml")
	f, err := os.Open(p)
	if err != nil {
		return []check{{"warn", "config.yaml", "missing, defaults apply", "scoutjobs init"}}
	}
	defer f.Close()
	var unknown []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		k, _, ok := strings.Cut(line, ":")
		if k = strings.TrimSpace(k); ok && k != "" && !configKeys[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		cs = append(cs, check{"warn", "config.yaml", "unknown keys ignored: " + strings.Join(unknown, ", "), "see the config reference for the spelling"})
	} else {
		cs = append(cs, check{"ok", "config.yaml", p, ""})
	}
	if len(cfg.SearchQueries) == 0 && len(cfg.SearchBoards) == 0 {
		cs = append(cs, check{"warn", "search", "no search_queries or search_boards", "set them in config.yaml or pass --query/--board"})
	} else {
		cs = append(cs, check{"ok", "search", fmt.Sprintf("%d queries, %d boards, remote=%v", len(cfg.SearchQueries), len(cfg.SearchBoards), cfg.SearchRemote), ""})
	}
	if cfg.Currency != "" && !store.KnownCurrency(cfg.Currency) {
		cs = append(cs, check{"warn", "currency", cfg.Currency + " has no rate; salaries are stored as quoted", "use an ISO code such as EUR, USD, GBP"})
	}
	return cs
}

func fileChecks(home string) []check {
	var cs []check
	prof := filepath.Join(home, "profile.md")
	if b, err := os.ReadFile(prof); err != nil {
		cs = append(cs, check{"fail", "profile.md", "missing", "scoutjobs init, then the scoutjobs-setup skill"})
	} else if strings.Contains(string(b), "Filled by the scoutjobs-setup skill") && len(b) <= len(profileTemplate)+64 {
		cs = append(cs, check{"warn", "profile.md", "still the template", "run the scoutjobs-setup skill in your agent"})
	} else {
		cs = append(cs, check{"ok", "profile.md", fmt.Sprintf("%d bytes", len(b)), ""})
	}
	if _, err := os.Stat(filepath.Join(home, "preferences.md")); err != nil {
		cs = append(cs, check{"warn", "preferences.md", "missing", "scoutjobs init"})
	}
	cvs, _ := filepath.Glob(filepath.Join(home, "cv", "*"))
	n := 0
	for _, f := range cvs {
		if !strings.EqualFold(filepath.Base(f), "README.md") {
			n++
		}
	}
	if n == 0 {
		cs = append(cs, check{"warn", "cv/", "no resume files", "copy your CV into " + filepath.Join(home, "cv")})
	} else {
		cs = append(cs, check{"ok", "cv/", fmt.Sprintf("%d files", n), ""})
	}
	return cs
}

func mailChecks(cfg Config) []check {
	if cfg.SMTPHost == "" && cfg.IMAPHost == "" {
		return []check{{"warn", "e-mail", "smtp_host not set; apply-by-email is off", "see the apply-by-email page if you want to send from here"}}
	}
	var cs []check
	switch {
	case cfg.SMTPHost == "":
		cs = append(cs, check{"warn", "smtp", "imap_host set without smtp_host", "add smtp_host and email_from"})
	case cfg.EmailFrom == "":
		cs = append(cs, check{"fail", "smtp", "email_from missing", "set email_from in config.yaml"})
	case os.Getenv("SCOUTJOBS_SMTP_PASSWORD") == "":
		cs = append(cs, check{"fail", "smtp", cfg.SMTPHost + " configured, SCOUTJOBS_SMTP_PASSWORD not in the environment", "export SCOUTJOBS_SMTP_PASSWORD (never put it in a file)"})
	default:
		cs = append(cs, check{"ok", "smtp", cfg.SMTPHost + " as " + cfg.EmailFrom + ", password in env", "scoutjobs email test sends a probe"})
	}
	if cfg.IMAPHost != "" {
		if os.Getenv("SCOUTJOBS_IMAP_PASSWORD") == "" && os.Getenv("SCOUTJOBS_SMTP_PASSWORD") == "" {
			cs = append(cs, check{"fail", "imap", cfg.IMAPHost + " configured, no SCOUTJOBS_IMAP_PASSWORD or SCOUTJOBS_SMTP_PASSWORD", "export one of them"})
		} else {
			cs = append(cs, check{"ok", "imap", cfg.IMAPHost + ", password in env", ""})
		}
	}
	return cs
}

func gmailCheck(home string, cfg Config) check {
	c := gmail.Client{Home: home}
	st := c.Status()
	switch {
	case st.Problem != "":
		return check{"fail", "gmail", st.Problem, "scoutjobs google reconnect"}
	case st.Testing:
		return check{"warn", "gmail", "connected as " + orDash(c.Account()) + ", app in Testing mode: Google disconnects it on " + st.Ends.Format("Jan 2"),
			"publish the app (scoutjobs google status lists the steps), then scoutjobs google reconnect"}
	case c.Connected() && !c.HasCalendar():
		return check{"warn", "gmail", "connected as " + orDash(c.Account()) + ", Calendar not allowed", "scoutjobs gmail connect, and allow Calendar (read-only)"}
	case c.Connected():
		return check{"ok", "gmail", "connected as " + orDash(c.Account()) + ", with Calendar", "scoutjobs gmail test checks both"}
	case cfg.GmailClientID != "" || gmail.LoadCredentials(home).ID != "":
		return check{"warn", "gmail", "OAuth client set, not connected", "scoutjobs gmail connect"}
	}
	return check{"warn", "gmail", "not connected", "scoutjobs gmail setup: replies matched without an agent reading your mail"}
}

func agentChecks(home string) []check {
	var cs []check
	userHome, _ := os.UserHomeDir()
	linked := 0
	for _, a := range agentSkillDirs {
		dir := filepath.Join(userHome, a.dir)
		if _, err := os.Stat(filepath.Join(dir, "scoutjobs-setup", "SKILL.md")); err != nil {
			if _, err := os.Stat(filepath.Dir(dir)); err == nil {
				cs = append(cs, check{"warn", a.name, "skills not linked into " + dir, "./install.sh"})
			}
			continue
		}
		linked++
		n, _ := filepath.Glob(filepath.Join(dir, "*", "SKILL.md"))
		cs = append(cs, check{"ok", a.name, fmt.Sprintf("%d skills in %s", len(n), dir), ""})
	}
	var plugins []string
	if b, err := os.ReadFile(filepath.Join(userHome, ".claude", "plugins", "installed_plugins.json")); err == nil && strings.Contains(string(b), `"scoutjobs@`) {
		plugins = []string{"installed_plugins.json lists scoutjobs"}
		cs = append(cs, check{"ok", "claude plugin", plugins[0], ""})
	}
	if linked == 0 && len(plugins) == 0 {
		cs = append(cs, check{"fail", "skills", "no agent has the skills", "./install.sh, or /plugin install scoutjobs@scoutjobs in Claude Code"})
	}
	if _, err := os.Stat(filepath.Join(userHome, ".codex")); err == nil {
		prompts, _ := filepath.Glob(filepath.Join(userHome, ".codex", "prompts", "scoutjobs-*.md"))
		if len(prompts) == 0 {
			cs = append(cs, check{"warn", "codex prompts", "none in ~/.codex/prompts", "SCOUTJOBS_PLATFORMS=codex ./install.sh"})
		} else {
			cs = append(cs, check{"ok", "codex prompts", fmt.Sprintf("%d in ~/.codex/prompts", len(prompts)), ""})
		}
	}
	if _, err := os.Stat(filepath.Join(home, "agents")); err != nil {
		cs = append(cs, check{"warn", "agents", home + "/agents missing; mock-interview personas unavailable outside Claude Code", "./install.sh"})
	}
	return cs
}

// releaseURL is GitHub's latest-release endpoint; tests point it at a local server.
var releaseURL = "https://api.github.com/repos/zoolcoder/scoutjobs/releases/latest"

// updateCheck compares the running Version with the latest tag. Only with --online, so a
// plain doctor run never leaves the machine. A dev build is reported, not compared.
func updateCheck() check {
	if devBuild() {
		return check{"ok", "update", "dev build, not compared", ""}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, releaseURL, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "scoutjobs/"+Version)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return check{"warn", "update", "could not reach GitHub: " + err.Error(), ""}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return check{"warn", "update", "GitHub answered " + resp.Status, ""}
	}
	var rel struct {
		Tag string `json:"tag_name"`
		URL string `json:"html_url"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil || rel.Tag == "" {
		return check{"warn", "update", "unexpected answer from GitHub", ""}
	}
	latest := strings.TrimPrefix(rel.Tag, "v")
	if latest == strings.TrimPrefix(Version, "v") {
		return check{"ok", "update", "latest is " + rel.Tag, ""}
	}
	return check{"warn", "update", "running " + Version + ", latest is " + rel.Tag, "scoutjobs update (brew upgrade --cask scoutjobs for Homebrew), or " + rel.URL}
}

func finishDoctor(out io.Writer, asJSON bool, cs []check) error {
	fails, warns := 0, 0
	for _, c := range cs {
		switch c.Level {
		case "fail":
			fails++
		case "warn":
			warns++
		}
	}
	return emit(out, asJSON, map[string]any{"checks": cs, "fails": fails, "warns": warns}, func() {
		for _, c := range cs {
			fmt.Fprintf(out, "%-5s %-14s %s\n", c.Level, c.Item, c.Detail)
			if c.Fix != "" && c.Level != "ok" {
				fmt.Fprintf(out, "      %-14s fix: %s\n", "", c.Fix)
			}
		}
		fmt.Fprintf(out, "\n%d checks, %d warnings, %d failures\n", len(cs), warns, fails)
		if fails > 0 {
			fmt.Fprintln(out, "still stuck after the fixes? open an issue with this output: https://github.com/ZoolCoder/scoutjobs/issues/new/choose")
		}
	})
}
