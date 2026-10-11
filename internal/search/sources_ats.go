package search

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Recruitee and BambooHR boards, Hacker News "Who is hiring" threads, and the single-posting
// endpoints behind FetchPosting. Same contract as the other sources.

// recruitee: GET https://{slug}.recruitee.com/api/offers/
func (f fetcher) recruitee(ctx context.Context, slug string) ([]Posting, error) {
	var list struct {
		Offers []struct {
			Title        string   `json:"title"`
			CareersURL   string   `json:"careers_url"`
			Location     string   `json:"location"`
			Remote       bool     `json:"remote"`
			Department   string   `json:"department"`
			Published    string   `json:"published_at"`
			Description  string   `json:"description"`
			Requirements string   `json:"requirements"`
			Tags         []string `json:"tags"`
		} `json:"offers"`
	}
	u := fmt.Sprintf("https://%s.recruitee.com/api/offers/", slug)
	if override, ok := f.base["recruitee"]; ok {
		u = override + "/" + url.PathEscape(slug)
	}
	if err := f.getJSON(ctx, u, &list); err != nil {
		return nil, err
	}
	var out []Posting
	for _, o := range list.Offers {
		remote := remoteOf(o.Location + " " + o.Title)
		if o.Remote {
			remote = "remote"
		}
		out = append(out, Posting{Company: slug, Title: o.Title, URL: o.CareersURL, Location: o.Location, Remote: remote, Source: "recruitee",
			PostedAt: day(o.Published), Tags: joinTags(append([]string{o.Department}, o.Tags...)...), Description: stripHTML(o.Description + " " + o.Requirements)})
	}
	return out, nil
}

// bamboohr: GET https://{slug}.bamboohr.com/careers/list, postings at /careers/{id}
type bamboohrJob struct {
	ID         json.Number `json:"id"`
	Name       string      `json:"jobOpeningName"`
	Department string      `json:"departmentLabel"`
	Employment string      `json:"employmentStatusLabel"`
	IsRemote   bool        `json:"isRemote"`
	Posted     string      `json:"datePosted"`
	Desc       string      `json:"description"`
	Location   struct {
		City  string `json:"city"`
		State string `json:"state"`
	} `json:"location"`
}

func (f fetcher) bamboohrBase(slug string) string {
	if override, ok := f.base["bamboohr"]; ok {
		return override + "/" + url.PathEscape(slug)
	}
	return fmt.Sprintf("https://%s.bamboohr.com/careers", slug)
}

func (j bamboohrJob) posting(slug, base string) Posting {
	loc := place(j.Location.City, j.Location.State)
	remote := remoteOf(loc + " " + j.Name)
	if j.IsRemote {
		remote = "remote"
	}
	return Posting{Company: slug, Title: j.Name, URL: base + "/" + j.ID.String(), Location: loc, Remote: remote, Source: "bamboohr",
		PostedAt: day(j.Posted), Tags: joinTags(j.Department, j.Employment), Description: stripHTML(j.Desc)}
}

func (f fetcher) bamboohr(ctx context.Context, slug string) ([]Posting, error) {
	var list struct {
		Result []bamboohrJob `json:"result"`
	}
	base := f.bamboohrBase(slug)
	if err := f.getJSON(ctx, base+"/list", &list); err != nil {
		return nil, err
	}
	public := fmt.Sprintf("https://%s.bamboohr.com/careers", slug)
	var out []Posting
	for _, j := range list.Result {
		out = append(out, j.posting(slug, public))
	}
	return out, nil
}

func (f fetcher) bamboohrJob(ctx context.Context, slug, id string) (Posting, error) {
	var detail struct {
		Result struct {
			JobOpening bamboohrJob `json:"jobOpening"`
		} `json:"result"`
	}
	if err := f.getJSON(ctx, f.bamboohrBase(slug)+"/"+url.PathEscape(id)+"/detail", &detail); err != nil {
		return Posting{}, err
	}
	j := detail.Result.JobOpening
	if j.ID == "" {
		j.ID = json.Number(id)
	}
	return j.posting(slug, fmt.Sprintf("https://%s.bamboohr.com/careers", slug)), nil
}

