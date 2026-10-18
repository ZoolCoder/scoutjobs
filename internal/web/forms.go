package web

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/apply"
	"github.com/zoolcoder/scoutjobs/internal/match"
	"github.com/zoolcoder/scoutjobs/internal/search"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

// newJob shows the manual add form; the fields match `scoutjobs job add`. ?from=<posting url>
// prefills them from the ATS, like `job add --from`; the user still reviews before saving.
func (v *server) newJob(w http.ResponseWriter, r *http.Request) {
	j := store.Job{Currency: v.cfg.Currency}
	data := map[string]any{"Back": back(r, "/pipeline"), "From": strings.TrimSpace(r.URL.Query().Get("from"))}
	if from, _ := data["From"].(string); from != "" {
		if existing, ok := v.s.JobByURL(from); ok {
			http.Redirect(w, r, fmt.Sprintf("/jobs/%d", existing.ID), http.StatusSeeOther)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
		defer cancel()
		p, err := search.FetchPosting(ctx, from, nil, v.cfg.SearchBaseURL)
		if err != nil {
			data["Error"] = err.Error()
			j.URL = from
		} else {
			j = p.Job()
			j.URL, j.Notes, j.Currency = from, search.Excerpt(p.Description, 4000), first(j.Currency, v.cfg.Currency)
		}
	}
	data["Job"] = j
	v.render(w, r, "add.html", "add_job", data)
}

func first(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// createJob is the form twin of POST /api/jobs: an existing company+title is updated, not duplicated.
func (v *server) createJob(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		fail(w, err)
		return
	}
	j := jobFromForm(r)
	if j.Company == "" || j.Title == "" {
		http.Error(w, "company and title are required", http.StatusBadRequest)
		return
	}
	job, _, err := v.s.UpsertJob(j)
	if err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, back(r, fmt.Sprintf("/jobs/%d", job.ID)), http.StatusSeeOther)
}

func jobFromForm(r *http.Request) store.Job {
	f := func(k string) string { return strings.TrimSpace(r.FormValue(k)) }
	n := func(k string) int64 { x, _ := strconv.ParseInt(f(k), 10, 64); return x }
	return store.Job{
		Company: f("company"), Title: f("title"), URL: f("url"), Location: f("location"), Remote: f("remote"),
		SalaryMin: n("salary_min"), SalaryMax: n("salary_max"), Currency: f("currency"), Source: f("source"),
		PostedAt: f("posted_at"), Deadline: f("deadline"), Tags: f("tags"), Notes: f("notes"), Contact: f("contact"),
	}
}

// searchForm carries the search page state through a GET (defaults) and a POST (results).
type searchForm struct {
	Queries, Boards string
	Remote          bool
	Limit           int
	Ran             bool
	Postings        []searchRow
	Added           int
	Errors          []string
	BoardNames      []string
	Saved           []store.SavedSearch
	Name            string // the saved search this form came from or is saved as
	Filter          search.Filter
	Hidden          int // postings the result filter dropped
	Mismatched      int // postings hidden because they break a must in the profile
	Ranked          bool
	Suggestions     []match.Suggestion
	TagCounts       []tagCount
}

// tagCount is a tag the results share, for the tag filter bar.
type tagCount struct {
	Tag string
	N   int
}

// searchRow is a posting plus the tracked job it already matches, if any, and how well it fits the
// profile.
type searchRow struct {
	search.Posting
	Job     store.Job
	Match   match.Result
	Ranked  bool
	TagList []string
}

// FitClass colours the match score: a strong fit plain, a partial one amber, a weak one or one that
// breaks a must red.
func (r searchRow) FitClass() string {
	switch {
	case len(r.Match.Hard) > 0 || r.Match.Score < 45:
		return "lo"
	case r.Match.Score < 70:
		return "mid"
	}
	return ""
}

// DataTags is the row's tags for the tag filter, lower case and "|" separated.
// MatchLabel says in words how well a ranked posting fits.
func (r searchRow) MatchLabel() string {
	switch {
	case len(r.Match.Hard) > 0:
		return "Breaks a must-have"
	case r.Match.Score >= 70:
		return "Great match"
	case r.Match.Score >= 45:
		return "Good match"
	}
	return "Weak match"
}

func (r searchRow) DataTags() string {
	return "|" + strings.ToLower(strings.Join(r.TagList, "|")) + "|"
}

func (v *server) searchRows(ps []search.Posting, pr match.Profile, ranked bool) []searchRow {
	rows := make([]searchRow, 0, len(ps))
	for _, p := range ps {
		row := searchRow{Posting: p, Ranked: ranked, TagList: splitList(p.AllTags())}
		if ranked {
			row.Match = pr.Score(p)
		}
		if j, err := v.s.JobByKey(store.JobKey(p.Company, p.Title)); err == nil {
			row.Job = j
		} else if j, ok := v.s.JobByURL(p.URL); ok {
			row.Job = j
		}
		rows = append(rows, row)
	}
	return rows
}

