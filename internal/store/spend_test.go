package store

import (
	"testing"
	"time"
)

func TestSpendSince(t *testing.T) {
	t.Setenv("SCOUTJOBS_HOME", t.TempDir())
	s, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.AddEvent(0, SpendEvent, "0.25 claude sonnet")
	s.AddEvent(0, SpendEvent, "0.50 claude haiku")
	s.AddEvent(0, SpendEvent, "garbled")
	s.AddEvent(0, "added", "1.00")
	sp, err := s.SpendSince("2000-01-01")
	if err != nil || sp.USD != 0.75 || sp.Sessions != 2 {
		t.Errorf("spend: %+v %v", sp, err)
	}
	if sp, _ := s.SpendSince("2999-01-01"); sp.Sessions != 0 {
		t.Errorf("future window: %+v", sp)
	}
}

func TestFollowUps(t *testing.T) {
	s := open(t)
	j, _, err := s.UpsertJob(Job{Company: "Acme", Title: "Go dev", Status: "applied"})
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().AddDate(0, 0, -20).Format(time.RFC3339)
	if _, err := s.DB.Exec(`UPDATE jobs SET updated_at=? WHERE id=?`, old, j.ID); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.Stats(10, 60); len(st.DueFollowUps) != 1 {
		t.Fatalf("due %d, want 1", len(st.DueFollowUps))
	}
	if n, err := s.FollowedUp(j.ID, ""); err != nil || n != 1 {
		t.Fatalf("first follow-up %d %v", n, err)
	}
	if st, _ := s.Stats(10, 60); len(st.DueFollowUps) != 0 {
		t.Error("a follow-up just sent is due again")
	}
	_, _ = s.FollowedUp(j.ID, "")
	_, _ = s.DB.Exec(`UPDATE jobs SET updated_at=? WHERE id=?`, old, j.ID)
	if st, _ := s.Stats(10, 60); len(st.DueFollowUps) != 0 {
		t.Error("a third follow-up falls due")
	}
	if _, err := s.FollowedUp(j.ID, ""); err == nil {
		t.Error("a third follow-up was logged")
	}
	_, _ = s.DB.Exec(`UPDATE jobs SET updated_at=? WHERE id=?`, time.Now().UTC().AddDate(0, 0, -70).Format(time.RFC3339), j.ID)
	st, _ := s.Stats(10, 60)
	if len(st.Stale) != 1 {
		t.Fatalf("stale %d, want 1", len(st.Stale))
	}
	if err := s.CloseStale(st.Stale); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.JobByID(j.ID); got.Status != "no_response" {
		t.Errorf("swept job is %s", got.Status)
	}
}
