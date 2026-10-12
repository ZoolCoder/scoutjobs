package web

import (
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/zoolcoder/scoutjobs/internal/apply"
	"github.com/zoolcoder/scoutjobs/internal/search"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

// colCap keeps a long column cheap to render; the rest is one "Show all" click away.
const colCap = 50

// listPage is the list view's page size.
const listPage = 100

type column struct {
	Status    string
	Jobs      []store.Job // the cards drawn, at most colCap unless the column is expanded
	Total     int
	Expanded  bool
	Collapsed bool // a closed stage drawn as a thin strip until "Closed stages" is on
}

// pipeFilter is the board's toolbar. Every field round-trips through the query string so a view can be bookmarked.
type pipeFilter struct {
	Q, Src, Sort, View  string
	Loc, Tag            string // a city, country or region; one tag
	Due, Remote         bool
	Fit                 int64
	Full, Dense, Closed bool   // focus mode, compact cards, closed stages as full columns
	More                string // the one column drawn without the card cap
	Page                int    // list view page, from 1
}

func (f pipeFilter) values() url.Values {
	q := url.Values{}
	set := func(k, v string) {
		if v != "" {
			q.Set(k, v)
		}
	}
	set("q", f.Q)
	set("src", f.Src)
	set("loc", f.Loc)
	set("tag", f.Tag)
	if f.Sort != "updated" {
		set("sort", f.Sort)
	}
	if f.View != "board" {
		set("view", f.View)
	}
	if f.Due {
		q.Set("due", "1")
	}
	if f.Remote {
		q.Set("remote", "1")
	}
	if f.Fit > 0 {
		q.Set("fit", strconv.FormatInt(f.Fit, 10))
	}
	for k, on := range map[string]bool{"full": f.Full, "dense": f.Dense, "closed": f.Closed} {
		if on {
			q.Set(k, "1")
		}
	}
	set("more", f.More)
	if f.Page > 1 {
		q.Set("page", strconv.Itoa(f.Page))
	}
	return q
}

// url is the board with this filter, plus overrides ("" deletes a key).
func (f pipeFilter) url(kv ...string) string {
	q := f.values()
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i] != "job" && kv[i] != "page" {
			q.Del("page") // a new filter starts the list from its first page
		}
		if kv[i+1] == "" {
			q.Del(kv[i])
		} else {
			q.Set(kv[i], kv[i+1])
		}
	}
	if len(q) == 0 {
		return "/pipeline"
	}
	return "/pipeline?" + q.Encode()
}

// Toggle links for the toolbar chips.
func (f pipeFilter) DueURL() string    { return f.url("due", flag(!f.Due)) }
func (f pipeFilter) RemoteURL() string { return f.url("remote", flag(!f.Remote)) }
func (f pipeFilter) FitURL() string {
	if f.Fit > 0 {
		return f.url("fit", "")
	}
	return f.url("fit", "70")
}
func (f pipeFilter) ViewURL(v string) string { return f.url("view", strings.TrimPrefix(v, "board")) }
func (f pipeFilter) JobURL(id int64) string  { return f.url("job", strconv.FormatInt(id, 10)) }
func (f pipeFilter) URL() string             { return f.url() }
func (f pipeFilter) FullURL() string         { return f.url("full", flag(!f.Full)) }
func (f pipeFilter) DenseURL() string        { return f.url("dense", flag(!f.Dense)) }
func (f pipeFilter) ClosedURL() string       { return f.url("closed", flag(!f.Closed)) }
func (f pipeFilter) PageURL(n int) string {
	if n <= 1 {
		return f.url("page", "")
	}
	return f.url("page", strconv.Itoa(n))
}
func (f pipeFilter) MoreURL(status string) string {
	if f.More == status {
		return f.url("more", "")
	}
	return f.url("more", status)
}
func (f pipeFilter) TagURL(tag string) string {
	if strings.EqualFold(f.Tag, tag) {
		return f.url("tag", "")
	}
	return f.url("tag", tag)
}
func (f pipeFilter) Active() bool {
	return f.Q != "" || f.Src != "" || f.Loc != "" || f.Tag != "" || f.Due || f.Remote || f.Fit > 0
}

func flag(b bool) string {
	if b {
		return "1"
	}
	return ""
}

