package web

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/search"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

// newJob shows the manual add form; the fields match `scoutjobs job add`.
func (v *server) newJob(w http.ResponseWriter, r *http.Request) {
	v.render(w, r, "add.html", "add_job", map[string]any{"Job": store.Job{Currency: v.cfg.Currency}, "Back": back(r, "/pipeline")})
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
	Postings        []search.Posting
	Added           int
	Errors          []string
	BoardNames      []string
}

func (v *server) searchPage(w http.ResponseWriter, r *http.Request) {
	v.render(w, r, "search.html", "search", searchForm{
		Queries: strings.Join(v.cfg.SearchQueries, ", "), Boards: strings.Join(v.cfg.SearchBoards, ", "),
		Remote: v.cfg.SearchRemote, Limit: 50, BoardNames: search.Boards,
	})
}

// runSearch runs the boards from the form; "add=1" tracks every hit as a new job, otherwise the rows get an Add button each.
func (v *server) runSearch(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		fail(w, err)
		return
	}
	f := searchForm{Queries: strings.TrimSpace(r.FormValue("queries")), Boards: strings.TrimSpace(r.FormValue("boards")),
		Remote: r.FormValue("remote") != "", Ran: true, BoardNames: search.Boards}
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
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	res := search.Run(ctx, search.Options{Queries: splitList(f.Queries), Boards: boards, Remote: f.Remote, Limit: f.Limit, BaseURL: v.cfg.SearchBaseURL})
	f.Postings, f.Errors = res.Postings, res.Errors
	if r.FormValue("add") != "" {
		for _, p := range res.Postings {
			if _, created, err := v.s.UpsertJob(p.Job()); err != nil {
				fail(w, err)
				return
			} else if created {
				f.Added++
			}
		}
	}
	v.render(w, r, "search.html", "search", f)
}

func splitList(s string) []string {
	var out []string
	for _, x := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' }) {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}
