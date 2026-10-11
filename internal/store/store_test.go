package store

import "testing"

func open(t *testing.T) *Store {
	t.Helper()
	t.Setenv("SCOUTJOBS_HOME", t.TempDir())
	s, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestUpsertAndResolve(t *testing.T) {
	s := open(t)
	j, created, err := s.UpsertJob(Job{Company: "Acme Corp", Title: "Senior Go Engineer", URL: "https://x"})
	if err != nil || !created {
		t.Fatalf("first upsert: %v created=%v", err, created)
	}
	if j.Key != "acme-corp|senior-go-engineer" || j.Status != "new" || j.FitScore != -1 {
		t.Errorf("defaults: %+v", j)
	}
	_, created, _ = s.UpsertJob(Job{Company: "ACME corp", Title: "senior go engineer"})
	if created {
		t.Error("duplicate created a second row")
	}
	for _, ref := range []string{"1", j.Key, "Acme Corp/Senior Go Engineer", "acme"} {
		if got, err := s.ResolveJob(ref); err != nil || got.ID != j.ID {
			t.Errorf("resolve %q: %v %+v", ref, err, got)
		}
	}
	if _, err := s.ResolveJob("nope"); err == nil {
		t.Error("resolve unknown should fail")
	}
}

func TestStatusEventsAndStats(t *testing.T) {
	s := open(t)
	j, _, _ := s.UpsertJob(Job{Company: "A", Title: "T"})
	st, score := "applied", int64(75)
	if err := s.UpdateJob(j.ID, JobPatch{Status: &st, FitScore: &score}); err != nil {
		t.Fatal(err)
	}
	bad := "bogus"
	if err := s.UpdateJob(j.ID, JobPatch{Status: &bad}); err == nil {
		t.Error("invalid status accepted")
	}
	ev, _ := s.Events(j.ID, 10)
	if len(ev) < 2 || ev[0].Kind != "scored" && ev[0].Kind != "status" {
		t.Errorf("events: %+v", ev)
	}
	stats, err := s.Stats(10, 60)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Total != 1 || stats.Open != 1 || stats.AppliedWeek != 1 || stats.AppliedTotal != 1 || stats.AvgFit != 75 || stats.Streak != 1 {
		t.Errorf("stats: %+v", stats)
	}
	st = "rejected"
	_ = s.UpdateJob(j.ID, JobPatch{Status: &st})
	stats, _ = s.Stats(10, 60)
	if stats.Open != 0 || stats.ResponseRate != 1 {
		t.Errorf("after rejection: %+v", stats)
	}
}

func TestDecisionSummary(t *testing.T) {
	s := open(t)
	j, _, _ := s.UpsertJob(Job{Company: "A", Title: "T"})
	score := int64(90)
	_ = s.UpdateJob(j.ID, JobPatch{FitScore: &score})
	_ = s.AddDecision(j.ID, "skip", "onsite only", `{"remote":"onsite","stack":["java","spring"]}`)
	_ = s.AddDecision(0, "skip", "too junior", `{"seniority":"junior"}`)
	ds, err := s.DecisionSummary(100)
	if err != nil {
		t.Fatal(err)
	}
	if ds.Counts["skip"] != 2 {
		t.Errorf("counts: %+v", ds.Counts)
	}
	sig := ds.Signals["skip"]
	if sig["remote=onsite"] != 1 || sig["stack:java=1"] != 1 || sig["seniority=junior"] != 1 {
		t.Errorf("signals: %+v", sig)
	}
	if len(ds.ScoreGap) != 1 || ds.ScoreGap[0].FitScore != 90 {
		t.Errorf("score gap: %+v", ds.ScoreGap)
	}
}

func TestPreferencesAndLearning(t *testing.T) {
	s := open(t)
	_ = s.SetPreference(Preference{Key: "remote", Value: "remote", Confidence: 0.6, Source: "learned"})
	_ = s.SetPreference(Preference{Key: "remote", Value: "remote only", Confidence: 0.9, Source: "stated"})
	prefs, _ := s.Preferences()
	if len(prefs) != 1 || prefs[0].Value != "remote only" {
		t.Errorf("pref upsert: %+v", prefs)
	}
	_ = s.UpsertLearning(Learning{Skill: "Kubernetes", Priority: 1})
	_ = s.UpsertLearning(Learning{Skill: "kubernetes", Demand: 4})
	items, _ := s.LearningItems()
	if len(items) != 1 || items[0].Demand != 4 {
		t.Errorf("learning upsert: %+v", items)
	}
	h := 1.5
	_ = s.UpdateLearning(items[0].ID, LearningPatch{Hours: &h})
	_ = s.UpdateLearning(items[0].ID, LearningPatch{Hours: &h})
	items, _ = s.LearningItems()
	if items[0].Hours != 3 {
		t.Errorf("hours additive: %v", items[0].Hours)
	}
}

func TestListDeleteAndCounts(t *testing.T) {
	s := open(t)
	a, _, _ := s.UpsertJob(Job{Company: "Acme", Title: "Go Engineer"})
	b, _, _ := s.UpsertJob(Job{Company: "Beta", Title: "Rust Engineer"})
	rejected := "rejected"
	if err := s.UpdateJob(b.ID, JobPatch{Status: &rejected}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		f    JobFilter
		want int
	}{
		{JobFilter{}, 2}, {JobFilter{Open: true}, 1}, {JobFilter{Status: "rejected"}, 1},
		{JobFilter{Query: "rust"}, 1}, {JobFilter{Query: "nobody"}, 0}, {JobFilter{Limit: 1}, 1},
	}
	for _, c := range cases {
		if got, err := s.ListJobs(c.f); err != nil || len(got) != c.want {
			t.Errorf("%+v: %d %v", c.f, len(got), err)
		}
	}
	if err := s.AddEvent(a.ID, "status", "new -> applied"); err != nil {
		t.Fatal(err)
	}
	if n := s.WeeklyCounts("status", "%applied", 2); len(n) != 2 || n[1] != 1 {
		t.Errorf("weekly counts: %v", n)
	}
	if err := s.DeleteJob(b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.JobByID(b.ID); err == nil {
		t.Error("deleted job still found")
	}
	if !KnownCurrency("eur") || KnownCurrency("XYZ") {
		t.Error("KnownCurrency")
	}
}

func TestInterviewsSessionsCVChecks(t *testing.T) {
	s := open(t)
	j, _, _ := s.UpsertJob(Job{Company: "Acme", Title: "Go Engineer"})
	id, err := s.AddInterview(Interview{JobID: j.ID, Stage: "phone", At: "2099-01-01 10:00", Format: "video"})
	if err != nil {
		t.Fatal(err)
	}
	up, _ := s.Interviews(0, true)
	if len(up) != 1 || up[0].Company != "Acme" || up[0].ID != id {
		t.Errorf("upcoming: %+v", up)
	}
	outcome := "passed"
	if err := s.UpdateInterview(id, InterviewPatch{Outcome: &outcome}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateInterview(id, InterviewPatch{}); err != nil {
		t.Errorf("empty patch: %v", err)
	}
	if up, _ := s.Interviews(j.ID, true); len(up) != 0 {
		t.Error("interview with an outcome is not upcoming")
	}
	if all, _ := s.Interviews(j.ID, false); len(all) != 1 || all[0].Outcome != "passed" {
		t.Errorf("all: %+v", all)
	}

	if err := s.AddSession(Session{Kind: "mock", Topic: "system design"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddSession(Session{Kind: "mock", Topic: "coding", Score: 7, Notes: "ok"}); err != nil {
		t.Fatal(err)
	}
	sess, _ := s.Sessions(1)
	if len(sess) != 1 || sess[0].Topic != "coding" {
		t.Errorf("sessions limit: %+v", sess)
	}
	if sess, _ = s.Sessions(0); len(sess) != 2 || sess[1].Score != -1 {
		t.Errorf("unscored session keeps -1: %+v", sess)
	}

	if _, err := s.AddCVCheck(CVCheck{File: "cv.md", Score: 60, ATSScore: 55}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddCVCheck(CVCheck{File: "resume.md", Score: 80, ATSScore: 90, JobID: j.ID, Gaps: "none"}); err != nil {
		t.Fatal(err)
	}
	checks, _ := s.CVChecks(10)
	if len(checks) != 2 || checks[0].JobID != j.ID || checks[1].JobID != 0 {
		t.Errorf("checks newest first: %+v", checks)
	}
}

func TestPreferencesAndMail(t *testing.T) {
	s := open(t)
	if err := s.SetPreference(Preference{Key: "remote", Value: "yes"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePreference("remote"); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.Preferences(); len(p) != 0 {
		t.Errorf("preference not deleted: %+v", p)
	}
	j, _, _ := s.UpsertJob(Job{Company: "Acme", Title: "Go Engineer"})
	if s.MailSeen("<a@b>") {
		t.Error("unknown mail seen")
	}
	if err := s.RecordMail("<a@b>", j.ID, "out", 0); err != nil {
		t.Fatal(err)
	}
	if id, ok := s.MailJob("<a@b>"); !ok || id != j.ID {
		t.Errorf("mail job: %d %v", id, ok)
	}
	if s.MailSeen("<a@b>") {
		t.Error("outbound mail is not an inbox message")
	}
	if err := s.RecordMail("<r@b>", j.ID, "in", 7); err != nil {
		t.Fatal(err)
	}
	if !s.MailSeen("<r@b>") {
		t.Error("recorded inbox mail not seen")
	}
}
