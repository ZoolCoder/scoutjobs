package search

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Workable, SmartRecruiters, Personio and plain RSS/Atom feeds. Same contract as the other
// sources: one call, a []Posting, no paging beyond the first page.

func (f fetcher) fetch(ctx context.Context, method, u string, body []byte, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", accept)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, httpError(u, res.StatusCode)
	}
	return io.ReadAll(io.LimitReader(res.Body, 20<<20))
}

// workable: POST https://apply.workable.com/api/v3/accounts/{slug}/jobs, postings at /{slug}/j/{shortcode}/
func (f fetcher) workable(ctx context.Context, slug string) ([]Posting, error) {
	var list struct {
		Results []struct {
			Title     string `json:"title"`
			Shortcode string `json:"shortcode"`
			Remote    bool   `json:"remote"`
			Workplace string `json:"workplace"` // remote | hybrid | on_site
			Dept      string `json:"department"`
			Published string `json:"published"`
			Location  struct {
				City    string `json:"city"`
				Country string `json:"country"`
			} `json:"location"`
		} `json:"results"`
	}
	body, err := f.fetch(ctx, "POST", f.url("workable")+"/"+url.PathEscape(slug)+"/jobs", []byte(`{"query":"","location":[],"department":[],"worktype":[],"remote":[]}`), "application/json")
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, err
	}
	var out []Posting
	for _, j := range list.Results {
		remote := map[string]string{"remote": "remote", "hybrid": "hybrid", "on_site": "onsite"}[j.Workplace]
		if j.Remote {
			remote = "remote"
		}
		if remote == "" {
			remote = "unknown"
		}
		out = append(out, Posting{Company: slug, Title: j.Title, URL: "https://apply.workable.com/" + slug + "/j/" + j.Shortcode + "/",
			Location: place(j.Location.City, j.Location.Country), Remote: remote, Source: "workable", PostedAt: day(j.Published), Tags: joinTags(j.Dept)})
	}
	return out, nil
}

// smartrecruiters: GET https://api.smartrecruiters.com/v1/companies/{company}/postings, postings at jobs.smartrecruiters.com/{company}/{id}
func (f fetcher) smartrecruiters(ctx context.Context, company string) ([]Posting, error) {
	var list struct {
		Content []struct {
			ID       string `json:"id"`
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
			Function struct {
				Label string `json:"label"`
			} `json:"function"`
		} `json:"content"`
	}
	if err := f.getJSON(ctx, f.url("smartrecruiters")+"/"+url.PathEscape(company)+"/postings?limit=100", &list); err != nil {
		return nil, err
	}
	var out []Posting
	for _, j := range list.Content {
		loc := place(j.Location.City, j.Location.Country)
		remote := remoteOf(loc + " " + j.Name)
		if j.Location.Remote {
			remote = "remote"
		}
		out = append(out, Posting{Company: company, Title: j.Name, URL: "https://jobs.smartrecruiters.com/" + company + "/" + j.ID,
			Location: loc, Remote: remote, Source: "smartrecruiters", PostedAt: day(j.Released), Tags: joinTags(j.Department.Label, j.Function.Label)})
	}
	return out, nil
}

// personio: GET https://{slug}.jobs.personio.de/xml, postings at https://{slug}.jobs.personio.de/job/{id}
func (f fetcher) personio(ctx context.Context, slug string) ([]Posting, error) {
	var doc struct {
		Positions []struct {
			ID         string `xml:"id"`
			Name       string `xml:"name"`
			Office     string `xml:"office"`
			Department string `xml:"department"`
			Employment string `xml:"employmentType"`
			Schedule   string `xml:"schedule"`
			Keywords   string `xml:"keywords"`
			Created    string `xml:"createdAt"`
			Descs      []struct {
				Value string `xml:"value"`
			} `xml:"jobDescriptions>jobDescription"`
		} `xml:"position"`
	}
	base := fmt.Sprintf("https://%s.jobs.personio.de", slug)
	u := base + "/xml"
	if override, ok := f.base["personio"]; ok {
		u = override + "/" + url.PathEscape(slug)
	}
	body, err := f.fetch(ctx, "GET", u, nil, "application/xml")
	if err != nil {
		return nil, err
	}
	if err := xml.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	var out []Posting
	for _, p := range doc.Positions {
		var desc []string
		for _, d := range p.Descs {
			desc = append(desc, stripHTML(d.Value))
		}
		text := strings.Join(desc, "\n")
		out = append(out, Posting{Company: slug, Title: strings.TrimSpace(p.Name), URL: base + "/job/" + p.ID, Location: p.Office,
			Remote: remoteOf(p.Office + " " + p.Name + " " + p.Schedule + " " + text[:min(len(text), 400)]), Source: "personio", PostedAt: day(p.Created),
			Tags: joinTags(p.Department, p.Employment, p.Keywords), Description: text})
	}
	return out, nil
}

// rss reads an RSS 2.0 or Atom feed. The company comes from "Title at Company" when the item
// title has that shape, else from the feed title.
func (f fetcher) rss(ctx context.Context, feedURL string) ([]Posting, error) {
	body, err := f.fetch(ctx, "GET", feedURL, nil, "application/rss+xml, application/atom+xml, application/xml")
	if err != nil {
		return nil, err
	}
	type link struct {
		Href string `xml:"href,attr"`
		Text string `xml:",chardata"`
	}
	type item struct {
		Title    string   `xml:"title"`
		Links    []link   `xml:"link"`
		Desc     string   `xml:"description"`
		Summary  string   `xml:"summary"`
		Content  string   `xml:"content"`
		Encoded  string   `xml:"encoded"`
		PubDate  string   `xml:"pubDate"`
		Updated  string   `xml:"updated"`
		Category []string `xml:"category"`
	}
	var doc struct {
		XMLName xml.Name
		Title   string `xml:"title"`
		Channel struct {
			Title string `xml:"title"`
			Items []item `xml:"item"`
		} `xml:"channel"`
		Entries []item `xml:"entry"`
	}
	if err := xml.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	feedTitle, items := doc.Title, doc.Entries
	if doc.XMLName.Local == "rss" {
		feedTitle, items = doc.Channel.Title, doc.Channel.Items
	}
	var out []Posting
	for _, it := range items {
		title, company := splitTitle(strings.TrimSpace(it.Title), feedTitle)
		href := ""
		for _, l := range it.Links {
			if href = strings.TrimSpace(l.Href); href != "" {
				break
			}
			if href = strings.TrimSpace(l.Text); href != "" {
				break
			}
		}
		desc := stripHTML(first(it.Encoded, it.Content, it.Desc, it.Summary))
		out = append(out, Posting{Company: company, Title: title, URL: href, Remote: remoteOf(title + " " + desc[:min(len(desc), 400)]),
			Source: "rss", PostedAt: rssDate(first(it.PubDate, it.Updated)), Tags: joinTags(it.Category...), Description: desc})
	}
	return out, nil
}

func splitTitle(title, fallback string) (string, string) {
	for _, sep := range []string{" at ", " @ ", " - ", " – "} {
		if i := strings.LastIndex(title, sep); i > 0 && i < len(title)-len(sep) {
			return strings.TrimSpace(title[:i]), strings.TrimSpace(title[i+len(sep):])
		}
	}
	return title, strings.TrimSpace(fallback)
}

func rssDate(s string) string {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC1123Z, time.RFC1123, time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("2006-01-02")
		}
	}
	return day(s)
}

func place(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, ", ")
}

func first(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
