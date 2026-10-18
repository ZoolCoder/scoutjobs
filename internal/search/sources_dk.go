package search

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Keyword boards are searched once per query. The Danish ones take an area as slug:
// "jobindex:dk" is all of Denmark, "jobindex:aarhus" keeps postings whose location names Aarhus.
// Every request is checked against the site's robots.txt first.

// AllDenmark is the slug that keeps every posting a Danish board returns.
const AllDenmark = "dk"

// polite fetches u only when the host's robots.txt allows it.
func (f fetcher) polite(ctx context.Context, method, u string, body []byte, accept string) ([]byte, error) {
	pu, err := url.Parse(u)
	if err != nil {
		return nil, err
	}
	if err := robotsAllowed(ctx, f.client, pu); err != nil {
		return nil, err
	}
	return f.fetch(ctx, method, u, body, accept)
}

// inArea keeps postings in the board's area; AllDenmark keeps every one.
func inArea(rows []Posting, area string) []Posting {
	if area == "" || strings.EqualFold(area, AllDenmark) {
		return rows
	}
	area = strings.ToLower(area)
	var out []Posting
	for _, p := range rows {
		if strings.Contains(strings.ToLower(p.Location), area) {
			out = append(out, p)
		}
	}
	return out
}

// latin1 lets encoding/xml read jobindex's ISO-8859-1 feed.
func latin1(charset string, r io.Reader) (io.Reader, error) {
	switch strings.ToLower(charset) {
	case "iso-8859-1", "latin1", "windows-1252":
		b, err := io.ReadAll(r)
		if err != nil {
			return nil, err
		}
		out := make([]byte, 0, len(b)+len(b)/8)
		for _, c := range b {
			out = utf8.AppendRune(out, rune(c))
		}
		return bytes.NewReader(out), nil
	}
	return nil, fmt.Errorf("unsupported charset %q", charset)
}

type rssItem struct {
	Title   string `xml:"title"`
	Link    string `xml:"link"`
	Desc    string `xml:"description"`
	PubDate string `xml:"pubDate"`
}

func rssItems(body []byte) ([]rssItem, error) {
	var doc struct {
		Items []rssItem `xml:"channel>item"`
	}
	d := xml.NewDecoder(bytes.NewReader(body))
	d.CharsetReader = latin1
	if err := d.Decode(&doc); err != nil {
		return nil, err
	}
	return doc.Items, nil
}

var jobindexAreaRe = regexp.MustCompile(`jix_robotjob--area[^>]*>([^<]+)<`)

// jobindex: GET https://www.jobindex.dk/jobsoegning.rss?q=..., titles read "Title, Company".
func (f fetcher) jobindex(ctx context.Context, q, area string) ([]Posting, error) {
	body, err := f.polite(ctx, "GET", f.url("jobindex")+"?"+url.Values{"q": {q}}.Encode(), nil, "application/rss+xml, application/xml")
	if err != nil {
		return nil, err
	}
	items, err := rssItems(body)
	if err != nil {
		return nil, err
	}
	var out []Posting
	for _, it := range items {
		title, company := strings.TrimSpace(it.Title), ""
		if i := strings.LastIndex(title, ", "); i > 0 {
			title, company = strings.TrimSpace(title[:i]), strings.TrimSpace(title[i+2:])
		}
		loc := ""
		if m := jobindexAreaRe.FindStringSubmatch(it.Desc); m != nil {
			loc = strings.TrimSpace(m[1])
		}
		desc := stripHTML(it.Desc)
		out = append(out, Posting{Company: company, Title: title, URL: strings.TrimSpace(it.Link), Location: loc,
			Remote: remoteOf(title + " " + desc[:min(len(desc), 400)]), Source: "jobindex", PostedAt: rssDate(it.PubDate), Description: desc})
	}
	return inArea(out, area), nil
}

// jobbank's summary reads "Fuldtidsjob hos Company, Area, Area (Ansøgningsfrist: 25.10.2026)".
var jobbankDescRe = regexp.MustCompile(`^(.*?)\s*\bhos ([^,]+), (.+?)\s*(?:\(Ansøgningsfrist[^)]*\))?$`)

// jobbank: GET https://jobbank.dk/job/rss/?key=...
func (f fetcher) jobbank(ctx context.Context, q, area string) ([]Posting, error) {
	body, err := f.polite(ctx, "GET", f.url("jobbank")+"?"+url.Values{"key": {q}}.Encode(), nil, "application/rss+xml, application/xml")
	if err != nil {
		return nil, err
	}
	items, err := rssItems(body)
	if err != nil {
		return nil, err
	}
	var out []Posting
	for _, it := range items {
		desc := strings.TrimSpace(stripHTML(it.Desc))
		p := Posting{Title: strings.TrimSpace(it.Title), URL: strings.TrimSpace(it.Link), Source: "jobbank", PostedAt: rssDate(it.PubDate), Description: desc}
		if m := jobbankDescRe.FindStringSubmatch(desc); m != nil {
			p.Tags, p.Company, p.Location = strings.TrimSpace(m[1]), strings.TrimSpace(m[2]), strings.TrimSpace(m[3])
		}
		p.Remote = remoteOf(p.Title + " " + desc)
		out = append(out, p)
	}
	return inArea(out, area), nil
}

