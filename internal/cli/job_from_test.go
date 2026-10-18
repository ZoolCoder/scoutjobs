package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zoolcoder/scoutjobs/internal/search"
)

func TestJobAddFrom(t *testing.T) {
	t.Setenv("SCOUTJOBS_HOME", t.TempDir())
	run(t, "init")
	old := fetchPosting
	defer func() { fetchPosting = old }()
	fetchPosting = func(_ context.Context, u string) (search.Posting, error) {
		if !strings.Contains(u, "greenhouse") {
			return search.Posting{}, errors.New("no known ATS on example.com")
		}
		return search.Posting{Company: "acme", Title: "Staff Go Engineer", URL: "https://boards.greenhouse.io/acme/jobs/1", Location: "Berlin", Remote: "hybrid", Source: "greenhouse", PostedAt: "2026-10-01", Tags: "go", Description: "Build services in Go"}, nil
	}
	out := run(t, "job", "add", "--from", "https://boards.greenhouse.io/acme/jobs/1", "--company", "Acme GmbH")
	if !strings.Contains(out, "added") || !strings.Contains(out, "Acme GmbH / Staff Go Engineer") {
		t.Fatalf("add --from:\n%s", out)
	}
	show := run(t, "job", "show", "1", "--json")
	for _, want := range []string{`"location":"Berlin"`, `"remote":"hybrid"`, `"source":"greenhouse"`, `"posted_at":"2026-10-01"`, `"notes":"Build services in Go"`, `"url":"https://boards.greenhouse.io/acme/jobs/1"`} {
		if !strings.Contains(show, want) {
			t.Errorf("job lacks %s:\n%s", want, show)
		}
	}
	if err := Run([]string{"job", "add", "--from", "https://example.com/job/1"}, &strings.Builder{}); err == nil || !strings.Contains(err.Error(), "no known ATS") {
		t.Errorf("unsupported url: %v", err)
	}
}
