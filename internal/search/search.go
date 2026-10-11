// Package search queries public job APIs that need no login or key.
// Postings are untrusted input: only fields are extracted, HTML is stripped, nothing is executed.
package search

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/store"
)

const userAgent = "scoutjobs/1.0 (+https://github.com/zoolcoder/scoutjobs)"

// Posting is one normalized job posting. Field names match `scoutjobs job add` flags.
type Posting struct {
	Company     string `json:"company"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	Location    string `json:"location"`
	Remote      string `json:"remote"`
	Source      string `json:"source"`
	PostedAt    string `json:"posted_at"`
	Tags        string `json:"tags"`
	SalaryMin   int64  `json:"salary_min,omitempty"`
	SalaryMax   int64  `json:"salary_max,omitempty"`
	Currency    string `json:"currency,omitempty"`
	Description string `json:"description,omitempty"`
}

// Job converts a posting into a tracker row.
func (p Posting) Job() store.Job {
	return store.Job{Company: p.Company, Title: p.Title, URL: p.URL, Location: p.Location, Remote: p.Remote,
		Source: p.Source, PostedAt: p.PostedAt, Tags: p.Tags, SalaryMin: p.SalaryMin, SalaryMax: p.SalaryMax, Currency: p.Currency}
}

// Options drive one search run.
type Options struct {
	Queries []string // every word of a query must appear; any query may match
	Boards  []string // "greenhouse:stripe", "lever:netlify", "ashby:linear", "workable:acme", "smartrecruiters:Acme", "personio:acme", "rss:https://..."
	Remote  bool     // also query the remote boards (remotive, remoteok, arbeitnow, jobicy, himalayas)
	Limit   int
	Client  *http.Client
	BaseURL map[string]string // test override per source
}

// Result is what a run returns besides the postings.
type Result struct {
	Postings []Posting
	Errors   []string
}

// Boards lists the board sources that need a company slug.
var Boards = []string{"greenhouse", "lever", "ashby", "workable", "smartrecruiters", "personio", "rss"}

// RemoteBoards lists the keyword-searchable remote boards.
var RemoteBoards = []string{"remotive", "remoteok", "arbeitnow", "jobicy", "himalayas"}

var defaultBase = map[string]string{
	"greenhouse":      "https://boards-api.greenhouse.io/v1/boards",
	"lever":           "https://api.lever.co/v0/postings",
	"ashby":           "https://api.ashbyhq.com/posting-api/job-board",
	"remotive":        "https://remotive.com/api/remote-jobs",
	"remoteok":        "https://remoteok.com/api",
	"arbeitnow":       "https://www.arbeitnow.com/api/job-board-api",
	"jobicy":          "https://jobicy.com/api/v2/remote-jobs",
	"himalayas":       "https://himalayas.app/jobs/api",
	"workable":        "https://apply.workable.com/api/v3/accounts",
	"smartrecruiters": "https://api.smartrecruiters.com/v1/companies",
}

type fetcher struct {
	client *http.Client
	base   map[string]string
}

func (f fetcher) url(source string) string {
	if u, ok := f.base[source]; ok {
		return u
	}
	return defaultBase[source]
}

func (f fetcher) getJSON(ctx context.Context, u string, v any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	res, err := f.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("%s: http %d", u, res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 20<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}

// Run executes the search and returns deduplicated postings that match the queries.
func Run(ctx context.Context, o Options) Result {
	if o.Limit <= 0 {
		o.Limit = 50
	}
	f := fetcher{client: o.Client, base: o.BaseURL}
	if f.client == nil {
		f.client = &http.Client{Timeout: 20 * time.Second}
	}
	type feed struct {
		name string
		fn   func(context.Context) ([]Posting, error)
	}
	var feeds []feed
	for _, b := range o.Boards {
		src, slug, ok := strings.Cut(b, ":")
		if !ok || slug == "" {
			continue
		}
		switch src {
		case "greenhouse":
			feeds = append(feeds, feed{b, func(ctx context.Context) ([]Posting, error) { return f.greenhouse(ctx, slug, o.Queries, o.Limit) }})
		case "lever":
			feeds = append(feeds, feed{b, func(ctx context.Context) ([]Posting, error) { return f.lever(ctx, slug) }})
		case "ashby":
			feeds = append(feeds, feed{b, func(ctx context.Context) ([]Posting, error) { return f.ashby(ctx, slug) }})
		case "workable":
			feeds = append(feeds, feed{b, func(ctx context.Context) ([]Posting, error) { return f.workable(ctx, slug) }})
		case "smartrecruiters":
			feeds = append(feeds, feed{b, func(ctx context.Context) ([]Posting, error) { return f.smartrecruiters(ctx, slug) }})
		case "personio":
			feeds = append(feeds, feed{b, func(ctx context.Context) ([]Posting, error) { return f.personio(ctx, slug) }})
		case "rss":
			feeds = append(feeds, feed{b, func(ctx context.Context) ([]Posting, error) { return f.rss(ctx, slug) }})
		}
	}
	if o.Remote {
		qs := o.Queries
		if len(qs) == 0 {
			qs = []string{""}
		}
		for _, q := range qs {
			feeds = append(feeds,
				feed{"remotive:" + q, func(ctx context.Context) ([]Posting, error) { return f.remotive(ctx, q) }},
				feed{"arbeitnow:" + q, func(ctx context.Context) ([]Posting, error) { return f.arbeitnow(ctx, q) }},
				feed{"jobicy:" + q, func(ctx context.Context) ([]Posting, error) { return f.jobicy(ctx, q) }},
				feed{"himalayas:" + q, func(ctx context.Context) ([]Posting, error) { return f.himalayas(ctx, q) }},
			)
		}
		feeds = append(feeds, feed{"remoteok", func(ctx context.Context) ([]Posting, error) { return f.remoteok(ctx) }})
	}

	var r Result
	seen := map[string]bool{}
	for _, fd := range feeds {
		rows, err := fd.fn(ctx)
		if err != nil {
			r.Errors = append(r.Errors, fd.name+": "+err.Error())
			continue
		}
		for _, p := range rows {
			key := store.JobKey(p.Company, p.Title)
			if seen[key] || !Matches(p, o.Queries) {
				continue
			}
			seen[key] = true
			r.Postings = append(r.Postings, p)
			if len(r.Postings) >= o.Limit {
				return r
			}
		}
	}
	return r
}

// Matches reports whether any query has all of its words in the title, tags or description.
func Matches(p Posting, queries []string) bool {
	if len(queries) == 0 {
		return true
	}
	desc := p.Description
	if len(desc) > 1500 {
		desc = desc[:1500]
	}
	hay := p.Title + " " + p.Tags + " " + desc
	for _, q := range queries {
		if wordsMatch(q, hay) {
			return true
		}
	}
	return false
}

// wordsMatch: every query word appears as a whole word, so "go" does not match "good".
func wordsMatch(query, hay string) bool {
	hay = strings.ToLower(hay)
	for _, w := range strings.Fields(strings.ToLower(query)) {
		re := regexp.MustCompile(`(?:^|[^a-z0-9])` + regexp.QuoteMeta(w) + `(?:$|[^a-z0-9])`)
		if !re.MatchString(hay) {
			return false
		}
	}
	return true
}

var tagRe = regexp.MustCompile(`<[^>]+>`)
var spaceRe = regexp.MustCompile(`\s+`)

func stripHTML(s string) string {
	s = html.UnescapeString(s)
	s = tagRe.ReplaceAllString(s, " ")
	s = strings.TrimSpace(spaceRe.ReplaceAllString(s, " "))
	if len(s) > 4000 {
		s = s[:4000]
	}
	return s
}

func remoteOf(text string) string {
	t := strings.ToLower(text)
	switch {
	case strings.Contains(t, "remote"):
		return "remote"
	case strings.Contains(t, "hybrid"):
		return "hybrid"
	}
	return "unknown"
}

func day(s string) string {
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}

func joinTags(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, ",")
}

// ---- sources ----

func (f fetcher) greenhouse(ctx context.Context, slug string, queries []string, limit int) ([]Posting, error) {
	var list struct {
		Jobs []struct {
			ID          int64  `json:"id"`
			Title       string `json:"title"`
			AbsoluteURL string `json:"absolute_url"`
			UpdatedAt   string `json:"updated_at"`
			Content     string `json:"content"`
			Location    struct {
				Name string `json:"name"`
			} `json:"location"`
		} `json:"jobs"`
	}
	base := f.url("greenhouse") + "/" + url.PathEscape(slug) + "/jobs"
	if err := f.getJSON(ctx, base, &list); err != nil {
		return nil, err
	}
	var out []Posting
	for _, j := range list.Jobs {
		if len(queries) > 0 {
			hit := false
			for _, q := range queries {
				if wordsMatch(q, j.Title) {
					hit = true
					break
				}
			}
			if !hit {
				continue
			}
		}
		if len(out) >= limit {
			break
		}
		// the list omits content; fetch it per job only for title matches
		var full struct {
			Content string `json:"content"`
		}
		_ = f.getJSON(ctx, fmt.Sprintf("%s/%d", base, j.ID), &full)
		out = append(out, Posting{Company: slug, Title: j.Title, URL: j.AbsoluteURL, Location: j.Location.Name,
			Remote: remoteOf(j.Location.Name + " " + j.Title), Source: "greenhouse", PostedAt: day(j.UpdatedAt), Description: stripHTML(full.Content)})
	}
	return out, nil
}

func (f fetcher) lever(ctx context.Context, slug string) ([]Posting, error) {
	var list []struct {
		Text             string `json:"text"`
		HostedURL        string `json:"hostedUrl"`
		WorkplaceType    string `json:"workplaceType"`
		DescriptionPlain string `json:"descriptionPlain"`
		Description      string `json:"description"`
		Categories       struct {
			Location   string `json:"location"`
			Team       string `json:"team"`
			Department string `json:"department"`
			Commitment string `json:"commitment"`
		} `json:"categories"`
	}
	if err := f.getJSON(ctx, f.url("lever")+"/"+url.PathEscape(slug)+"?mode=json", &list); err != nil {
		return nil, err
	}
	var out []Posting
	for _, j := range list {
		remote := remoteOf(j.Categories.Location + " " + j.Categories.Commitment)
		if j.WorkplaceType == "remote" {
			remote = "remote"
		}
		desc := j.DescriptionPlain
		if desc == "" {
			desc = j.Description
		}
		out = append(out, Posting{Company: slug, Title: j.Text, URL: j.HostedURL, Location: j.Categories.Location, Remote: remote,
			Source: "lever", Tags: joinTags(j.Categories.Team, j.Categories.Department), Description: stripHTML(desc)})
	}
	return out, nil
}

func (f fetcher) ashby(ctx context.Context, slug string) ([]Posting, error) {
	var list struct {
		Jobs []struct {
			Title           string `json:"title"`
			JobURL          string `json:"jobUrl"`
			Location        string `json:"location"`
			IsRemote        bool   `json:"isRemote"`
			PublishedAt     string `json:"publishedAt"`
			Department      string `json:"department"`
			Team            string `json:"team"`
			DescriptionText string `json:"descriptionPlain"`
			DescriptionHTML string `json:"descriptionHtml"`
		} `json:"jobs"`
	}
	if err := f.getJSON(ctx, f.url("ashby")+"/"+url.PathEscape(slug)+"?includeCompensation=true", &list); err != nil {
		return nil, err
	}
	var out []Posting
	for _, j := range list.Jobs {
		remote := remoteOf(j.Location)
		if j.IsRemote {
			remote = "remote"
		}
		desc := j.DescriptionText
		if desc == "" {
			desc = j.DescriptionHTML
		}
		out = append(out, Posting{Company: slug, Title: j.Title, URL: j.JobURL, Location: j.Location, Remote: remote, Source: "ashby",
			PostedAt: day(j.PublishedAt), Tags: joinTags(j.Department, j.Team), Description: stripHTML(desc)})
	}
	return out, nil
}

func (f fetcher) remotive(ctx context.Context, q string) ([]Posting, error) {
	var list struct {
		Jobs []struct {
			CompanyName string   `json:"company_name"`
			Title       string   `json:"title"`
			URL         string   `json:"url"`
			Location    string   `json:"candidate_required_location"`
			Published   string   `json:"publication_date"`
			Tags        []string `json:"tags"`
			Salary      string   `json:"salary"`
			Description string   `json:"description"`
		} `json:"jobs"`
	}
	if err := f.getJSON(ctx, f.url("remotive")+"?"+url.Values{"search": {q}, "limit": {"100"}}.Encode(), &list); err != nil {
		return nil, err
	}
	var out []Posting
	for _, j := range list.Jobs {
		smin, smax, cur := store.ParseSalary(j.Salary)
		out = append(out, Posting{Company: j.CompanyName, Title: j.Title, URL: j.URL, Location: j.Location, Remote: "remote", Source: "remotive",
			PostedAt: day(j.Published), Tags: strings.Join(j.Tags, ","), SalaryMin: smin, SalaryMax: smax, Currency: cur, Description: stripHTML(j.Description)})
	}
	return out, nil
}

func (f fetcher) remoteok(ctx context.Context) ([]Posting, error) {
	var rows []map[string]any
	if err := f.getJSON(ctx, f.url("remoteok"), &rows); err != nil {
		return nil, err
	}
	str := func(m map[string]any, k string) string {
		s, _ := m[k].(string)
		return s
	}
	var out []Posting
	for _, m := range rows {
		if _, ok := m["position"]; !ok {
			continue // first row is a legal notice
		}
		var tags []string
		if ts, ok := m["tags"].([]any); ok {
			for _, t := range ts {
				if s, ok := t.(string); ok {
					tags = append(tags, s)
				}
			}
		}
		var smin, smax int64
		if v, ok := m["salary_min"].(float64); ok {
			smin = int64(v)
		}
		if v, ok := m["salary_max"].(float64); ok {
			smax = int64(v)
		}
		cur := ""
		if smax > 0 {
			cur = "USD"
		}
		out = append(out, Posting{Company: str(m, "company"), Title: str(m, "position"), URL: str(m, "url"), Location: str(m, "location"), Remote: "remote",
			Source: "remoteok", PostedAt: day(str(m, "date")), Tags: strings.Join(tags, ","), SalaryMin: smin, SalaryMax: smax, Currency: cur, Description: stripHTML(str(m, "description"))})
	}
	return out, nil
}

func (f fetcher) arbeitnow(ctx context.Context, q string) ([]Posting, error) {
	var list struct {
		Data []struct {
			Slug        string   `json:"slug"`
			CompanyName string   `json:"company_name"`
			Title       string   `json:"title"`
			URL         string   `json:"url"`
			Location    string   `json:"location"`
			Remote      bool     `json:"remote"`
			Tags        []string `json:"tags"`
			JobTypes    []string `json:"job_types"`
			Description string   `json:"description"`
			CreatedAt   int64    `json:"created_at"`
		} `json:"data"`
	}
	if err := f.getJSON(ctx, f.url("arbeitnow")+"?"+url.Values{"search": {q}}.Encode(), &list); err != nil {
		return nil, err
	}
	var out []Posting
	for _, j := range list.Data {
		remote := "unknown"
		if j.Remote {
			remote = "remote"
		}
		posted := ""
		if j.CreatedAt > 0 {
			posted = time.Unix(j.CreatedAt, 0).UTC().Format("2006-01-02")
		}
		out = append(out, Posting{Company: j.CompanyName, Title: j.Title, URL: j.URL, Location: j.Location, Remote: remote, Source: "arbeitnow",
			PostedAt: posted, Tags: strings.Join(append(j.Tags, j.JobTypes...), ","), Description: stripHTML(j.Description)})
	}
	return out, nil
}

func (f fetcher) jobicy(ctx context.Context, q string) ([]Posting, error) {
	var list struct {
		Jobs []struct {
			CompanyName    string   `json:"companyName"`
			JobTitle       string   `json:"jobTitle"`
			URL            string   `json:"url"`
			JobGeo         string   `json:"jobGeo"`
			JobIndustry    []string `json:"jobIndustry"`
			JobType        []string `json:"jobType"`
			PubDate        string   `json:"pubDate"`
			SalaryMin      float64  `json:"annualSalaryMin"`
			SalaryMax      float64  `json:"annualSalaryMax"`
			SalaryCurrency string   `json:"salaryCurrency"`
			Description    string   `json:"jobDescription"`
		} `json:"jobs"`
	}
	if err := f.getJSON(ctx, f.url("jobicy")+"?"+url.Values{"count": {"50"}, "tag": {q}}.Encode(), &list); err != nil {
		return nil, err
	}
	var out []Posting
	for _, j := range list.Jobs {
		out = append(out, Posting{Company: j.CompanyName, Title: j.JobTitle, URL: j.URL, Location: j.JobGeo, Remote: "remote", Source: "jobicy",
			PostedAt: day(j.PubDate), Tags: strings.Join(append(j.JobIndustry, j.JobType...), ","), SalaryMin: int64(j.SalaryMin), SalaryMax: int64(j.SalaryMax),
			Currency: j.SalaryCurrency, Description: stripHTML(j.Description)})
	}
	return out, nil
}

func (f fetcher) himalayas(ctx context.Context, q string) ([]Posting, error) {
	var list struct {
		Jobs []struct {
			CompanyName     string   `json:"companyName"`
			Title           string   `json:"title"`
			ApplicationLink string   `json:"applicationLink"`
			LocationRestr   []string `json:"locationRestrictions"`
			Categories      []string `json:"categories"`
			PubDate         int64    `json:"pubDate"`
			MinSalary       float64  `json:"minSalary"`
			MaxSalary       float64  `json:"maxSalary"`
			Currency        string   `json:"currency"`
			Description     string   `json:"description"`
			ExcerptPlain    string   `json:"excerpt"`
		} `json:"jobs"`
	}
	if err := f.getJSON(ctx, f.url("himalayas")+"?"+url.Values{"limit": {"50"}, "q": {q}}.Encode(), &list); err != nil {
		return nil, err
	}
	var out []Posting
	for _, j := range list.Jobs {
		posted := ""
		if j.PubDate > 0 {
			posted = time.Unix(j.PubDate, 0).UTC().Format("2006-01-02")
		}
		desc := j.Description
		if desc == "" {
			desc = j.ExcerptPlain
		}
		out = append(out, Posting{Company: j.CompanyName, Title: j.Title, URL: j.ApplicationLink, Location: strings.Join(j.LocationRestr, ", "), Remote: "remote",
			Source: "himalayas", PostedAt: posted, Tags: strings.Join(j.Categories, ","), SalaryMin: int64(j.MinSalary), SalaryMax: int64(j.MaxSalary), Currency: j.Currency, Description: stripHTML(desc)})
	}
	return out, nil
}
