package autopilot

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/apply"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

func open(t *testing.T) *store.Store {
	t.Helper()
	t.Setenv("SCOUTJOBS_HOME", t.TempDir())
	s, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// board serves n Go jobs and one sales job on a fake Greenhouse board.
func board(t *testing.T, n int) map[string]string {
	t.Helper()
	var jobs []string
	for i := 1; i <= n; i++ {
		jobs = append(jobs, fmt.Sprintf(`{"id":%d,"title":"Go Engineer %d","absolute_url":"https://acme/%d","location":{"name":"Remote"}}`, i, i, i))
	}
	jobs = append(jobs, `{"id":999,"title":"Sales Rep","absolute_url":"https://acme/999","location":{"name":"Remote"}}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/gh/acme/jobs" {
			w.Write([]byte(`{"jobs":[` + strings.Join(jobs, ",") + `]}`))
			return
		}
		w.Write([]byte(`{"content":"Build services in Go"}`))
	}))
	t.Cleanup(srv.Close)
	return map[string]string{"greenhouse": srv.URL + "/gh"}
}

func TestDailySearchRunsOncePerDayAndCaps(t *testing.T) {
	s := open(t)
	base := board(t, 25)
	id, err := s.SaveSearch(store.SavedSearch{Name: "go", Queries: "go", Boards: "greenhouse:acme"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveSearch(store.SavedSearch{Name: "not daily", Queries: "sales", Boards: "greenhouse:acme"}); err != nil {
		t.Fatal(err)
	}
	_ = s.SetSearchDaily(id, true)
	now := time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)
	o := Options{FollowUpDays: 10, BaseURL: base}

	r := Daily(context.Background(), s, o, now)
	if len(r.Searches) != 1 || r.Searches[0].Found != 25 || r.Added != maxPerSearch {
		t.Fatalf("first pass: %+v", r)
	}
	events, _ := s.Events(0, 100)
	digests := 0
	for _, e := range events {
		if e.Kind == DigestEvent && e.Note == "go" {
			digests++
		}
	}
	if digests != maxPerSearch {
		t.Errorf("digest events: %d", digests)
	}
	if r = Daily(context.Background(), s, o, now.Add(time.Hour)); len(r.Searches) != 0 || r.Summary() != "nothing due" {
		t.Errorf("ran again within the day: %+v", r)
	}
	r = Daily(context.Background(), s, o, now.Add(21*time.Hour))
	if len(r.Searches) != 1 || r.Added != 5 {
		t.Errorf("next day adds the rest only: %+v", r)
	}
	jobs, _ := s.ListJobs(store.JobFilter{})
	if len(jobs) != 25 {
		t.Errorf("jobs: %d", len(jobs))
	}
}

func TestFollowUpDraftedOnceWhenSwitchedOn(t *testing.T) {
	s := open(t)
	j, _, err := s.UpsertJob(store.Job{Company: "Globex", Title: "Go Engineer", Contact: "hr@globex.test"})
	if err != nil {
		t.Fatal(err)
	}
	applied := "applied"
	if err := s.UpdateJob(j.ID, store.JobPatch{Status: &applied}); err != nil {
		t.Fatal(err)
	}
	j, _ = s.JobByID(j.ID)
	later := time.Now().Add(11 * 24 * time.Hour)
	o := Options{FollowUpDays: 10}

	if r := Daily(context.Background(), s, o, later); len(r.FollowUps) != 0 {
		t.Fatalf("drafted while switched off: %+v", r)
	}
	_ = s.SetSetting(FollowUpsSetting, "on")
	r := Daily(context.Background(), s, o, later)
	if len(r.FollowUps) != 1 || r.FollowUps[0] != "Globex: followup" {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(filepath.Join(apply.Dir(s, j), apply.DraftFile("followup"))); err != nil {
		t.Fatal(err)
	}
	if r = Daily(context.Background(), s, o, later); len(r.FollowUps) != 0 {
		t.Errorf("drafted twice: %+v", r)
	}
	events, _ := s.Events(j.ID, 50)
	for _, e := range events {
		if strings.HasPrefix(e.Kind, "email:sent") || e.Kind == "email:send" {
			t.Errorf("sent: %+v", e)
		}
	}
}

func TestPrepQueuedForInterviewThisWeek(t *testing.T) {
	s := open(t)
	j, _, _ := s.UpsertJob(store.Job{Company: "Initech", Title: "Go Engineer"})
	far, _, _ := s.UpsertJob(store.Job{Company: "Hooli", Title: "Go Engineer"})
	now := time.Now()
	_, _ = s.AddInterview(store.Interview{JobID: j.ID, Stage: "tech", At: now.Add(48 * time.Hour).Format("2006-01-02T15:04")})
	_, _ = s.AddInterview(store.Interview{JobID: far.ID, Stage: "tech", At: now.Add(30 * 24 * time.Hour).Format("2006-01-02T15:04")})
	var asked []string
	o := Options{Prep: func(j store.Job) bool { asked = append(asked, j.Company); return true }}
	_ = s.SetSetting(PrepSetting, "on")

	if r := Daily(context.Background(), s, o, now); strings.Join(r.Prep, ",") != "Initech" || strings.Join(asked, ",") != "Initech" {
		t.Fatalf("%+v %v", r, asked)
	}
	dir := apply.Dir(s, j)
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "interview-prep.md"), []byte("# Prep"), 0o644)
	if r := Daily(context.Background(), s, o, now); len(r.Prep) != 0 {
		t.Errorf("asked again with a pack in place: %+v", r)
	}
}
