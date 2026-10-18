package apply

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zoolcoder/scoutjobs/internal/store"
)

func TestRank(t *testing.T) {
	s, j := testJob(t)
	os.WriteFile(filepath.Join(s.Home, "profile.md"), []byte("- Skills: Go, SQL\n"), 0o600)
	agent, _, err := s.UpsertJob(store.Job{Company: "Beta", Title: "Go Engineer", FitScore: 80, FitNotes: "strong match", Status: "ranked"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := Rank(s, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Job.ID != j.ID {
		t.Fatalf("ranked %+v, want only the unscored job", out)
	}
	got, _ := s.JobByID(j.ID)
	if got.Status != "ranked" || got.FitScore < 0 || !strings.HasPrefix(got.FitNotes, QuickPrefix) {
		t.Errorf("job after rank: %s %d %q", got.Status, got.FitScore, got.FitNotes)
	}
	if kept, _ := s.JobByID(agent.ID); kept.FitScore != 80 || kept.FitNotes != "strong match" {
		t.Errorf("agent score overwritten: %d %q", kept.FitScore, kept.FitNotes)
	}
	// a quick score is redone on the next pass, an agent score never
	if again, _ := Rank(s, true); len(again) != 1 {
		t.Errorf("rerank: %d", len(again))
	}
}
