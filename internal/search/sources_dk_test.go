package search

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDanishBoardsAndFreehire(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("User-agent: *\nDisallow: /jobbank/\n"))
	})
	mux.HandleFunc("/jobindex", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") != "python" {
			t.Errorf("jobindex query: %s", r.URL.RawQuery)
		}
		// ISO-8859-1: \xf8 is ø. The first item names python only in the full posting: the board's match stands.
		w.Write([]byte("<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?><rss version=\"2.0\"><channel><title>Jobindex</title>" +
			"<item><title>Data Engineer, Novo Nordisk A/S</title><link>https://www.jobindex.dk/vis-job/h1</link>" +
			"<description>&#x3C;span class=&#x22;jix_robotjob--area&#x22;&#x3E;K\xf8benhavn K&#x3C;/span&#x3E;&#x3C;p&#x3E;Data platforms&#x3C;/p&#x3E;</description>" +
			"<pubDate>Fri, 16 Oct 2026 00:00:00 +0200</pubDate></item>" +
			"<item><title>Python Engineer, Grundfos</title><link>https://www.jobindex.dk/vis-job/h2</link>" +
			"<description>&#x3C;span class=&#x22;jix_robotjob--area&#x22;&#x3E;Bjerringbro&#x3C;/span&#x3E;</description></item></channel></rss>"))
	})
	mux.HandleFunc("/jobbank/rss/", func(w http.ResponseWriter, r *http.Request) { t.Error("jobbank fetched past robots.txt") })
	mux.HandleFunc("/jobdanmark", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != "POST" || !strings.Contains(string(body), `"value":"python"`) {
			t.Errorf("jobdanmark request: %s %s", r.Method, body)
		}
		w.Write([]byte(`{"items":[{"title":"Python Analyst","companyName":"Syddansk Universitet","companyAddress":"Campusvej 55, 5230 Odense M","jobTypes":["fuldtid"],"publishedDate":"15-10-2026","url":"/job/python-analyst"}]}`))
	})
	mux.HandleFunc("/freehire", func(w http.ResponseWriter, r *http.Request) {
		if q := r.URL.Query(); q.Get("q") != "python" || q.Get("include_description") != "true" {
			t.Errorf("freehire query: %s", r.URL.RawQuery)
		}
		w.Write([]byte(`{"data":[{"url":"https://jobs.acme.io/1?utm_source=freehire.me","title":"Senior Python Engineer","company":"Acme","location":"Berlin","description":"Python services","skills":["python","aws"],"work_mode":"remote","posted_at":"2026-10-10T00:00:00Z","enrichment":{"category":"backend","salary_min":90000,"salary_max":120000,"salary_currency":"EUR"}}]}`))
	})
	for _, p := range []string{"/remotive", "/arbeitnow", "/jobicy", "/himalayas", "/remoteok", "/hn/search_by_date"} {
		mux.HandleFunc(p, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) })
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()
	base := map[string]string{"jobindex": srv.URL + "/jobindex", "jobbank": srv.URL + "/jobbank/rss/", "jobdanmark": srv.URL + "/jobdanmark", "freehire": srv.URL + "/freehire"}

	r := Run(context.Background(), Options{Queries: []string{"python"}, Boards: []string{"jobindex:københavn", "jobbank:dk", "jobdanmark:dk"},
		Remote: true, Off: []string{"remotive", "arbeitnow", "jobicy", "himalayas", "remoteok", "hn"}, Limit: 10, Client: srv.Client(), BaseURL: base})
	var got []string
	for _, p := range r.Postings {
		got = append(got, p.Source+"/"+p.Company+"/"+p.Title+"/"+p.Location+"/"+p.PostedAt)
	}
	want := []string{
		"jobindex/Novo Nordisk A/S/Data Engineer/København K/2026-10-16",
		"jobdanmark/Syddansk Universitet/Python Analyst/Odense M/2026-10-15",
		"freehire/Acme/Senior Python Engineer/Berlin/2026-10-10",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("postings:\n got %v\nwant %v", got, want)
	}
	if len(r.Errors) != 1 || !strings.Contains(r.Errors[0], "jobbank:dk:python") || !strings.Contains(r.Errors[0], "robots.txt") {
		t.Errorf("errors: %v", r.Errors)
	}
	if len(r.Postings) == 3 {
		if fh := r.Postings[2]; fh.URL != "https://jobs.acme.io/1" || fh.Remote != "remote" || fh.SalaryMax != 120000 || fh.Description != "Python services" {
			t.Errorf("freehire posting: %+v", fh)
		}
		if jd := r.Postings[1]; jd.URL != srv.URL+"/job/python-analyst" {
			t.Errorf("jobdanmark url: %s", jd.URL)
		}
	}
}

func TestJobbankSummary(t *testing.T) {
	for in, want := range map[string][3]string{
		"Fuldtidsjob hos Ørsted, Storkøbenhavn (Ansøgningsfrist: 25.10.2026)":                           {"Fuldtidsjob", "Ørsted", "Storkøbenhavn"},
		"Fuldtidsjob, Graduate/trainee hos CERN - The European, Udlandet (øvrige) (Ansøgningsfrist: 1)": {"Fuldtidsjob, Graduate/trainee", "CERN - The European", "Udlandet (øvrige)"},
		"Fuldtidsjob hos Teknologisk Institut, Storkøbenhavn, Østjylland (Aarhus)":                      {"Fuldtidsjob", "Teknologisk Institut", "Storkøbenhavn, Østjylland (Aarhus)"},
	} {
		m := jobbankDescRe.FindStringSubmatch(in)
		if m == nil || [3]string{m[1], m[2], m[3]} != want {
			t.Errorf("%s: %q", in, m)
		}
	}
}
