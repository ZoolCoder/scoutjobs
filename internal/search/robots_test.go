package search

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestParseRobots(t *testing.T) {
	txt := `User-agent: *
Disallow: /private
Allow: /private/jobs$
Disallow: /*?ref=

User-agent: otherbot
Disallow: /
`
	cases := map[string]bool{"/jobs/1": true, "/private/x": false, "/private/jobs": true, "/private/jobs/2": false, "/jobs/1?ref=x": false}
	rules := parseRobots(strings.NewReader(txt))
	for p, want := range cases {
		robotsCache.Store("https://t.example", robotsEntry{rules: rules, at: time.Now()})
		u, _ := url.Parse("https://t.example" + p)
		if got := robotsAllowed(context.Background(), nil, u) == nil; got != want {
			t.Errorf("%s: allowed %v, want %v", p, got, want)
		}
	}
	if r := parseRobots(strings.NewReader("User-agent: scoutjobs\nDisallow: /\n\nUser-agent: *\nDisallow:\n")); len(r) != 1 || r[0].allow {
		t.Errorf("own group not preferred: %+v", r)
	}
}

func TestPostingLiveHonoursRobots(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			w.Write([]byte("User-agent: *\nDisallow: /secret\n"))
			return
		}
		if r.URL.Path == "/secret/job" {
			t.Error("fetched a disallowed page")
		}
	}))
	defer srv.Close()
	if err := PostingLive(context.Background(), srv.URL+"/secret/job", srv.Client(), nil); !errors.Is(err, ErrRobots) {
		t.Errorf("disallowed page: %v", err)
	}
	if err := PostingLive(context.Background(), srv.URL+"/open/job", srv.Client(), nil); err != nil {
		t.Errorf("allowed page: %v", err)
	}
}
