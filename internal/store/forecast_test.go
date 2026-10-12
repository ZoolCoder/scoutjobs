package store

import (
	"fmt"
	"testing"
	"time"
)

// history adds n applied jobs from source, answered answers of which led to an interview when
// interviewed is true.
func history(t *testing.T, s *Store, source string, n, answered, interviewed int) {
	t.Helper()
	for i := range n {
		j, _, err := s.UpsertJob(Job{Company: fmt.Sprintf("%s%d", source, i), Title: "Engineer", Source: source})
		if err != nil {
			t.Fatal(err)
		}
		path := []string{"applied"}
		switch {
		case i < interviewed:
			path = append(path, "interview")
		case i < answered:
			path = append(path, "rejected")
		}
		for _, st := range path {
			_ = s.UpdateJob(j.ID, JobPatch{Status: &st})
		}
	}
}

func TestChanceNeedsData(t *testing.T) {
	s := open(t)
	history(t, s, "lever", 10, 5, 2)
	f, _ := s.Funnel()
	if c := f.Chance("lever", 80); c.Enough || c.Answer != 0 || c.Basis != 10 {
		t.Errorf("10 applications: %+v", c)
	}
}

func TestChanceBySource(t *testing.T) {
	s := open(t)
	history(t, s, "lever", 20, 12, 6)
	history(t, s, "linkedin", 20, 2, 0)
	f, _ := s.Funnel()
	good, poor := f.Chance("lever", -1), f.Chance("linkedin", -1)
	if !good.Enough || good.Answer <= poor.Answer || good.Interview <= poor.Interview {
		t.Fatalf("lever %+v vs linkedin %+v", good, poor)
	}
	if good.Answer >= 60 || poor.Answer <= 10 {
		t.Errorf("small groups pull towards the overall 35%%: lever %d, linkedin %d", good.Answer, poor.Answer)
	}
	if good.Low > good.Answer || good.High < good.Answer {
		t.Errorf("range %d–%d around %d", good.Low, good.High, good.Answer)
	}
	if poor.Interview < 1 || poor.Interview > poor.Answer {
		t.Errorf("interview chance stays between 1%% and the answer chance: %+v", poor)
	}
	if unknown := f.Chance("indeed", -1); unknown.Answer < poor.Answer || unknown.Answer > good.Answer {
		t.Errorf("a new source gets the overall rate: %+v", unknown)
	}
}

func TestOutlook(t *testing.T) {
	s := open(t)
	history(t, s, "lever", 20, 10, 5)
	waiting := 10 // unanswered history
	for i := range 4 {
		j, _, _ := s.UpsertJob(Job{Company: fmt.Sprintf("Open%d", i), Title: "Engineer", Source: "lever"})
		st := "applied"
		_ = s.UpdateJob(j.ID, JobPatch{Status: &st})
		waiting++
	}
	f, _ := s.Funnel()
	o, err := s.Outlook(f, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if !o.Enough || o.Waiting != waiting || o.Expected <= 0 || o.Expected > float64(waiting) || o.Low > o.High {
		t.Fatalf("outlook %+v", o)
	}
	if o.ToApply != 5 || o.Rate != 20 {
		t.Errorf("5 interviews in 24 applications: %+v", o)
	}
	if o, _ = s.Outlook(f, time.Now().UTC().AddDate(0, 0, 60)); o.Waiting != 0 {
		t.Errorf("applications older than %d days no longer count: %+v", outlookWindow, o)
	}
}

func TestFunnelByQuality(t *testing.T) {
	s := open(t)
	add := func(company, note string, path ...string) {
		j, _, _ := s.UpsertJob(Job{Company: company, Title: "Engineer"})
		for _, st := range path {
			_ = s.UpdateJob(j.ID, JobPatch{Status: &st})
		}
		if note != "" {
			_ = s.AddEvent(j.ID, "quality", note)
		}
	}
	add("A", "pass score 92", "applied", "interview")
	add("B", "override score 55: figure from a side project", "applied", "rejected")
	add("C", "", "applied")
	f, _ := s.Funnel()
	if len(f.Quality) != 3 || f.Quality[0].Group != QualityGroups[0] || f.Quality[0].Interview != 1 || f.Quality[1].Group != QualityGroups[1] || f.Quality[2].Group != QualityGroups[3] {
		t.Fatalf("quality groups: %+v", f.Quality)
	}
}