type pipeData struct {
	Columns []column
	Hidden  []string // empty final stages get no column but stay reachable as drop targets
	Jobs    []store.Job
	Count   int
	F       pipeFilter
	Sources []string
	Tags    []string // the most used tags, for the tag picker
	Sorts   []string
	MailN   map[int64]int
	Sel     *jobPanel
	Query   string // kept for the old search box name
	Due     bool
	Page    int // list view paging
	Pages   int
}

func (v *server) pipeline(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := pipeFilter{Q: strings.TrimSpace(q.Get("q")), Src: q.Get("src"), Loc: strings.TrimSpace(q.Get("loc")), Tag: strings.TrimSpace(q.Get("tag")), Sort: q.Get("sort"), View: q.Get("view"), Due: q.Get("due") != "", Remote: q.Get("remote") != "",
		Full: q.Get("full") != "", Dense: q.Get("dense") != "", Closed: q.Get("closed") != "", More: q.Get("more")}
	f.Fit, _ = strconv.ParseInt(q.Get("fit"), 10, 64)
	f.Page, _ = strconv.Atoi(q.Get("page"))
	if f.View != "list" {
		f.View = "board"
	}
	switch f.Sort {
	case "fit", "deadline", "company":
	default:
		f.Sort = "updated"
	}
	all, err := v.s.ListJobs(store.JobFilter{Query: f.Q})
	if err != nil {
		fail(w, err)
		return
	}
	srcs := map[string]bool{}
	for _, j := range all {
		if j.Source != "" {
			srcs[j.Source] = true
		}
	}
	jobs := all
	if f.Due {
		jobs = v.dueJobs(jobs)
	}
	jobs = filterJobs(jobs, f)
	sortJobs(jobs, f.Sort)
	d := pipeData{Jobs: jobs, Count: len(jobs), F: f, MailN: v.s.MailCounts(), Query: f.Q, Due: f.Due,
		Sorts: []string{"updated", "fit", "deadline", "company"}}
	for s := range srcs {
		d.Sources = append(d.Sources, s)
	}
	sort.Strings(d.Sources)
	d.Tags = topTags(all, 40)
	by := map[string][]store.Job{}
	for _, j := range jobs {
		by[j.Status] = append(by[j.Status], j)
	}
	var closed []column // final stages sit right of the open ones, like Done on a Jira board
	for _, st := range store.Statuses {
		n := len(by[st])
		if n == 0 && store.FinalStatuses[st] {
			d.Hidden = append(d.Hidden, st)
			continue
		}
		c := column{Status: st, Jobs: by[st], Total: n, Expanded: f.More == st, Collapsed: store.FinalStatuses[st] && !f.Closed}
		if c.Collapsed {
			c.Jobs = nil
		} else if !c.Expanded && n > colCap {
			c.Jobs = c.Jobs[:colCap]
		}
		if store.FinalStatuses[st] {
			closed = append(closed, c)
		} else {
			d.Columns = append(d.Columns, c)
		}
	}
	d.Columns = append(d.Columns, closed...)
	d.Pages = (len(jobs) + listPage - 1) / listPage
	d.Page = min(max(f.Page, 1), max(d.Pages, 1))
	if f.View == "list" && len(jobs) > listPage {
		d.Jobs = jobs[(d.Page-1)*listPage : min(d.Page*listPage, len(jobs))]
	}
	if id, err := strconv.ParseInt(q.Get("job"), 10, 64); err == nil {
		if j, err := v.s.JobByID(id); err == nil {
			d.Sel = v.panel(j, f.JobURL(id), f.URL())
		}
	}
	v.render(w, r, "pipeline.html", "pipeline", d)
}

func filterJobs(jobs []store.Job, f pipeFilter) []store.Job {
	out := jobs[:0:0]
	for _, j := range jobs {
		if f.Src != "" && j.Source != f.Src {
			continue
		}
		if f.Remote && !strings.EqualFold(j.Remote, "remote") {
			continue
		}
		if f.Fit > 0 && j.FitScore < f.Fit {
			continue
		}
		if f.Loc != "" && !jobIn(j, f.Loc) {
			continue
		}
		if f.Tag != "" && !hasTag(j.Tags, f.Tag) {
			continue
		}
		out = append(out, j)
	}
	return out
}

