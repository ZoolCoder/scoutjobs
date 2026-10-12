package store

import "testing"

func TestQuestionBank(t *testing.T) {
	s := open(t)
	j, _, _ := s.UpsertJob(Job{Company: "Stripe, Inc.", Title: "Engineer"})
	other, _, _ := s.UpsertJob(Job{Company: "stripe", Title: "Platform"})
	if ok, err := s.AddQuestion(j, Question{Text: "  Tell me about   a failure. ", Source: "reported", Kind: "behavioral"}); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, _ := s.AddQuestion(other, Question{Text: "Tell me about a failure.", Source: "debrief"}); ok {
		t.Error("the same question twice for one company")
	}
	_, _ = s.AddQuestion(j, Question{Text: "Design a rate limiter", Source: "bogus", Kind: "bogus"})
	qs, err := s.Questions("Stripe")
	if err != nil || len(qs) != 2 {
		t.Fatalf("bank: %v %v", qs, err)
	}
	if qs[1].Source != "manual" || qs[1].Kind != "" {
		t.Errorf("unknown source and kind: %+v", qs[1])
	}
	_ = s.RateQuestion(qs[0].ID, true, 4)
	_ = s.RateQuestion(qs[0].ID, true, 0)
	q, _ := s.QuestionByID(qs[0].ID)
	if !q.Rehearsed || q.Rating != 4 {
		t.Errorf("rated: %+v", q)
	}
	qs, _ = s.Questions("stripe")
	if done, total := Rehearsed(qs); done != 1 || total != 2 || qs[0].Rehearsed {
		t.Errorf("rehearsed %d/%d, unrehearsed first: %+v", done, total, qs)
	}
	_ = s.DeleteQuestion(q.ID)
	if qs, _ = s.Questions("stripe"); len(qs) != 1 {
		t.Error("delete")
	}
}

func TestInterviewDebrief(t *testing.T) {
	s := open(t)
	j, _, _ := s.UpsertJob(Job{Company: "Acme", Title: "Engineer"})
	id, _ := s.AddInterview(Interview{JobID: j.ID, Stage: "onsite", At: "2026-09-01T10:00"})
	asked, well, weak := "Why us?\nDesign a cache", "System design", "Salary question"
	if err := s.UpdateInterview(id, InterviewPatch{Asked: &asked, WentWell: &well, Weak: &weak}); err != nil {
		t.Fatal(err)
	}
	ivs, _ := s.Interviews(j.ID, false)
	if ivs[0].Asked != asked || ivs[0].WentWell != well || ivs[0].Weak != weak {
		t.Errorf("debrief: %+v", ivs[0])
	}
}
