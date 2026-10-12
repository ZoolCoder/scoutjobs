package apply

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/store"
)

// MockSize is how many questions a written mock interview asks.
const MockSize = 5

// MockKind is the practice session a scored mock is logged as.
const MockKind = "mock_written"

// Mock is a written mock interview: questions from the company's bank, the user's typed answers,
// and the assistant's scores once it read them.
type Mock struct {
	N         int          `json:"n"`
	Started   string       `json:"started"`
	Questions []MockAnswer `json:"questions"`
	Result    *MockResult  `json:"-"`
	Feedback  bool         `json:"-"` // mock-<n>-feedback.md exists
}

type MockAnswer struct {
	ID     int64  `json:"id"`
	Text   string `json:"text"`
	Answer string `json:"answer"`
}

// MockResult is what the mock step writes in mock-<n>-result.json.
type MockResult struct {
	Overall int    `json:"overall"` // 1-5
	Summary string `json:"summary"`
	Answers []MockRating `json:"answers"`
}

// MockRating is the assistant's verdict on one answer.
type MockRating struct {
	ID     int64  `json:"id"`
	Rating int    `json:"rating"` // 1-5
	Note   string `json:"note"`
}

// Answered counts the questions with an answer.
func (m Mock) Answered() int {
	n := 0
	for _, q := range m.Questions {
		if strings.TrimSpace(q.Answer) != "" {
			n++
		}
	}
	return n
}

// Rating is the assistant's rating of one answer; zero before scoring.
func (m Mock) Rating(id int64) MockRating {
	if m.Result != nil {
		for _, a := range m.Result.Answers {
			if a.ID == id {
				return a
			}
		}
	}
	return MockRating{}
}

func MockName(n int) string         { return fmt.Sprintf("mock-%d.json", n) }
func MockFeedbackName(n int) string { return fmt.Sprintf("mock-%d-feedback.md", n) }
func MockResultName(n int) string   { return fmt.Sprintf("mock-%d-result.json", n) }

// Mocks lists the mock interviews of the application folder, newest first.
func Mocks(dir string) []Mock {
	names, _ := filepath.Glob(filepath.Join(dir, "mock-*.json"))
	var out []Mock
	for _, p := range names {
		n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(filepath.Base(p), "mock-"), ".json"))
		if err != nil {
			continue
		}
		if m, err := LoadMock(dir, n); err == nil {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, k int) bool { return out[i].N > out[k].N })
	return out
}

// LoadMock reads one mock interview with its result, when scored.
func LoadMock(dir string, n int) (Mock, error) {
	var m Mock
	b, err := os.ReadFile(filepath.Join(dir, MockName(n)))
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, err
	}
	m.N = n
	if b, err := os.ReadFile(filepath.Join(dir, MockResultName(n))); err == nil {
		var r MockResult
		if json.Unmarshal(b, &r) == nil {
			m.Result = &r
		}
	}
	m.Feedback = exists(filepath.Join(dir, MockFeedbackName(n)))
	return m, nil
}

// StartMock picks up to MockSize questions from the company's bank, those not rehearsed yet first.
func StartMock(s *store.Store, j store.Job) (Mock, error) {
	qs, err := s.Questions(j.Company)
	if err != nil {
		return Mock{}, err
	}
	if len(qs) == 0 {
		return Mock{}, errors.New("the question bank is empty: add or paste questions first")
	}
	dir := Dir(s, j)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Mock{}, err
	}
	m := Mock{N: 1, Started: time.Now().UTC().Format(time.RFC3339)}
	if list := Mocks(dir); len(list) > 0 {
		m.N = list[0].N + 1
	}
	for _, q := range qs[:min(len(qs), MockSize)] {
		m.Questions = append(m.Questions, MockAnswer{ID: q.ID, Text: q.Text})
	}
	// a result left by a run without a mock file would score the new one
	for _, name := range []string{MockResultName(m.N), MockFeedbackName(m.N)} {
		_ = os.Remove(filepath.Join(dir, name))
	}
	return m, saveMock(dir, m)
}

// SaveAnswers keeps the typed answers, by question id.
func SaveAnswers(dir string, n int, answers map[int64]string) error {
	m, err := LoadMock(dir, n)
	if err != nil {
		return err
	}
	if m.Result != nil {
		return errors.New("this mock is scored already: start a new one")
	}
	for i, q := range m.Questions {
		if a, ok := answers[q.ID]; ok {
			m.Questions[i].Answer = strings.TrimSpace(a)
		}
	}
	return saveMock(dir, m)
}

func saveMock(dir string, m Mock) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, MockName(m.N)), b, 0o600)
}

// MockScored reports whether the newest answered mock has its result.
func MockScored(dir string) (bool, string) {
	for _, m := range Mocks(dir) {
		if m.Answered() == 0 {
			continue
		}
		return m.Result != nil, MockResultName(m.N)
	}
	return false, "an answered mock interview"
}

// mockEvent marks a scored mock as recorded, so a rerun does not log it twice.
const mockEvent = "mock:scored"

// RecordMock logs every newly scored mock as a practice session and rates its questions in the bank.
func RecordMock(s *store.Store, j store.Job, events []store.Event) (int, error) {
	done := map[string]bool{}
	for _, e := range events {
		if e.Kind == mockEvent {
			done[e.Note] = true
		}
	}
	recorded := 0
	for _, m := range Mocks(Dir(s, j)) {
		key := strconv.Itoa(m.N)
		if m.Result == nil || done[key] {
			continue
		}
		overall := min(max(m.Result.Overall, 0), 5)
		if err := s.AddSession(store.Session{Kind: MockKind, Topic: j.Company, Score: int64(overall * 20), Notes: m.Result.Summary}); err != nil {
			return recorded, err
		}
		for _, a := range m.Result.Answers {
			if m.has(a.ID) {
				_ = s.RateQuestion(a.ID, true, a.Rating)
			}
		}
		_ = s.AddEvent(j.ID, mockEvent, key)
		recorded++
	}
	return recorded, nil
}

func (m Mock) has(id int64) bool {
	for _, q := range m.Questions {
		if q.ID == id {
			return true
		}
	}
	return false
}
