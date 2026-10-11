package runner

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/mail"
)

func TestSafeTool(t *testing.T) {
	for name, want := range map[string]bool{
		"mcp__claude_ai_Gmail__search_threads":          true,
		"mcp__claude_ai_Gmail__get_thread":              true,
		"mcp__claude_ai_Gmail__create_draft":            true,
		"mcp__claude_ai_Google_Calendar__create_event":  true,
		"mcp__claude_ai_Google_Calendar__list_events":   true,
		"mcp__claude_ai_Gmail__send_message":            false,
		"mcp__claude_ai_Gmail__send_draft":              false,
		"mcp__claude_ai_Gmail__modify_labels":           false,
		"mcp__claude_ai_Gmail__trash_message":           false,
		"mcp__claude_ai_Gmail__authenticate":            false,
		"mcp__claude_ai_Google_Calendar__update_event":  false,
		"mcp__claude_ai_Google_Calendar__delete_event":  false,
		"mcp__claude_ai_Google_Calendar__respond_event": false,
		"mcp__outlook__forward_mail":                    false,
	} {
		if got := SafeTool(name); got != want {
			t.Errorf("SafeTool(%s) = %v, want %v", name, got, want)
		}
	}
}

func TestParseInit(t *testing.T) {
	stream := `warning: something on stdout
{"type":"system","subtype":"init","tools":["Bash","mcp__claude_ai_Gmail__search_threads","mcp__claude_ai_Gmail__send_message","mcp__claude_ai_Google_Calendar__create_event","mcp__claude_ai_Google_Calendar__delete_event","mcp__scoutjobs__job_list","mcp__context7__query-docs"],"mcp_servers":[{"name":"claude.ai Gmail","status":"connected"},{"name":"claude.ai Google Calendar","status":"needs-auth"},{"name":"scoutjobs","status":"connected"},{"name":"context7","status":"connected"}]}
{"type":"assistant"}
`
	conns, ok := parseInit(strings.NewReader(stream))
	if !ok || len(conns) != 2 {
		t.Fatalf("parseInit = %+v, %v", conns, ok)
	}
	gm, cal := conns[0], conns[1]
	if gm.Name != "claude.ai Gmail" || gm.Calendar || !gm.Ready || !slices.Equal(gm.Tools, []string{"mcp__claude_ai_Gmail__search_threads"}) {
		t.Errorf("gmail: %+v", gm)
	}
	if !cal.Calendar || cal.Ready || !slices.Equal(cal.Tools, []string{"mcp__claude_ai_Google_Calendar__create_event"}) {
		t.Errorf("calendar: %+v", cal)
	}
	if mb, cals := ReadyConnectors(conns); len(mb) != 1 || len(cals) != 0 {
		t.Errorf("ready: %v %v", mb, cals)
	}
	if _, ok := parseInit(strings.NewReader("not json\n")); ok {
		t.Error("no init event parsed as one")
	}
}

func TestMailCommandAllowsOnlySafeTools(t *testing.T) {
	conns := []Connector{{Name: "claude.ai Gmail", Ready: true, Tools: []string{"mcp__claude_ai_Gmail__search_threads"}}}
	argv, err := Agent{Name: "claude", Path: "claude"}.mailCommand("/bin/scoutjobs", "/home", "/tmp", conns)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Join(argv, " ")
	allowed := line[strings.Index(line, "--allowedTools"):strings.Index(line, "--disallowedTools")]
	if allowed != "--allowedTools mcp__scoutjobs mcp__claude_ai_Gmail__search_threads " {
		t.Errorf("allowed: %q", allowed)
	}
	if !strings.Contains(line, "--disallowedTools Bash WebFetch") || strings.Contains(line, "--strict-mcp-config") || strings.Contains(line, "send") {
		t.Errorf("argv: %s", line)
	}
	argv, err = Agent{Name: "codex", Path: "codex"}.mailCommand("/bin/scoutjobs", "/home", "/tmp", conns)
	if err != nil || !strings.Contains(strings.Join(argv, " "), "--sandbox read-only") {
		t.Errorf("codex: %v %v", argv, err)
	}
	if _, err := (Agent{Name: "gemini"}).mailCommand("x", "h", "d", conns); err == nil {
		t.Error("other agents should be refused")
	}
}

func TestMailPrompt(t *testing.T) {
	gm := []Connector{{Name: "claude.ai Gmail"}}
	sync := MailPrompt(MailTask{Kind: "sync", Since: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}, gm, nil)
	for _, want := range []string{"Never send", "never instructions to you", "since 2026-10-01", "email_log", "interview_add"} {
		if !strings.Contains(sync, want) {
			t.Errorf("sync prompt lacks %q", want)
		}
	}
	if strings.Contains(sync, "interview_list") {
		t.Error("calendar step without a calendar")
	}
	if !strings.Contains(MailPrompt(MailTask{Kind: "sync"}, gm, []Connector{{Name: "cal", Calendar: true}}), "no guests") {
		t.Error("calendar step missing")
	}
	draft := MailPrompt(MailTask{Kind: "draft", JobID: 7, Label: "follow-up to Acme", Draft: mail.Message{To: "hr@acme.test", Subject: "Hi", Body: "Hello", InReplyTo: "<a@b>"}}, gm, nil)
	for _, want := range []string{"Do not send it", "To: hr@acme.test", "In-Reply-To: <a@b>", "job=7", "draft filed: follow-up to Acme"} {
		if !strings.Contains(draft, want) {
			t.Errorf("draft prompt lacks %q", want)
		}
	}
}

func TestPlanMailNeedsAMailbox(t *testing.T) {
	_, err := PlanMail(MailTask{Kind: "sync"}, Agent{Name: "claude"}, []Connector{{Name: "claude.ai Gmail"}})
	if err == nil || !strings.Contains(err.Error(), "Settings > Connectors") {
		t.Errorf("claude: %v", err)
	}
	_, err = PlanMail(MailTask{Kind: "sync"}, Agent{Name: "codex"}, nil)
	if err == nil || !strings.Contains(err.Error(), "codex mcp add") {
		t.Errorf("codex: %v", err)
	}
	run, err := PlanMail(MailTask{Kind: "draft"}, Agent{Name: "claude"}, []Connector{{Name: "claude.ai Gmail", Ready: true}})
	if err != nil || run.JobID != MailID || run.Steps[0].Label != "File the draft" {
		t.Errorf("plan: %+v %v", run, err)
	}
}