func (v *server) searchPage(w http.ResponseWriter, r *http.Request) {
	f := searchForm{
		Queries: strings.Join(v.cfg.SearchQueries, ", "), Boards: strings.Join(v.cfg.SearchBoards, ", "),
		Remote: v.cfg.SearchRemote, Limit: 50, BoardNames: search.Boards,
	}
	pr := match.Load(v.s)
	f.Filter.Location = strings.Join(pr.Places, ", ")
	f.Suggestions = pr.Suggest()
	f.Saved, _ = v.s.SavedSearches()
	v.render(w, r, "search.html", "search", f)
}

// runSearch runs the boards from the form; "add=1" tracks every hit as a new job, otherwise the rows get an Add button each.
func (v *server) runSearch(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		fail(w, err)
		return
	}
	f := searchForm{Queries: strings.TrimSpace(r.FormValue("queries")), Boards: strings.TrimSpace(r.FormValue("boards")),
		Remote: r.FormValue("remote") != "", Ran: true, BoardNames: search.Boards, Filter: search.FilterFromValues(r.Form), Name: strings.TrimSpace(r.FormValue("name"))}
	if id, err := strconv.ParseInt(r.FormValue("saved"), 10, 64); err == nil {
		x, err := v.s.SavedSearch(id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		f.Queries, f.Boards, f.Remote, f.Filter, f.Name = x.Queries, x.Boards, x.Remote, search.ParseFilter(x.Filters), x.Name
	} else if r.FormValue("save") != "" && f.Name != "" {
		if _, err := v.s.SaveSearch(store.SavedSearch{Name: f.Name, Queries: f.Queries, Boards: f.Boards, Remote: f.Remote, Filters: f.Filter.Encode()}); err != nil {
			fail(w, err)
			return
		}
	}
	f.Saved, _ = v.s.SavedSearches()
	f.Limit, _ = strconv.Atoi(r.FormValue("limit"))
	if f.Limit <= 0 {
		f.Limit = 50
	}
	boards := splitList(f.Boards)
	if len(boards) == 0 && !f.Remote {
		f.Errors = []string{"nothing to search: name a board or tick remote boards"}
		v.render(w, r, "search.html", "search", f)
		return
	}
	pr := match.Load(v.s)
	f.Suggestions = pr.Suggest()
	if f.Filter.Location != "" {
		pr.Places = splitList(f.Filter.Location) // the place searched for is the place to score against
	}
	f.Ranked = !pr.Empty()
	opts := search.Options{Queries: splitList(f.Queries), Boards: boards, Remote: f.Remote, Off: v.remoteOff(), Limit: f.Limit, BaseURL: v.cfg.SearchBaseURL}
	now := time.Now()
	opts.Keep = func(p search.Posting) bool {
		switch {
		case !f.Filter.Keep(p, now):
			f.Hidden++
		case f.Filter.HideMismatch && f.Ranked && len(pr.Score(p).Hard) > 0:
			f.Mismatched++
		default:
			return true
		}
		return false
	}
	if f.Ranked {
		opts.Rank = func(p search.Posting) int { return pr.Score(p).Score }
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	res := search.Run(ctx, opts)
	f.Errors = res.Errors
	if r.FormValue("add") != "" {
		for _, p := range res.Postings {
			j, created, err := v.s.UpsertJob(p.Job())
			if err == nil {
				err = apply.SavePosting(v.s, j, p.Description)
			}
			if err != nil {
				fail(w, err)
				return
			}
			if created {
				f.Added++
			}
		}
		if f.Added > 0 {
			_, _ = apply.Rank(v.s, false)
		}
	}
	f.Postings = v.searchRows(res.Postings, pr, f.Ranked)
	f.TagCounts = tagCounts(f.Postings, 24)
	v.render(w, r, "search.html", "search", f)
}

// tagCounts is the tags shared by two or more rows, most common first, at most n of them.
func tagCounts(rows []searchRow, n int) []tagCount {
	count := map[string]int{}
	for _, r := range rows {
		for _, t := range r.TagList {
			count[strings.ToLower(t)]++
		}
	}
	var out []tagCount
	for t, c := range count {
		if c >= 2 {
			out = append(out, tagCount{t, c})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].N != out[j].N {
			return out[i].N > out[j].N
		}
		return out[i].Tag < out[j].Tag
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

func (v *server) deleteSearch(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	if err := v.s.DeleteSearch(id); err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, "/search", http.StatusSeeOther)
}

// dailySearch switches a saved search's daily run on or off; the dashboard and `scoutjobs daily` run it.
func (v *server) dailySearch(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	if err := v.s.SetSearchDaily(id, r.FormValue("on") == "1"); err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, "/search", http.StatusSeeOther)
}

func splitList(s string) []string { return search.SplitList(s) }

// remoteOff lists the remote boards turned off on the Connections page.
func (v *server) remoteOff() []string { return splitList(v.s.Setting(search.OffSetting)) }
