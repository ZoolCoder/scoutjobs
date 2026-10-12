package web

import (
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

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

func (v *server) newStory(w http.ResponseWriter, r *http.Request) {
	v.render(w, r, "story-new.html", "stories", map[string]any{"Err": r.URL.Query().Get("err")})
}

func (v *server) createStory(w http.ResponseWriter, r *http.Request) {
	if err := sameOrigin(r); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, apply.MaxFile)
	f := r.FormValue
	name, err := apply.NewStory(v.s.Home, apply.StoryDraft{
		Title: f("title"), Context: f("context"), Role: f("role"), Timeline: f("timeline"),
		Themes: strings.Split(f("themes"), ","), Metrics: strings.Split(f("metrics"), "\n"), Facts: f("facts"),
		Situation: f("situation"), Task: f("task"), Action: f("action"), Result: f("result"),
	}, time.Now())
	if err != nil {
		http.Redirect(w, r, "/stories/new?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/stories/"+name+"?saved=1", http.StatusSeeOther)
}

// storyFile shows one story, and edits it as Markdown with its front matter.
func (v *server) storyFile(w http.ResponseWriter, r *http.Request) {
	p, ok := loadPage(filepath.Join(v.s.Home, "stories"), r.PathValue("name"))
	if !ok || p.Kind != "md" {
		http.NotFound(w, r)
		return
	}
	p.HTML = mdHTML(apply.StoryBody(p.Body))
	p.Back, p.BackLabel = "/stories", "Stories"
	p.Editable = true
	p.Edit = r.URL.Query().Get("edit") != ""
	p.Saved = r.URL.Query().Get("saved") != ""
	p.SaveURL = "/stories/" + p.Name
	v.render(w, r, "file.html", "stories", map[string]any{"F": p})
}

func (v *server) saveStory(w http.ResponseWriter, r *http.Request) {
	if err := sameOrigin(r); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	dir := filepath.Join(v.s.Home, "stories")
	if p, ok := loadPage(dir, r.PathValue("name")); !ok || p.Kind != "md" {
		http.NotFound(w, r)
		return
	}
	name := filepath.Base(r.PathValue("name"))
	r.Body = http.MaxBytesReader(w, r.Body, apply.MaxFile+4096)
	body := strings.ReplaceAll(r.FormValue("body"), "\r\n", "\n")
	if _, err := apply.WriteFile(dir, name, []byte(body)); err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, "/stories/"+name+"?saved=1", http.StatusSeeOther)
}
