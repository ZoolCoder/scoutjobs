package apply

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zoolcoder/scoutjobs/internal/store"
)

func TestQuestions(t *testing.T) {
	text := "Process was 3 rounds.\n- Why do you want to work here?\n2. Tell me about a conflict with a peer? Then a take-home.\nThey asked: how would you design a rate limiter?\nWhy?\n- why do you want to work here?"
	got := Questions(text)
	want := []string{"Why do you want to work here?", "Tell me about a conflict with a peer?", "how would you design a rate limiter?"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("questions:\n got %q\nwant %q", got, want)
	}
}

func TestSavePaste(t *testing.T) {
	s, j := testJob(t)
	if _, err := SavePaste(s, j, Paste{Source: "Glassdoor", Text: "  "}); err == nil {
		t.Error("empty paste saved")
	}
	n, err := SavePaste(s, j, Paste{Source: "Glassdoor", URL: "javascript:alert(1)", Text: "Friendly team.\nWhat is your biggest weakness?"})
	if err != nil || n != 1 {
		t.Fatalf("paste: %d %v", n, err)
	}
	if n, _ = SavePaste(s, j, Paste{Source: "Blind", URL: "https://blind.test/x", Text: "What is your biggest weakness?"}); n != 0 {
		t.Errorf("repeat question counted as new: %d", n)
	}
	notes := CompanyFile(s, j.Company, CompanyNotes)
	for _, want := range []string{"## ", "Glassdoor", "Friendly team.", "Blind · <https://blind.test/x>"} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes lack %q:\n%s", want, notes)
		}
	}
	if strings.Contains(notes, "javascript:") {
		t.Error("a non-web link was kept")
	}
	qs, _ := s.Questions(j.Company)
	if len(qs) != 1 || qs[0].Source != "reported" {
		t.Errorf("bank: %+v", qs)
	}
}

func TestRecordIntelAndQuestions(t *testing.T) {
	s, j := testJob(t)
	dir := Dir(s, j)
	_ = os.MkdirAll(dir, 0o755)
	if ok, _ := RecordIntel(s, j); ok {
		t.Error("intel recorded without a file")
	}
	_ = os.WriteFile(filepath.Join(dir, IntelName), []byte("# Acme\n\nSells anvils.\n"), 0o600)
	if ok, err := RecordIntel(s, j); !ok || err != nil {
		t.Fatalf("record intel: %v %v", ok, err)
	}
	if !strings.Contains(CompanyFile(s, j.Company, CompanyIntel), "Sells anvils.") {
		t.Error("intel not kept on the company")
	}
	b, _ := json.Marshal([]map[string]string{{"text": "Design a URL shortener?", "kind": "technical"}, {"text": " "}})
	_ = os.WriteFile(filepath.Join(dir, QuestionsName), b, 0o600)
	if _, err := RecordQuestions(s, j); err != nil {
		t.Fatal(err)
	}
	qs, _ := s.Questions(j.Company)
	if len(qs) != 1 || qs[0].Kind != "technical" || qs[0].Source != "prep" {
		t.Errorf("prep questions: %+v", qs)
	}
}

func TestMockFlow(t *testing.T) {
	s, j := testJob(t)
	if _, err := StartMock(s, j); err == nil {
		t.Error("mock with an empty bank")
	}
	for _, q := range []string{"Q one?", "Q two?", "Q three?", "Q four?", "Q five?", "Q six?"} {
		_, _ = s.AddQuestion(j, store.Question{Text: q})
	}
	qs, _ := s.Questions(j.Company)
	_ = s.RateQuestion(qs[0].ID, true, 4)
	m, err := StartMock(s, j)
	if err != nil || m.N != 1 || len(m.Questions) != MockSize {
		t.Fatalf("start: %+v %v", m, err)
	}
	for _, q := range m.Questions {
		if q.ID == qs[0].ID {
			t.Error("a rehearsed question was picked before the unrehearsed ones")
		}
	}
	dir := Dir(s, j)
	if ok, _ := MockScored(dir); ok {
		t.Error("unanswered mock counts as scored")
	}
	first := m.Questions[0].ID
	if err := SaveAnswers(dir, 1, map[int64]string{first: " I led the migration. "}); err != nil {
		t.Fatal(err)
	}
	if m, _ = LoadMock(dir, 1); m.Answered() != 1 || m.Questions[0].Answer != "I led the migration." {
		t.Fatalf("answers: %+v", m.Questions)
	}
	if ok, name := MockScored(dir); ok || name != MockResultName(1) {
		t.Errorf("before scoring: %v %s", ok, name)
	}
	res := MockResult{Overall: 3, Summary: "Clear but short.", Answers: []MockRating{{ID: first, Rating: 2, Note: "Add a number."}, {ID: 9999, Rating: 5}}}
	b, _ := json.Marshal(res)
	_ = os.WriteFile(filepath.Join(dir, MockResultName(1)), b, 0o600)
	if ok, _ := MockScored(dir); !ok {
		t.Error("scored mock not done")
	}
	if m, _ = LoadMock(dir, 1); m.Rating(first).Rating != 2 || m.Rating(first).Note != "Add a number." {
		t.Errorf("rating: %+v", m.Rating(first))
	}
	if err := SaveAnswers(dir, 1, nil); err == nil {
		t.Error("answers changed after scoring")
	}
	events, _ := s.Events(j.ID, 50)
	if n, err := RecordMock(s, j, events); n != 1 || err != nil {
		t.Fatalf("record: %d %v", n, err)
	}
	events, _ = s.Events(j.ID, 50)
	if n, _ := RecordMock(s, j, events); n != 0 {
		t.Error("mock recorded twice")
	}
	sessions, _ := s.Sessions(0)
	if len(sessions) != 1 || sessions[0].Kind != MockKind || sessions[0].Score != 60 {
		t.Errorf("session: %+v", sessions)
	}
	if q, _ := s.QuestionByID(first); !q.Rehearsed || q.Rating != 2 {
		t.Errorf("bank not rated: %+v", q)
	}
	if m2, _ := StartMock(s, j); m2.N != 2 {
		t.Errorf("next mock number: %d", m2.N)
	}
}
