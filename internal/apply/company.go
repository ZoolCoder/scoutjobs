package apply

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zoolcoder/scoutjobs/internal/store"
)

// File names of the company folder and the application folder.
const (
	CompanyNotes  = "notes.md"         // pasted reviews and debrief notes, newest last
	CompanyIntel  = "intel.md"         // the research the assistant wrote, kept for the next job there
	IntelName     = "company-intel.md" // where the assistant writes the research in the application folder
	QuestionsName = "questions.json"   // likely questions the prep step lists
	OfferName     = "offer-prep.md"    // the offer step's negotiation pack
	maxPaste      = 64 << 10           // pasted text is a page of reviews, not a dump
	maxQuestions  = 60                 // questions taken from one paste
)

// CompanyDir is the folder ScoutJobs keeps on a company, shared by every job there.
func CompanyDir(s *store.Store, company string) string {
	return filepath.Join(s.Home, "companies", store.CompanyKey(company))
}

// Paste is text the user copied from a review site or their notes.
type Paste struct {
	Source string // e.g. Glassdoor, Blind, a friend
	URL    string
	Text   string
}

// SavePaste appends pasted text to the company notes and files the questions in it. It returns
// how many questions were new to the bank. ScoutJobs never fetches the page itself.
func SavePaste(s *store.Store, j store.Job, p Paste) (int, error) {
	text := strings.TrimSpace(strings.ReplaceAll(p.Text, "\r\n", "\n"))
	if text == "" {
		return 0, errors.New("paste some text first")
	}
	if len(text) > maxPaste || !utf8.ValidString(text) {
		return 0, fmt.Errorf("paste at most %d KB of plain text", maxPaste>>10)
	}
	source := strings.Join(strings.Fields(p.Source), " ")
	if source == "" {
		source = "Pasted notes"
	}
	head := source
	if u := strings.TrimSpace(p.URL); strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://") {
		head += " · <" + u + ">"
	}
	if err := AddCompanyNote(s, j.Company, head, text); err != nil {
		return 0, err
	}
	added := 0
	for _, q := range Questions(text) {
		if ok, _ := s.AddQuestion(j, store.Question{Text: q, Source: "reported"}); ok {
			added++
		}
	}
	return added, nil
}

// AddCompanyNote appends a dated section to the company notes.
func AddCompanyNote(s *store.Store, company, heading, body string) error {
	dir := CompanyDir(s, company)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, CompanyNotes)
	var b strings.Builder
	if _, err := os.Stat(path); err != nil {
		fmt.Fprintf(&b, "# %s\n\nNotes you collected on %s: pasted reviews and interview debriefs.\n", company, company)
	}
	fmt.Fprintf(&b, "\n## %s · %s\n\n%s\n", time.Now().Format("2006-01-02"), heading, strings.TrimSpace(body))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = f.WriteString(b.String())
	return errors.Join(err, f.Close())
}

// Questions picks the interview questions out of pasted text: the lines, or sentences, that end in
// a question mark and are long enough to be one.
func Questions(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "-*•>0123456789.) \t"))
		for _, part := range strings.SplitAfter(line, "?") {
			q := strings.TrimSpace(part)
			if i := strings.LastIndexAny(strings.TrimSuffix(q, "?"), ".!:"); i >= 0 {
				q = strings.TrimSpace(q[i+1:])
			}
			q = strings.Trim(q, `"“”'`)
			if !strings.HasSuffix(q, "?") || len(strings.Fields(q)) < 3 || seen[strings.ToLower(q)] {
				continue
			}
			seen[strings.ToLower(q)] = true
			out = append(out, q)
			if len(out) == maxQuestions {
				return out
			}
		}
	}
	return out
}

// CompanyFile reads a file of the company folder; empty when there is none.
func CompanyFile(s *store.Store, company, name string) string {
	b, _ := os.ReadFile(filepath.Join(CompanyDir(s, company), name))
	return string(b)
}

// RecordIntel keeps the research the assistant wrote in the application folder on the company, so
// the next job there starts from it. It reports whether there was new research.
func RecordIntel(s *store.Store, j store.Job) (bool, error) {
	src := filepath.Join(Dir(s, j), IntelName)
	b, err := os.ReadFile(src)
	if err != nil {
		return false, nil
	}
	dst := filepath.Join(CompanyDir(s, j.Company), CompanyIntel)
	if old, err := os.ReadFile(dst); err == nil && string(old) == string(b) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(dst, b, 0o600)
}

// RecordQuestions files the likely questions the prep step listed in questions.json.
func RecordQuestions(s *store.Store, j store.Job) (int, error) {
	b, err := os.ReadFile(filepath.Join(Dir(s, j), QuestionsName))
	if err != nil {
		return 0, nil
	}
	var list []struct {
		Text string `json:"text"`
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(b, &list); err != nil {
		return 0, fmt.Errorf("%s: %w", QuestionsName, err)
	}
	added := 0
	for _, q := range list[:min(len(list), maxQuestions)] {
		if ok, _ := s.AddQuestion(j, store.Question{Text: q.Text, Kind: q.Kind, Source: "prep"}); ok {
			added++
		}
	}
	return added, nil
}
