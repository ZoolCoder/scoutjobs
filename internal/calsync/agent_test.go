package calsync

import "testing"

func TestAgentEvents(t *testing.T) {
	s := open(t)
	n, err := SaveAgentEvents(s, `[{"id":"e1","title":"Flight to Berlin","start":"2026-10-14T09:30","end":"2026-10-14T11:00","location":"CAI"},
		{"title":"Holiday","start":"2026-10-20","all_day":true},{"title":"no time","start":"soon"}]`)
	if err != nil || n != 2 {
		t.Fatalf("saved %d: %v", n, err)
	}
	evs := LoadAgentEvents(s)
	if len(evs) != 2 || evs[0].ID != "e1" || evs[0].End.Sub(evs[0].Start).Minutes() != 90 || evs[0].Location != "CAI" {
		t.Errorf("timed event: %+v", evs)
	}
	if !evs[1].AllDay || evs[1].ID == "" || evs[1].End.Sub(evs[1].Start).Hours() != 24 {
		t.Errorf("all-day event: %+v", evs[1])
	}
	if _, err := SaveAgentEvents(s, "not json"); err == nil {
		t.Error("bad JSON accepted")
	}
}