// hn reads the latest "Ask HN: Who is hiring?" thread through the Algolia API: one call finds the
// thread, one searches its top-level comments for the query. Posts follow the "Company | Role |
// Location | ..." convention loosely, so the fields are a best effort and the comment is the description.
func (f fetcher) hn(ctx context.Context, q string) ([]Posting, error) {
	base := f.url("hn")
	var threads struct {
		Hits []struct {
			ID    string `json:"objectID"`
			Title string `json:"title"`
		} `json:"hits"`
	}
	if err := f.getJSON(ctx, base+"/search_by_date?"+url.Values{"tags": {"story,author_whoishiring"}, "query": {"who is hiring"}, "hitsPerPage": {"5"}}.Encode(), &threads); err != nil {
		return nil, err
	}
	thread := ""
	for _, h := range threads.Hits {
		if strings.Contains(strings.ToLower(h.Title), "who is hiring") {
			thread = h.ID
			break
		}
	}
	if thread == "" {
		return nil, fmt.Errorf("no who-is-hiring thread found")
	}
	var comments struct {
		Hits []struct {
			ID       string      `json:"objectID"`
			ParentID json.Number `json:"parent_id"`
			Text     string      `json:"comment_text"`
			Created  string      `json:"created_at"`
		} `json:"hits"`
	}
	if err := f.getJSON(ctx, base+"/search?"+url.Values{"tags": {"comment,story_" + thread}, "query": {q}, "hitsPerPage": {"100"}}.Encode(), &comments); err != nil {
		return nil, err
	}
	var out []Posting
	for _, c := range comments.Hits {
		if c.ParentID.String() != thread || strings.TrimSpace(c.Text) == "" {
			continue
		}
		p := hnPosting(c.Text)
		p.URL = "https://news.ycombinator.com/item?id=" + c.ID
		p.PostedAt = day(c.Created)
		out = append(out, p)
	}
	return out, nil
}

var roleRe = regexp.MustCompile(`(?i)\b(engineer|developer|programmer|architect|designer|manager|lead|scientist|analyst|devops|sre|cto|founder|intern|researcher|product|recruiter|sales|marketing|support|ops|qa|tester)\b`)

