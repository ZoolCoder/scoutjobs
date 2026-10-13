package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMCP(t *testing.T) {
	t.Setenv("SCOUTJOBS_HOME", t.TempDir())
	run(t, "init")
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"job_add","arguments":{"company":"Acme","title":"Go Engineer","remote":"remote","salary_min":90000}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"job_set","arguments":{"job":"1","status":"applied","note":"sent via portal"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"job_list","arguments":{"open":true}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"job_add","arguments":{"company":"NoTitle"}}}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"run","arguments":{"args":["email","send","1","--yes"]}}}`,
		`{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"run","arguments":{"args":["stats","--json"]}}}`,
		`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"nope","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":10,"method":"ping"}`,
		`not json`,
		`{"jsonrpc":"2.0","id":11,"method":"resources/list"}`,
		`{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"job_fit","arguments":{"job":"1","review":"{\"requirements\":[{\"requirement\":\"Go\",\"kind\":\"must\",\"strength\":\"direct\"}],\"vetoes\":[{\"rule\":\"visa\",\"reason\":\"no sponsorship\"}],\"score\":65}"}}}`,
		`{"jsonrpc":"2.0","id":13,"method":"tools/call","params":{"name":"job_check","arguments":{"job":"1"}}}`,
	}, "\n") + "\n"
	var out strings.Builder
	if err := runMCP(strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 14 {
		t.Fatalf("want 14 replies (no reply to the notification), got %d:\n%s", len(lines), out.String())
	}
	type resp struct {
		ID     json.RawMessage `json:"id"`
		Result map[string]any  `json:"result"`
		Error  *rpcError       `json:"error"`
	}
	byID := map[string]resp{}
	for _, l := range lines {
		var r resp
		if err := json.Unmarshal([]byte(l), &r); err != nil {
			t.Fatalf("bad line %q: %v", l, err)
		}
		byID[string(r.ID)] = r
	}
	text := func(id string) string {
		c := byID[id].Result["content"].([]any)[0].(map[string]any)
		return c["text"].(string)
	}
	isErr := func(id string) bool { b, _ := byID[id].Result["isError"].(bool); return b }
	if byID["1"].Result["protocolVersion"] != mcpProtocol {
		t.Errorf("initialize: %v", byID["1"].Result)
	}
	if n := len(byID["2"].Result["tools"].([]any)); n != len(mcpTools) {
		t.Errorf("tools/list: %d", n)
	}
	if !strings.Contains(text("12"), `"veto": "visa: no sponsorship"`) || isErr("12") {
		t.Errorf("job_fit: %s", text("12"))
	}
	if !strings.Contains(text("13"), `"ok": false`) || !isErr("13") {
		t.Errorf("job_check must report the veto as an error: %s", text("13"))
	}
	if !strings.Contains(text("3"), `"created": true`) || isErr("3") {
		t.Errorf("job_add: %s", text("3"))
	}
	if isErr("4") {
		t.Errorf("job_set: %s", text("4"))
	}
	if !strings.Contains(text("5"), `"status": "applied"`) {
		t.Errorf("job_list: %s", text("5"))
	}
	if !isErr("6") || !strings.Contains(text("6"), "title is required") {
		t.Errorf("missing required: %s", text("6"))
	}
	if !isErr("7") || !strings.Contains(text("7"), "not available over MCP") {
		t.Errorf("email send must be refused: %s", text("7"))
	}
	if !strings.Contains(text("8"), `"applied_total": 1`) {
		t.Errorf("run stats: %s", text("8"))
	}
	if byID["9"].Error == nil || byID["10"].Error != nil || byID["null"].Error == nil || byID["11"].Error == nil || byID["11"].Error.Code != -32601 {
		t.Errorf("errors: 9=%v 10=%v parse=%v 11=%v", byID["9"].Error, byID["10"].Error, byID["null"].Error, byID["11"].Error)
	}
	// every tool builds a command the CLI accepts (unknown flags would fail at parse time)
	for _, tool := range mcpTools {
		if tool.Name == "run" || tool.Name == "email_sync" || tool.Name == "search" {
			continue
		}
		in := map[string]any{}
		for _, pr := range tool.Props {
			if pr.Required {
				in[pr.Name] = map[string]any{"string": "1", "integer": 1.0, "number": 1.0, "boolean": true, "array": []any{"x"}}[pr.Type]
			}
		}
		args, err := tool.args(in)
		if err != nil {
			t.Errorf("%s: %v", tool.Name, err)
			continue
		}
		var buf strings.Builder
		if err := Run(args, &buf); err != nil && strings.Contains(err.Error(), "flag provided but not defined") {
			t.Errorf("%s: %v (%v)", tool.Name, err, args)
		}
	}
}

func TestMCPDeniesGoogleToTheRunner(t *testing.T) {
	if mcpDenied([]string{"gmail", "status"}) {
		t.Error("gmail status refused outside a run")
	}
	if !mcpDenied([]string{"job", "run", "3"}) {
		t.Error("an agent may start a nested run")
	}
	if !mcpDenied([]string{"google", "reconnect"}) || mcpDenied([]string{"google", "verify"}) {
		t.Error("an agent may replace the Google connection, or cannot verify it")
	}
	t.Setenv("SCOUTJOBS_RUNNER", "1")
	if !mcpDenied([]string{"gmail", "draft", "3"}) {
		t.Error("a job run reaches Gmail")
	}
	if !mcpDenied([]string{"calendar", "sync"}) || mcpDenied([]string{"calendar", "save", "--events", "[]"}) {
		t.Error("a run may only save the events it listed, not sync Google Calendar")
	}
}
