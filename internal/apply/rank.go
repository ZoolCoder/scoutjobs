package apply

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/zoolcoder/scoutjobs/internal/match"
	"github.com/zoolcoder/scoutjobs/internal/search"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

// QuickPrefix marks fit notes written by the local ranker, so an agent's score is never mistaken for one.
const QuickPrefix = "quick: "

// Ranked is one job the local ranker scored.
type Ranked struct {
	Job   store.Job    `json:"job"`
	Match match.Result `json:"match"`
}

// MatchJob scores a tracked job against the profile from its stored posting: the same scoring a search
// uses, with no agent and no tokens.
func MatchJob(s *store.Store, pr match.Profile, j store.Job) match.Result {
	p := search.Posting{Company: j.Company, Title: j.Title, Location: j.Location, Remote: j.Remote, Tags: j.Tags,
		SalaryMin: j.SalaryMin, SalaryMax: j.SalaryMax, Currency: j.Currency}
	if b, err := os.ReadFile(filepath.Join(Dir(s, j), PostingName)); err == nil {
		p.Description = string(b)
	}
	search.Enrich(&p)
	return pr.Score(p)
}

// Rank scores the new jobs, or with all every open job not yet scored by an agent, and moves new
// ones to ranked. An agent's score is kept: it read the posting, the ranker only matched words.
func Rank(s *store.Store, all bool) ([]Ranked, error) {
	pr := match.Load(s)
	f := store.JobFilter{Status: "new"}
	if all {
		f = store.JobFilter{Open: true}
	}
	jobs, err := s.ListJobs(f)
	if err != nil {
		return nil, err
	}
	var out []Ranked
	for _, j := range jobs {
		if j.FitScore > 0 && !strings.HasPrefix(j.FitNotes, QuickPrefix) {
			continue
		}
		m := MatchJob(s, pr, j)
		score, notes := int64(m.Score), QuickNotes(m)
		p := store.JobPatch{FitScore: &score, FitNotes: &notes}
		if j.Status == "new" {
			ranked := "ranked"
			p.Status = &ranked
		}
		if err := s.UpdateJob(j.ID, p); err != nil {
			return out, err
		}
		j.FitScore, j.FitNotes = score, notes
		out = append(out, Ranked{j, m})
	}
	return out, nil
}

// QuickNotes is the one-line reason a local score gets: what breaks a must, then what fits and what is missing.
func QuickNotes(m match.Result) string {
	var parts []string
	if len(m.Hard) > 0 {
		parts = append(parts, "breaks: "+strings.Join(m.Hard, ", "))
	}
	if len(m.Why) > 0 {
		parts = append(parts, strings.Join(first(m.Why, 3), ", "))
	}
	if len(m.Miss) > 0 {
		parts = append(parts, "missing: "+strings.Join(first(m.Miss, 3), ", "))
	}
	n := QuickPrefix + strings.Join(parts, "; ")
	if len(n) > 240 {
		n = n[:240]
	}
	return n
}

func first(list []string, n int) []string {
	if len(list) > n {
		return list[:n]
	}
	return list
}
