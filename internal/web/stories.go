package web

import (
	"net/http"

	"github.com/zoolcoder/scoutjobs/internal/apply"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

// storyCard is a story with the open jobs whose must-haves it answers.
type storyCard struct {
	apply.Story
	Jobs []store.Job
}

func (v *server) stories(w http.ResponseWriter, r *http.Request) {
	bank := apply.Stories(v.s.Home)
	cards := make([]storyCard, len(bank))
	byID := map[string]*storyCard{}
	for i := range bank {
		cards[i].Story = bank[i]
		byID[bank[i].ID] = &cards[i]
	}
	uncovered := 0
	if jobs, err := v.s.ListJobs(store.JobFilter{}); err == nil && len(bank) > 0 {
		for _, j := range jobs {
			if store.FinalStatuses[j.Status] {
				continue
			}
			review, err := apply.LoadReview(apply.Dir(v.s, j))
			if err != nil {
				continue
			}
			seen := map[string]bool{}
			for _, m := range apply.MatchStories(bank, review) {
				if m.Story == nil {
					uncovered++
					continue
				}
				if c := byID[m.Story.ID]; c != nil && !seen[m.Story.ID] {
					seen[m.Story.ID] = true
					c.Jobs = append(c.Jobs, j)
				}
			}
		}
	}
	v.render(w, r, "stories.html", "stories", map[string]any{"Stories": cards, "Uncovered": uncovered})
}

// storyMap maps the job's must-haves to the story bank; nil without a fit review or without stories.
func (v *server) storyMap(dir string) []apply.StoryMatch {
	bank := apply.Stories(v.s.Home)
	review, err := apply.LoadReview(dir)
	if len(bank) == 0 || err != nil {
		return nil
	}
	return apply.MatchStories(bank, review)
}
