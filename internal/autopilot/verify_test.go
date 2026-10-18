package autopilot

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/store"
)

func TestVerifySkipsOnlyAfterRepeatedFailures(t *testing.T) {
	s := open(t)
	open404 := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/gh/acme/jobs/2" || (open404 && r.URL.Path == "/gh/acme/jobs/1") {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/gh/acme/jobs/3" {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`{"title":"Go Engineer","absolute_url":"https://boards.greenhouse.io/acme/jobs/1","content":"Go"}`))
	}))
	t.Cleanup(srv.Close)
	o := Options{BaseURL: map[string]string{"greenhouse": srv.URL + "/gh"}}
	var ids []int64
	for i, title := range []string{"Live", "Gone", "Flaky"} {
		j, _, err := s.UpsertJob(store.Job{Company: "Acme", Title: title, URL: "https://boards.greenhouse.io/acme/jobs/" + string(rune('1'+i)), Status: "new"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, j.ID)
	}
	now := time.Now().UTC()
	results := func(at time.Time) map[string]string {
		checks, err := Verify(context.Background(), s, o, nil, at)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, c := range checks {
			out[c.Title] = c.Result
		}
		return out
	}
	if r := results(now); r["Live"] != "live" || r["Gone"] != "gone (1 of 3)" || r["Flaky"] != "unchecked" {
		t.Fatalf("first pass: %v", r)
	}
	if r := results(now.Add(time.Hour)); r["Gone"] != "" {
		t.Errorf("rechecked within the day: %v", r)
	}
	results(now.Add(25 * time.Hour))
	if j, _ := s.JobByID(ids[1]); j.Status != "new" {
		t.Errorf("skipped after two checks in one day: %s", j.Status)
	}
	if r := results(now.Add(50 * time.Hour)); r["Gone"] != "closed" {
		t.Fatalf("third check two days on: %v", r)
	}
	if j, _ := s.JobByID(ids[1]); j.Status != "skipped" {
		t.Errorf("closed posting status: %s", j.Status)
	}
	if j, _ := s.JobByID(ids[2]); j.Status != "new" {
		t.Errorf("network errors skipped a job: %s", j.Status)
	}

	open404 = true
	results(now.Add(51 * time.Hour))
	open404 = false
	if r := results(now.Add(80 * time.Hour)); r["Live"] != "live" {
		t.Fatalf("back open: %v", r)
	}
	if n, _ := goneSince(s, ids[0]); n != 0 {
		t.Errorf("live check did not clear the failures: %d", n)
	}
}

func TestVerifyChecksBoardPagesByStatus(t *testing.T) {
	s := open(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/job/gone" {
			w.WriteHeader(http.StatusGone)
		}
	}))
	t.Cleanup(srv.Close)
	for _, path := range []string{"/job/open", "/job/gone"} {
		if _, _, err := s.UpsertJob(store.Job{Company: "Board", Title: path, URL: srv.URL + path, Status: "ranked"}); err != nil {
			t.Fatal(err)
		}
	}
	checks, err := Verify(context.Background(), s, Options{}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range checks {
		got[c.Title] = c.Result
	}
	if got["/job/open"] != "live" || got["/job/gone"] != "gone (1 of 3)" {
		t.Errorf("board page checks: %v", got)
	}
}
