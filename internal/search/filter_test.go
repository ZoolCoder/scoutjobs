package search

import (
	"net/url"
	"testing"
	"time"
)

func TestFilterKeep(t *testing.T) {
	now := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	f := FilterFromValues(url.Values{"remote_only": {"1"}, "days": {"7"}, "min_salary": {"80000"}, "exclude": {"PHP, intern"}})
	cases := []struct {
		p    Posting
		keep bool
	}{
		{Posting{Title: "Go Dev", Remote: "remote"}, true},
		{Posting{Title: "Go Dev", Remote: "onsite"}, false},
		{Posting{Title: "Senior PHP Dev", Remote: "remote"}, false},
		{Posting{Title: "Go Dev", Remote: "remote", PostedAt: "2026-09-01"}, false},
		{Posting{Title: "Go Dev", Remote: "remote", PostedAt: "2026-10-09T08:00:00Z"}, true},
		{Posting{Title: "Go Dev", Remote: "remote", SalaryMax: 60000}, false},
		{Posting{Title: "Go Dev", Remote: "remote", SalaryMax: 90000}, true},
	}
	for _, c := range cases {
		if got := f.Keep(c.p, now); got != c.keep {
			t.Errorf("%+v: keep %v, want %v", c.p, got, c.keep)
		}
	}
	if enc := f.Encode(); ParseFilter(enc) != f {
		t.Errorf("round trip %q", enc)
	}
}
