package search

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// The Bundesagentur für Arbeit's Jobsuche is Germany's public job board. Its API answers with the
// key the agency publishes for every client. The slug is an area: "arbeitsagentur:de" is all of
// Germany, "arbeitsagentur:berlin" keeps postings within 25 km of Berlin. A posting's URL is its
// page on arbeitsagentur.de, which links on to the employer's own form; FetchPosting reads the
// full description from it.

// AllGermany is the slug that searches the whole country.
const AllGermany = "de"

const (
	jobsucheKey  = "jobboerse-jobsuche"
	jobdetailURL = "https://www.arbeitsagentur.de/jobsuche/jobdetail/"
)

type jobsucheJob struct {
	Title       string  `json:"stellenangebotsTitel"`
	Description string  `json:"stellenangebotsBeschreibung"` // details only
	Company     string  `json:"firma"`
	Ref         string  `json:"referenznummer"`
	Published   string  `json:"datumErsteVeroeffentlichung"`
	Job         string  `json:"hauptberuf"`
	FullTime    *bool   `json:"arbeitszeitVollzeit"`
	Term        string  `json:"vertragsdauer"`
	HomeOff     *bool   `json:"homeofficemoeglich"`
	SalaryMin   float64 `json:"gehaltsspanneVon"`
	SalaryMax   float64 `json:"gehaltsspanneBis"`
	Places      []struct {
		Address struct {
			City string `json:"ort"`
		} `json:"adresse"`
	} `json:"stellenlokationen"`
}

func (f fetcher) arbeitsagentur(ctx context.Context, q, area string) ([]Posting, error) {
	v := url.Values{"angebotsart": {"1"}, "size": {"50"}} // 1: employment, not apprenticeships
	if q != "" {
		v.Set("was", q)
	}
	if area != "" && !strings.EqualFold(area, AllGermany) {
		v.Set("wo", area)
		v.Set("umkreis", "25")
	}
	var list struct {
		Results []jobsucheJob `json:"ergebnisliste"`
	}
	if err := f.jobsuche(ctx, f.url("arbeitsagentur")+"?"+v.Encode(), &list); err != nil {
		return nil, err
	}
	var out []Posting
	for _, j := range list.Results {
		out = append(out, j.posting())
	}
	return out, nil
}

// arbeitsagenturJob reads one posting by its reference number; the details API wants it base64.
func (f fetcher) arbeitsagenturJob(ctx context.Context, ref string) (Posting, error) {
	var j jobsucheJob
	if err := f.jobsuche(ctx, f.url("arbeitsagentur-detail")+"/"+base64.StdEncoding.EncodeToString([]byte(ref)), &j); err != nil {
		return Posting{}, err
	}
	if j.Ref == "" {
		j.Ref = ref
	}
	return j.posting(), nil
}

func (f fetcher) jobsuche(ctx context.Context, u string, v any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-API-Key", jobsucheKey)
	res, err := f.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return httpError(u, res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 20<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}

func (j jobsucheJob) posting() Posting {
	loc := "Germany"
	if len(j.Places) > 0 && j.Places[0].Address.City != "" {
		loc = j.Places[0].Address.City + ", Germany"
	}
	remote := "unknown"
	if j.HomeOff != nil {
		remote = "onsite"
		if *j.HomeOff {
			remote = "hybrid" // home office "by agreement": rarely fully remote
		}
	}
	tags := []string{j.Job}
	if j.FullTime != nil {
		tags = append(tags, map[bool]string{true: "full-time", false: "part-time"}[*j.FullTime])
	}
	if j.Term == "BEFRISTET" {
		tags = append(tags, "fixed-term")
	}
	p := Posting{Company: j.Company, Title: j.Title, URL: jobdetailURL + url.PathEscape(j.Ref), Location: loc, Remote: remote,
		Source: "arbeitsagentur", PostedAt: day(j.Published), Tags: joinTags(tags...), Description: strings.TrimSpace(j.Description)}
	if j.SalaryMin > 0 {
		p.SalaryMin, p.SalaryMax, p.Currency = int64(j.SalaryMin), int64(j.SalaryMax), "EUR"
	}
	return p
}
