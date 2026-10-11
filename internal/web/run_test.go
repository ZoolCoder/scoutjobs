package web

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/runner"
)

func TestPrepareApplicationPanel(t *testing.T) {
	s := testStore(t)
	agent := filepath.Join(s.Home, "agent.sh")
	_ = os.WriteFile(agent, []byte("#!/bin/sh\ncat >/dev/null\nprintf 'resume %.0s' $(seq 60) > resume.md\necho ok\n"), 0o700)
	m := runner.NewManager(&runner.Runner{Store: s, Exe: "scoutjobs", Custom: agent})
	h, err := Handler(s, Config{FollowUpDays: 10, StaleDays: 60, WeeklyApplyTarget: 5, Runner: m})
	if err != nil {
		t.Fatal(err)
	}
	do := func(method, path, body string, hdr ...string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if method == "POST" && !strings.HasPrefix(path, "/api/") {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		for i := 0; i+1 < len(hdr); i += 2 {
			req.Header.Set(hdr[i], hdr[i+1])
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	page := do("GET", "/jobs/1", "").Body.String()
	for _, want := range []string{"Start preparing with AI", `name="step" value="tailor"`, `<option value="custom">custom</option>`, "Nothing is sent"} {
		if !strings.Contains(page, want) {
			t.Errorf("job page lacks %q", want)
		}
	}
	if rec := do("POST", "/jobs/1/run", "step=tailor", "Origin", "https://evil.example"); rec.Code != 403 {
		t.Errorf("cross-site run: %d", rec.Code)
	}
	if rec := do("POST", "/jobs/1/run", "step=tailor&agent=custom"); rec.Code != 303 || !strings.HasSuffix(rec.Header().Get("Location"), "#run") {
		t.Fatalf("start: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	run := m.Latest(1)
	for deadline := time.Now().Add(5 * time.Second); run.Snapshot().State == runner.Running && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}
	var got runner.Status
	_ = json.Unmarshal(do("GET", "/jobs/1/run", "").Body.Bytes(), &got)
	if got.State != runner.Done || got.Steps[0].Name != "tailor" || got.Steps[0].State != runner.Done {
		t.Errorf("run state %+v", got)
	}
	if page := do("GET", "/jobs/1", "").Body.String(); !strings.Contains(page, `data-step="tailor"`) || strings.Contains(page, "data-run=") {
		t.Errorf("finished run not shown, or still polling")
	}

	rec := do("POST", "/api/jobs/1/run", `{"steps":["tailor","draft"],"dry_run":true}`)
	if rec.Code != 202 {
		t.Fatalf("api run: %d %s", rec.Code, rec.Body)
	}
	var started struct{ ID, Status string }
	_ = json.Unmarshal(rec.Body.Bytes(), &started)
	if rec := do("GET", started.Status, ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"dry_run":true`) {
		t.Errorf("run status %d %s", rec.Code, rec.Body)
	}
	if rec := do("POST", "/api/jobs/1/run", `{"steps":"send"}`); rec.Code != 400 {
		t.Errorf("unknown step: %d", rec.Code)
	}
	if rec := do("GET", "/api/agents", ""); !strings.Contains(rec.Body.String(), `"Name":"custom"`) {
		t.Errorf("agents %s", rec.Body)
	}
}

func TestPrepareApplicationWithoutAgent(t *testing.T) {
	s := testStore(t)
	t.Setenv("PATH", t.TempDir())
	saved := runner.ExtraDirs
	runner.ExtraDirs = func() []string { return nil }
	t.Cleanup(func() { runner.ExtraDirs = saved })
	h, _ := Handler(s, Config{Runner: runner.NewManager(&runner.Runner{Store: s})})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/jobs/1", nil))
	if !strings.Contains(rec.Body.String(), "Connect an AI assistant") {
		t.Error("no hint when no agent is installed")
	}
}
