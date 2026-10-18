package apply

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/zoolcoder/scoutjobs/internal/match"
	"github.com/zoolcoder/scoutjobs/internal/search"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

// RepeatDays is how far back an application to the same company counts as recent.
const RepeatDays = 90

// Score is the readiness of the application, 0-100: a pass counts fully, a warning half, a failure
// nothing, and any failure caps the score at 60. Skipped gates do not count.
func (r Report) Score() int {
	counted, points := 0, 0
	for _, g := range r.Gates {
		switch g.State {
		case Pass:
			counted, points = counted+1, points+2
		case Warn:
			counted, points = counted+1, points+1
		case Fail:
			counted++
		}
	}
	if counted == 0 {
		return 0
	}
	score := points * 100 / (2 * counted)
	if r.Failed > 0 {
		score = min(score, 60)
	}
	return score
}

var (
	numberRe = regexp.MustCompile(`[$€£]?\d+(?:[.,]\d+)*\s?(?:%|×|x\b|\+|k\b|m\b|bn\b|ms\b)?`)
	digitsRe = regexp.MustCompile(`\d+(?:\.\d+)?`)
)

// claim is a number in a draft with the words around it.
type claim struct {
	value, file, context string
}

// numbers lists the figures of a text worth tracing: any with a unit, and plain ones of 10 or more
// that are not a year. Digits inside a name such as p99, K8s or S3 are not figures.
func numbers(file, text string) []claim {
	var out []claim
	for _, loc := range numberRe.FindAllStringIndex(text, -1) {
		if r, _ := utf8.DecodeLastRuneInString(text[:loc[0]]); unicode.IsLetter(r) {
			continue
		}
		raw := text[loc[0]:loc[1]]
		value := digitsRe.FindString(strings.ReplaceAll(raw, ",", ""))
		unit := strings.TrimSpace(strings.TrimLeft(raw, "$€£0123456789.,")) != "" || strings.ContainsAny(raw[:1], "$€£")
		if !unit && (len(value) < 2 || isYear(value)) {
			continue
		}
		lo, hi := max(loc[0]-40, 0), min(loc[1]+40, len(text))
		out = append(out, claim{value, file, strings.Join(strings.Fields(text[lo:hi]), " ")})
	}
	return out
}

func isYear(v string) bool { return len(v) == 4 && v >= "1950" && v <= "2049" }

// evidenceNumbers is every figure in the profile, the CVs, the stories, the posting and the fit review.
func evidenceNumbers(home, dir string) (map[string]bool, bool) {
	paths := []string{filepath.Join(home, "profile.md"), filepath.Join(dir, PostingName), filepath.Join(dir, ReviewName)}
	for _, pat := range []string{"cv/*.md", "cv/*.txt", "stories/*.md"} {
		m, _ := filepath.Glob(filepath.Join(home, pat))
		paths = append(paths, m...)
	}
	seen, found := map[string]bool{}, false
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if p != filepath.Join(dir, PostingName) && p != filepath.Join(dir, ReviewName) {
			found = true
		}
		for _, v := range digitsRe.FindAllString(strings.ReplaceAll(string(b), ",", ""), -1) {
			seen[v] = true
		}
	}
	return seen, found
}

// gateClaims traces every figure in the drafts to the profile, a CV, a story or the posting. An
// untraced number is a claim nobody can back in the interview, so it blocks sending.
func gateClaims(home, dir string, drafts map[string]string) Gate {
	if len(drafts) == 0 {
		return Gate{"claims", Skip, "no markdown drafts", nil}
	}
	known, ok := evidenceNumbers(home, dir)
	if !ok {
		return Gate{"claims", Skip, "no profile.md, CV or stories to trace the figures to", nil}
	}
	var loose []string
	names := make([]string, 0, len(drafts))
	for n := range drafts {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		for _, c := range numbers(n, drafts[n]) {
			if !known[c.value] {
				loose = append(loose, fmt.Sprintf("%s in %s: “…%s…”", c.value, c.file, c.context))
			}
		}
	}
	if len(loose) > 0 {
		return Gate{"claims", Fail, fmt.Sprintf("%d figure(s) not found in your profile, CV, stories or the posting", len(loose)), loose}
	}
	return Gate{"claims", Pass, "every figure traces to your profile, CV, stories or the posting", nil}
}