var postcodeRe = regexp.MustCompile(`\d{4}\s+(.+)$`)

// jobdanmark: POST https://jobdanmark.dk/api/jobsearch/search/1 with a free-text filter.
func (f fetcher) jobdanmark(ctx context.Context, q, area string) ([]Posting, error) {
	// the API answers 400 unless jobTypes comes first, so the body is built from ordered structs
	type filter struct {
		Type        string `json:"type"`
		Value       string `json:"value"`
		DisplayText string `json:"displayText"`
	}
	filters := []filter{}
	if q != "" {
		filters = append(filters, filter{"freetext", q, q})
	}
	req, _ := json.Marshal(struct {
		JobTypes     []string `json:"jobTypes"`
		Filters      []filter `json:"filters"`
		LocationMode string   `json:"locationMode"`
		Distance     int      `json:"distance"`
	}{[]string{}, filters, "Text", 50})
	u := f.url("jobdanmark")
	body, err := f.polite(ctx, "POST", u, req, "application/json")
	if err != nil {
		return nil, err
	}
	var list struct {
		Items []struct {
			Title     string   `json:"title"`
			Company   string   `json:"companyName"`
			Address   string   `json:"companyAddress"`
			JobTypes  []string `json:"jobTypes"`
			Published string   `json:"publishedDate"` // dd-mm-yyyy
			URL       string   `json:"url"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, err
	}
	origin := u
	if pu, err := url.Parse(u); err == nil {
		origin = pu.Scheme + "://" + pu.Host
	}
	var out []Posting
	for _, j := range list.Items {
		link := j.URL
		if strings.HasPrefix(link, "/") {
			link = origin + link
		}
		loc := j.Address
		if m := postcodeRe.FindStringSubmatch(loc); m != nil {
			loc = strings.TrimSpace(m[1])
		}
		posted := j.Published
		if d, m, y, ok := dmy(posted); ok {
			posted = y + "-" + m + "-" + d
		}
		out = append(out, Posting{Company: j.Company, Title: j.Title, URL: link, Location: loc, Remote: remoteOf(j.Title),
			Source: "jobdanmark", PostedAt: posted, Tags: strings.Join(j.JobTypes, ",")})
	}
	return inArea(out, area), nil
}

func dmy(s string) (d, m, y string, ok bool) {
	parts := strings.Split(s, "-")
	if len(parts) != 3 || len(parts[0]) != 2 || len(parts[1]) != 2 || len(parts[2]) != 4 {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

// freehire: GET https://freehire.me/api/v1/agent/jobs/search, an open index of ATS postings
// worldwide that returns the full description, so no second request per job.
func (f fetcher) freehire(ctx context.Context, q string) ([]Posting, error) {
	v := url.Values{"q": {q}, "limit": {"50"}, "semantic_ratio": {"0"}, "include_description": {"true"},
		"description_format": {"text"}, "posted_within_days": {"30"}}
	body, err := f.polite(ctx, "GET", f.url("freehire")+"?"+v.Encode(), nil, "application/json")
	if err != nil {
		return nil, err
	}
	var list struct {
		Data []struct {
			URL         string   `json:"url"`
			Title       string   `json:"title"`
			Company     string   `json:"company"`
			Location    string   `json:"location"`
			Description string   `json:"description"`
			Skills      []string `json:"skills"`
			WorkMode    string   `json:"work_mode"`
			PostedAt    string   `json:"posted_at"`
			CreatedAt   string   `json:"created_at"`
			Enrichment  struct {
				Seniority string  `json:"seniority"`
				Category  string  `json:"category"`
				SalaryMin float64 `json:"salary_min"`
				SalaryMax float64 `json:"salary_max"`
				Currency  string  `json:"salary_currency"`
			} `json:"enrichment"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, err
	}
	var out []Posting
	for _, j := range list.Data {
		remote := j.WorkMode
		if remote == "" || remote == "onsite" {
			remote = remoteOf(j.Location + " " + j.Title)
		}
		desc := j.Description
		if len(desc) > 4000 {
			desc = desc[:4000]
		}
		link, _, _ := strings.Cut(j.URL, "?utm_source=")
		out = append(out, Posting{Company: j.Company, Title: j.Title, URL: link, Location: j.Location, Remote: remote, Source: "freehire",
			PostedAt: day(first(j.PostedAt, j.CreatedAt)), Tags: joinTags(append([]string{j.Enrichment.Category}, j.Skills...)...),
			SalaryMin: int64(j.Enrichment.SalaryMin), SalaryMax: int64(j.Enrichment.SalaryMax), Currency: j.Enrichment.Currency, Description: desc})
	}
	return out, nil
}

func (f fetcher) danish(ctx context.Context, board, q, area string) ([]Posting, error) {
	switch board {
	case "jobindex":
		return f.jobindex(ctx, q, area)
	case "jobbank":
		return f.jobbank(ctx, q, area)
	}
	return f.jobdanmark(ctx, q, area)
}

// queriesOrAll runs a keyword board once per query, or once unfiltered when there is none.
func queriesOrAll(qs []string) []string {
	if len(qs) == 0 {
		return []string{""}
	}
	return qs
}
