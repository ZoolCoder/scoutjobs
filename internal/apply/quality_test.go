package apply

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/store"
)

func TestScore(t *testing.T) {
	r := Report{Gates: []Gate{{State: Pass}, {State: Pass}, {State: Warn}, {State: Skip}}}
	if got := r.Score(); got != 83 {
		t.Errorf("2 pass + 1 warn: %d, want 83", got)
	}
	r.Gates = append(r.Gates, Gate{State: Fail})
	r.Failed = 1
	if got := r.Score(); got != 60 {
		t.Errorf("a failure caps the score: %d", got)
	}
	if (Report{}).Score() != 0 {
		t.Error("no gates")
	}
}

func TestGateClaims(t *testing.T) {
	home, dir := t.TempDir(), t.TempDir()
	drafts := map[string]string{"resume.md": "Cut p99 latency by 40% for 2,000,000 users in 2023. Led 6 engineers."}
	if g := gateClaims(home, dir, drafts); g.State != Skip {
		t.Fatalf("no corpus: %s", g.State)
	}
	_ = os.WriteFile(filepath.Join(home, "profile.md"), []byte("- Strong: Go\nCut latency 40% for 2000000 users."), 0o600)
	g := gateClaims(home, dir, drafts)
	if g.State != Pass {
		t.Fatalf("traced figures: %s %v", g.State, g.Items)
	}
	drafts["cover-letter.md"] = "I saved $300k a year and served 15 teams."
	g = gateClaims(home, dir, drafts)
	if g.State != Fail || len(g.Items) != 2 || !strings.Contains(g.Items[0], "300") || !strings.Contains(g.Items[0], "cover-letter.md") {
		t.Fatalf("untraced claims: %s %v", g.State, g.Items)
	}
	_ = os.MkdirAll(filepath.Join(home, "stories"), 0o755)
	_ = os.WriteFile(filepath.Join(home, "stories", "savings.md"), []byte("Saved 300k, 15 teams"), 0o600)
	if g = gateClaims(home, dir, drafts); g.State != Pass {
		t.Errorf("stories back the claim: %s %v", g.State, g.Items)
	}
}

func TestGateSpecific(t *testing.T) {
	j := store.Job{Company: "Acme Corp, Inc.", Title: "Go Engineer"}
	review := FitReview{Requirements: []Requirement{{Requirement: "Postgres tuning"}}}
	if g := gateSpecific(j, review, map[string]string{}); g.State != Skip {
		t.Error("no letter")
	}
	if g := gateSpecific(j, review, map[string]string{"cover-letter.md": "Dear team, I like code."}); g.State != Warn || len(g.Items) != 2 {
		t.Errorf("generic letter: %s %v", g.State, g.Items)
	}
	if g := gateSpecific(j, review, map[string]string{"cover-letter.md": "Dear Acme Corp team, I did Postgres tuning for years."}); g.State != Pass {
		t.Errorf("specific letter: %s %s", g.State, g.Detail)
	}
}

func TestGatePreferencesAndRepeat(t *testing.T) {
	s, j := testJob(t)
	if g := gatePreferences(s, j, Dir(s, j)); g.State != Skip {
		t.Errorf("no profile: %s", g.State)
	}
	_ = os.WriteFile(filepath.Join(s.Home, "profile.md"), []byte("- Roles: Go engineer\n- Salary: 90000 EUR\n"), 0o600)
	low := int64(60000)
	cur := "EUR"
	_ = s.UpdateJob(j.ID, store.JobPatch{SalaryMax: &low, Currency: &cur})
	j, _ = s.JobByID(j.ID)
	if g := gatePreferences(s, j, Dir(s, j)); g.State != Warn || !strings.Contains(g.Detail, "floor") {
		t.Errorf("salary below floor: %s %s", g.State, g.Detail)
	}

	now := time.Now()
	if g := gateRepeat(s, j, now); g.State != Pass {
		t.Errorf("first application: %s", g.State)
	}
	o, _, _ := s.UpsertJob(store.Job{Company: "ACME Corp", Title: "Platform Engineer"})
	st := "applied"
	_ = s.UpdateJob(o.ID, store.JobPatch{Status: &st})
	g := gateRepeat(s, j, now)
	if g.State != Warn || len(g.Items) != 1 || !strings.Contains(g.Items[0], "Platform Engineer") {
		t.Errorf("recent application to the same company: %s %v", g.State, g.Items)
	}
	if g := gateRepeat(s, j, now.AddDate(0, 0, RepeatDays+1)); g.State != Pass {
		t.Errorf("older than %d days: %s", RepeatDays, g.State)
	}
}