// gateSpecific warns about a cover letter that could go to any company: it should name the company
// and something from this posting, the title or a requirement.
func gateSpecific(j store.Job, review FitReview, drafts map[string]string) Gate {
	letter, ok := drafts["cover-letter.md"]
	if !ok {
		return Gate{"specific", Skip, "no cover-letter.md", nil}
	}
	text := fold(letter)
	var miss []string
	if name := store.CompanyName(j.Company); name != "" && !strings.Contains(text, fold(name)) {
		miss = append(miss, "does not name "+name)
	}
	specific := j.Title != "" && strings.Contains(text, fold(j.Title))
	for _, q := range review.Requirements {
		for _, w := range []string{q.Requirement, q.Wording} {
			if w != "" && len(w) <= 60 && strings.Contains(text, fold(w)) {
				specific = true
			}
		}
	}
	if !specific {
		miss = append(miss, "mentions neither the job title nor a requirement of the posting")
	}
	if len(miss) > 0 {
		return Gate{"specific", Warn, "the cover letter reads generic: " + strings.Join(miss, "; "), miss}
	}
	return Gate{"specific", Pass, "names the company and the role", nil}
}

// gatePreferences warns when the job breaks a must of the profile or preferences: place, work mode,
// engagement or a salary below the floor.
func gatePreferences(s *store.Store, j store.Job, dir string) Gate {
	pr := match.Load(s)
	if pr.Empty() {
		return Gate{"preferences", Skip, "no profile or preferences to compare with", nil}
	}
	p := search.Posting{Company: j.Company, Title: j.Title, Location: j.Location, Remote: j.Remote, Tags: j.Tags,
		SalaryMin: j.SalaryMin, SalaryMax: j.SalaryMax, Currency: j.Currency}
	if b, err := os.ReadFile(filepath.Join(dir, PostingName)); err == nil {
		p.Description = string(b)
	}
	search.Enrich(&p)
	if hard := pr.Score(p).Hard; len(hard) > 0 {
		return Gate{"preferences", Warn, "breaks what you said you need: " + strings.Join(hard, "; "), hard}
	}
	return Gate{"preferences", Pass, "nothing against your must-haves", nil}
}

// gateRepeat warns about another application to the same company in the last RepeatDays days.
func gateRepeat(s *store.Store, j store.Job, now time.Time) Gate {
	since := now.AddDate(0, 0, -RepeatDays).UTC().Format(time.RFC3339)
	jobs, _ := s.AppliedSince(since)
	var hits []string
	for _, o := range jobs {
		if o.ID == j.ID || store.CompanyKey(o.Company) != store.CompanyKey(j.Company) {
			continue
		}
		hits = append(hits, fmt.Sprintf("%s, %s on %s", o.Title, o.Status, o.UpdatedAt[:min(10, len(o.UpdatedAt))]))
	}
	if len(hits) > 0 {
		return Gate{"repeat", Warn, fmt.Sprintf("you applied to %s in the last %d days: recruiters see both", j.Company, RepeatDays), hits}
	}
	return Gate{"repeat", Pass, fmt.Sprintf("no other application to %s in %d days", j.Company, RepeatDays), nil}
}

// HasDrafts reports whether the assistant or the user wrote a resume or cover letter for the job.
func HasDrafts(s *store.Store, j store.Job) bool {
	for _, n := range []string{"resume.md", "cover-letter.md"} {
		if _, err := os.Stat(filepath.Join(Dir(s, j), n)); err == nil {
			return true
		}
	}
	return false
}
