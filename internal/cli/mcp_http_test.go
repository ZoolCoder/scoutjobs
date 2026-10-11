package cli

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zoolcoder/scoutjobs/internal/store"
	"github.com/zoolcoder/scoutjobs/internal/web"
)

func TestMCPOverHTTP(t *testing.T) {
	t.Setenv("SCOUTJOBS_HOME", t.TempDir())
	run(t, "init")
	s, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	handler := func(token string) func(method, target, body string, hdr ...string) *httptest.ResponseRecorder {
		h, err := web.Handler(s, web.Config{APIToken: token, MCP: mcpHTTP()})
		if err != nil {
			t.Fatal(err)
		}
		return func(method, target, body string, hdr ...string) *httptest.ResponseRecorder {
			req := httptest.NewRequest(method, target, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			for i := 0; i+1 < len(hdr); i += 2 {
				req.Header.Set(hdr[i], hdr[i+1])
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			return rec
		}
	}
	list := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	if c := handler("")("POST", "/mcp", list).Code; c != 403 {
		t.Errorf("no api_token: %d, want 403", c)
	}
	do := handler("tok")
	cases := []struct {
		name, method, target, body string
		hdr                        []string
		code                       int
	}{
		{"no token", "POST", "/mcp", list, nil, 401},
		{"wrong token", "POST", "/mcp", list, []string{"Authorization", "Bearer nope"}, 401},
		{"header token", "POST", "/mcp", list, []string{"Authorization", "Bearer tok"}, 200},
		{"query token", "POST", "/mcp?token=tok", list, nil, 200},
		{"foreign origin", "POST", "/mcp?token=tok", list, []string{"Origin", "https://evil.example"}, 403},
		{"same origin", "POST", "/mcp?token=tok", list, []string{"Origin", "http://example.com"}, 200},
		{"get stream", "GET", "/mcp?token=tok", "", nil, 405},
		{"notification", "POST", "/mcp?token=tok", `{"jsonrpc":"2.0","method":"notifications/initialized"}`, nil, 202},
		{"bad json", "POST", "/mcp?token=tok", `[{"jsonrpc":"2.0"}]`, nil, 400},
	}
	for _, c := range cases {
		if rec := do(c.method, c.target, c.body, c.hdr...); rec.Code != c.code {
			t.Errorf("%s: %d, want %d: %s", c.name, rec.Code, c.code, rec.Body.String())
		}
	}
	rec := do("POST", "/mcp?token=tok", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"job_add","arguments":{"company":"Acme","title":"Go Engineer"}}}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Acme") || rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("tools/call: %d %s", rec.Code, rec.Body.String())
	}
}
