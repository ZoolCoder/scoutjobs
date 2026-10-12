package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/apply"
	"github.com/zoolcoder/scoutjobs/internal/mail"
	"github.com/zoolcoder/scoutjobs/internal/runner"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

func crossSite(h http.Handler, path string) int {
	req := httptest.NewRequest("POST", path, strings.NewReader("x=1"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func TestNotifyJSON(t *testing.T) {
	s := testStore(t)
	h := mustHandler(t, s, Config{})
	var first notices
	if err := json.Unmarshal(get(h, "/notify.json").Body.Bytes(), &first); err != nil || first.Last == 0 || len(first.Items) != 0 {
		t.Fatalf("first poll replays the past: %+v %v", first, err)
	}
	_ = s.AddEvent(1, "run:done", "")
	_ = s.AddEvent(1, "email:proposal", "thanks drafted")
	_ = s.AddEvent(1, "note", "not worth a notification")
	var next notices
	_ = json.Unmarshal(get(h, fmt.Sprintf("/notify.json?after=%d", first.Last)).Body.Bytes(), &next)
	if len(next.Items) != 2 || next.Last <= first.Last {
		t.Fatalf("after: %+v", next)
	}
	titles := next.Items[0].Title + " | " + next.Items[1].Title
	for _, want := range []string{"Your assistant finished Acme", "Review the thank-you to Acme"} {
		if !strings.Contains(titles, want) {
			t.Errorf("notices lack %q: %s", want, titles)
		}
	}
}

func TestStoryEditor(t *testing.T) {
	s := testStore(t)
	h := mustHandler(t, s, Config{})
	if b := get(h, "/stories/new").Body.String(); !strings.Contains(b, `action="/stories"`) || !strings.Contains(b, `name="situation"`) {
		t.Fatal("story form missing")
	}
	if crossSite(h, "/stories") != http.StatusForbidden {
		t.Error("cross-site story create")
	}
	if loc := post(h, "/stories", url.Values{"title": {"  "}}).Header().Get("Location"); !strings.Contains(loc, "err=") {
		t.Errorf("story without a title: %s", loc)
	}
	form := url.Values{"title": {"Cut p99 latency"}, "themes": {"performance, postgres"}, "metrics": {"900 ms to 350 ms\n\n0 incidents"},
		"facts": {"Checkout API in Go."}, "action": {"Added a read replica."}}
	rec := post(h, "/stories", form)
	if loc := rec.Header().Get("Location"); rec.Code != http.StatusSeeOther || loc != "/stories/cut-p99-latency.md?saved=1" {
		t.Fatalf("create: %d %s", rec.Code, loc)
	}
	if loc := post(h, "/stories", form).Header().Get("Location"); loc != "/stories/cut-p99-latency-2.md?saved=1" {
		t.Errorf("second story with the same title: %s", loc)
	}
	bank := apply.Stories(s.Home)
	sort.Slice(bank, func(i, k int) bool { return len(bank[i].File) < len(bank[k].File) })
	if len(bank) != 2 || bank[0].ID != "cut-p99-latency" || strings.Join(bank[0].Themes, ",") != "performance,postgres" || len(bank[0].Metrics) != 2 || bank[0].Facts != "Checkout API in Go." {
		t.Fatalf("written story does not parse back: %+v", bank)
	}
	b := get(h, "/stories/cut-p99-latency.md").Body.String()
	if !strings.Contains(b, "Added a read replica.") || strings.Contains(b, "last_modified") || !strings.Contains(b, `href="/stories"`) {
		t.Error("story preview")
	}
	if !strings.Contains(get(h, "/stories").Body.String(), `href="/stories/cut-p99-latency.md"`) {
		t.Error("story bank does not link the story")
	}
	if crossSite(h, "/stories/cut-p99-latency.md") != http.StatusForbidden {
		t.Error("cross-site story save")
	}
	if rec := post(h, "/stories/cut-p99-latency.md", url.Values{"body": {"---\ntitle: Renamed\n---\n"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("save: %d", rec.Code)
	}
	if bank = apply.Stories(s.Home); bank[1].Title != "Renamed" && bank[0].Title != "Renamed" {
		t.Error("edit not saved")
	}
	for _, p := range []string{"/stories/missing.md", "/stories/..%2Fscoutjobs.db"} {
		if rec := get(h, p); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}
	if rec := post(h, "/stories/new.md", url.Values{"body": {"x"}}); rec.Code != http.StatusNotFound {
		t.Errorf("save creates a file: %d", rec.Code)
	}
}

func TestUnqueue(t *testing.T) {
	s := testStore(t)
	if crossSite(mustHandler(t, s, Config{}), "/pipeline/queue/1/remove") != http.StatusForbidden {
		t.Error("unqueue without an assistant")
	}
	agent := filepath.Join(s.Home, "agent.sh")
	_ = os.WriteFile(agent, []byte("#!/bin/sh\ncat >/dev/null\n"), 0o700)
	m := runner.NewManager(&runner.Runner{Store: s, Exe: "scoutjobs", Custom: agent})
	h := mustHandler(t, s, Config{Runner: m})
	if crossSite(h, "/pipeline/queue/1/remove") != http.StatusForbidden {
		t.Error("cross-site unqueue")
	}
	if loc := post(h, "/pipeline/queue/1/remove", nil).Header().Get("Location"); !strings.Contains(loc, "already+started") {
		t.Errorf("unqueue idle job: %s", loc)
	}
}

func TestTodayThanksAndQuietRows(t *testing.T) {
	s := testStore(t)
	h := mustHandler(t, s, Config{FollowUpDays: 400})
	set := func(id int64, st string) { _ = s.UpdateJob(id, store.JobPatch{Status: &st}) }
	for i := range 3 {
		j, _, _ := s.UpsertJob(store.Job{Company: fmt.Sprintf("Co%d", i), Title: "Eng", Source: "lever"})
		set(j.ID, "applied")
		set(j.ID, "rejected")
	}
	q, _, _ := s.UpsertJob(store.Job{Company: "Hooli", Title: "Eng", Source: "lever"})
	set(q.ID, "applied")
	old := time.Now().UTC().AddDate(0, 0, -30).Format(time.RFC3339)
	if _, err := s.DB.Exec(`UPDATE jobs SET updated_at=? WHERE id=?`, old, q.ID); err != nil {
		t.Fatal(err)
	}
	set(1, "interview")
	j, _ := s.JobByID(1)
	if err := apply.SaveProposal(s, j, "thanks", mail.Message{To: "hr@acme.test", Subject: "Thank you", Body: "Thanks"}); err != nil {
		t.Fatal(err)
	}
	b := between(get(h, "/").Body.String(), "Today", "Upcoming")
	for _, want := range []string{"Review thank-you to Acme", "Hooli has likely moved on", "lever jobs usually answer in ", `value="no_response"`} {
		if !strings.Contains(b, want) {
			t.Errorf("today lacks %q:\n%s", want, b)
		}
	}
}

func TestPrepPackStatus(t *testing.T) {
	s := testStore(t)
	agent := filepath.Join(s.Home, "agent.sh")
	_ = os.WriteFile(agent, []byte("#!/bin/sh\ncat >/dev/null\n"), 0o700)
	h := mustHandler(t, s, Config{Runner: runner.NewManager(&runner.Runner{Store: s, Exe: "scoutjobs", Custom: agent})})
	if b := get(h, "/interviews").Body.String(); strings.Contains(b, "Prep pack ready") || !strings.Contains(b, ">Build prep pack<") {
		t.Error("prep pack shown before it exists")
	}
	j, _ := s.JobByID(1)
	dir := apply.Dir(s, j)
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "interview-prep.md"), []byte("# Prep\n"), 0o644)
	ivs, _ := s.Interviews(1, false)
	for _, p := range []string{"/interviews", fmt.Sprintf("/calendar?view=week&d=2999-01-01&ev=i%d", ivs[0].ID)} {
		b := get(h, p).Body.String()
		for _, want := range []string{"Prep pack ready", `href="/jobs/1/files/interview-prep.md"`, ">Rebuild prep pack<"} {
			if !strings.Contains(b, want) {
				t.Errorf("%s lacks %q", p, want)
			}
		}
	}
}

func TestSkipLinkAndPaletteCount(t *testing.T) {
	h := mustHandler(t, testStore(t), Config{})
	b := get(h, "/").Body.String()
	for _, want := range []string{`<a class="skiplink" href="#main">`, `<main id="main" tabindex="-1">`, `aria-live="polite" data-palcount`} {
		if !strings.Contains(b, want) {
			t.Errorf("layout lacks %s", want)
		}
	}
}