// jobIn is true when the job is in, or remote and open to, one of the comma separated places.
// A job with no location stated is left out: the user asked for a place.
func jobIn(j store.Job, places string) bool {
	ok, note := search.LocationMatch(search.Posting{Location: j.Location, Remote: j.Remote}, splitList(places))
	return ok && note == ""
}

func hasTag(tags, tag string) bool {
	for _, t := range splitList(tags) {
		if strings.EqualFold(t, tag) {
			return true
		}
	}
	return false
}

// topTags is the tags most jobs carry, most common first, at most n.
func topTags(jobs []store.Job, n int) []string {
	count := map[string]int{}
	for _, j := range jobs {
		for _, t := range splitList(j.Tags) {
			count[strings.ToLower(t)]++
		}
	}
	tags := make([]string, 0, len(count))
	for t := range count {
		tags = append(tags, t)
	}
	sort.Slice(tags, func(a, b int) bool {
		if count[tags[a]] != count[tags[b]] {
			return count[tags[a]] > count[tags[b]]
		}
		return tags[a] < tags[b]
	})
	if len(tags) > n {
		tags = tags[:n]
	}
	return tags
}

func sortJobs(jobs []store.Job, by string) {
	sort.SliceStable(jobs, func(a, b int) bool {
		x, y := jobs[a], jobs[b]
		switch by {
		case "fit":
			return x.FitScore > y.FitScore
		case "deadline":
			if (x.Deadline == "") != (y.Deadline == "") {
				return x.Deadline != ""
			}
			return x.Deadline < y.Deadline
		case "company":
			return strings.ToLower(x.Company) < strings.ToLower(y.Company)
		}
		return x.UpdatedAt > y.UpdatedAt
	})
}

// step is one stage of the happy path, drawn as a stepper on the panel and the job page.
type step struct {
	Status string
	State  string // done | current | todo
}

var happyPath = []string{"new", "ranked", "drafted", "applied", "interview", "offer", "hired"}

func stepsOf(status string) []step {
	at := -1
	for i, s := range happyPath {
		if s == status {
			at = i
		}
	}
	out := make([]step, len(happyPath))
	for i, s := range happyPath {
		st := "todo"
		switch {
		case i < at:
			st = "done"
		case i == at:
			st = "current"
		}
		out[i] = step{s, st}
	}
	return out
}

// jobPanel is the issue view: everything needed to work a job without leaving the board.
type jobPanel struct {
	Job        store.Job
	Steps      []step
	OffPath    bool // rejected, skipped, withdrawn...: shown as a badge after the stepper
	Events     []store.Event
	EventCount int // all events, Events holds the newest few
	Interviews []store.Interview
	Mails      []mailRow
	Files      []string
	KitDone    int
	Kit        []kitItem
	Back       string // where forms return: the board with this panel open
	Close      string // the board without the panel
	Compose    string // Gmail compose to the contact
	MailSearch string // Gmail search for everything from the company
	CanDraft   bool   // a mailbox takes drafts and an email draft exists
	DraftLabel string
	Contacts   int
}

type kitItem struct {
	Label string
	Done  bool
}

func (v *server) panel(j store.Job, backURL, closeURL string) *jobPanel {
	events, _ := v.s.Events(j.ID, 100)
	p := &jobPanel{Job: j, Steps: stepsOf(j.Status), Back: backURL, Close: closeURL}
	for _, s := range happyPath {
		p.OffPath = p.OffPath || s == j.Status
	}
	p.OffPath = !p.OffPath
	p.Events, p.EventCount = events, len(events)
	if len(p.Events) > 5 {
		p.Events = p.Events[:5]
	}
	p.Interviews, _ = v.s.Interviews(j.ID, false)
	p.Mails = v.mailRows(j.ID, 5)
	kit, _, entries := apply.Status(v.s, j, events)
	for _, f := range entries {
		p.Files = append(p.Files, f.Name)
	}
	p.KitDone = kit.Done
	p.Kit = []kitItem{{"Tailored CV", kit.Resume}, {"Cover letter", kit.Cover}, {"ATS check", kit.ATS != nil}, {"Sent", kit.Sent}}
	p.Compose = composeTo(j)
	p.MailSearch = mailSearch(j)
	p.CanDraft, p.DraftLabel = kit.Draft != nil && !kit.Sent && v.canDraft(), v.draftLabel()
	if cs, err := v.s.ContactsAt(j.Company); err == nil {
		p.Contacts = len(cs)
	}
	return p
}