func hnPosting(html string) Posting {
	head := html
	if i := strings.Index(html, "<p>"); i > 0 {
		head = html[:i]
	}
	head = stripHTML(head)
	parts := strings.Split(head, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	p := Posting{Company: parts[0], Source: "hn", Description: stripHTML(html), Remote: remoteOf(head)}
	var rest []string
	for _, part := range parts[1:] {
		if p.Title == "" && roleRe.MatchString(part) {
			p.Title = part
			continue
		}
		rest = append(rest, part)
	}
	if p.Title == "" && len(rest) > 0 {
		p.Title, rest = rest[0], rest[1:]
	}
	if p.Title == "" {
		p.Title = "see posting"
	}
	if len(p.Company) > 80 {
		p.Company = p.Company[:80]
	}
	p.Location = strings.Join(rest, " · ")
	if len(p.Location) > 100 {
		p.Location = p.Location[:100]
	}
	return p
}

// ---- single postings ----

func (f fetcher) greenhouseJob(ctx context.Context, slug, id string) (Posting, error) {
	var j struct {
		Title       string `json:"title"`
		AbsoluteURL string `json:"absolute_url"`
		UpdatedAt   string `json:"updated_at"`
		Content     string `json:"content"`
		Location    struct {
			Name string `json:"name"`
		} `json:"location"`
	}
	if err := f.getJSON(ctx, f.url("greenhouse")+"/"+url.PathEscape(slug)+"/jobs/"+url.PathEscape(id), &j); err != nil {
		return Posting{}, err
	}
	return Posting{Company: slug, Title: j.Title, URL: j.AbsoluteURL, Location: j.Location.Name, Remote: remoteOf(j.Location.Name + " " + j.Title),
		Source: "greenhouse", PostedAt: day(j.UpdatedAt), Description: stripHTML(j.Content)}, nil
}

func (f fetcher) leverJob(ctx context.Context, slug, id string) (Posting, error) {
	var j struct {
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
	if err := f.getJSON(ctx, f.url("lever")+"/"+url.PathEscape(slug)+"/"+url.PathEscape(id), &j); err != nil {
		return Posting{}, err
	}
	remote := remoteOf(j.Categories.Location + " " + j.Categories.Commitment)
	if j.WorkplaceType == "remote" {
		remote = "remote"
	}
	return Posting{Company: slug, Title: j.Text, URL: j.HostedURL, Location: j.Categories.Location, Remote: remote, Source: "lever",
		Tags: joinTags(j.Categories.Team, j.Categories.Department), Description: stripHTML(first(j.DescriptionPlain, j.Description))}, nil
}

// workableJob: GET https://apply.workable.com/api/v2/accounts/{slug}/jobs/{shortcode}
func (f fetcher) workableJob(ctx context.Context, slug, code string) (Posting, error) {
	var j struct {
		Title        string `json:"title"`
		Description  string `json:"description"`
		Requirements string `json:"requirements"`
		Remote       bool   `json:"remote"`
		Workplace    string `json:"workplace"`
		Published    string `json:"published_on"`
		Department   string `json:"department"`
		Location     struct {
			City    string `json:"city"`
			Country string `json:"country"`
		} `json:"location"`
	}
	base := "https://apply.workable.com/api/v2/accounts"
	if override, ok := f.base["workable_job"]; ok {
		base = override
	}
	if err := f.getJSON(ctx, base+"/"+url.PathEscape(slug)+"/jobs/"+url.PathEscape(code), &j); err != nil {
		return Posting{}, err
	}
	remote := map[string]string{"remote": "remote", "hybrid": "hybrid", "on_site": "onsite"}[j.Workplace]
	if j.Remote {
		remote = "remote"
	}
	if remote == "" {
		remote = "unknown"
	}
	return Posting{Company: slug, Title: j.Title, URL: "https://apply.workable.com/" + slug + "/j/" + code + "/", Location: place(j.Location.City, j.Location.Country),
		Remote: remote, Source: "workable", PostedAt: day(j.Published), Tags: joinTags(j.Department), Description: stripHTML(j.Description + " " + j.Requirements)}, nil
}

func (f fetcher) smartrecruitersJob(ctx context.Context, company, id string) (Posting, error) {
	var j struct {
		Name     string `json:"name"`
		Released string `json:"releasedDate"`
		Location struct {
			City    string `json:"city"`
			Country string `json:"country"`
			Remote  bool   `json:"remote"`
		} `json:"location"`
		Department struct {
			Label string `json:"label"`
		} `json:"department"`
		JobAd struct {
			Sections struct {
				Description struct {
					Text string `json:"text"`
				} `json:"jobDescription"`
				Qualifications struct {
					Text string `json:"text"`
				} `json:"qualifications"`
			} `json:"sections"`
		} `json:"jobAd"`
	}
	if err := f.getJSON(ctx, f.url("smartrecruiters")+"/"+url.PathEscape(company)+"/postings/"+url.PathEscape(id), &j); err != nil {
		return Posting{}, err
	}
	loc := place(j.Location.City, j.Location.Country)
	remote := remoteOf(loc + " " + j.Name)
	if j.Location.Remote {
		remote = "remote"
	}
	return Posting{Company: company, Title: j.Name, URL: "https://jobs.smartrecruiters.com/" + company + "/" + id, Location: loc, Remote: remote,
		Source: "smartrecruiters", PostedAt: day(j.Released), Tags: joinTags(j.Department.Label),
		Description: stripHTML(j.JobAd.Sections.Description.Text + " " + j.JobAd.Sections.Qualifications.Text)}, nil
}
