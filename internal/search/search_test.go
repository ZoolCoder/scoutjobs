package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRunAgainstFakeBoards(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/gh/acme/jobs", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"jobs":[{"id":1,"title":"Senior Go Engineer","absolute_url":"https://acme/1","updated_at":"2026-10-01T00:00:00Z","location":{"name":"Remote - EU"}},{"id":2,"title":"Good Sales Rep","absolute_url":"https://acme/2","location":{"name":"Berlin"}}]}`))
	})
	mux.HandleFunc("/gh/acme/jobs/1", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"content":"&lt;p&gt;Build &lt;b&gt;services&lt;/b&gt; in Go&lt;/p&gt;"}`))
	})
	mux.HandleFunc("/lever/netlify", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"text":"Go Platform Engineer","hostedUrl":"https://l/1","workplaceType":"remote","descriptionPlain":"k8s and go","categories":{"location":"EU","team":"Platform"}}]`))
	})
	mux.HandleFunc("/remotive", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("search") != "go" {
			t.Errorf("remotive query: %s", r.URL.RawQuery)
		}
		w.Write([]byte(`{"jobs":[{"company_name":"Acme","title":"Senior Go Engineer","url":"dup"},{"company_name":"Zed","title":"Rust Dev","url":"z","tags":["rust"]}]}`))
	})
	mux.HandleFunc("/remoteok", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"legal":"notice"},{"company":"Okta","position":"Go Backend","url":"o","tags":["go"],"salary_min":100000,"salary_max":150000,"date":"2026-10-02T00:00:00+00:00"}]`))
	})
	mux.HandleFunc("/arbeitnow", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"data":[]}`)) })
	mux.HandleFunc("/jobicy", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) })
	mux.HandleFunc("/himalayas", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"jobs":[]}`)) })
	mux.HandleFunc("/hn/search_by_date", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"hits":[]}`)) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	r := Run(context.Background(), Options{
		Queries: []string{"go"}, Boards: []string{"greenhouse:acme", "lever:netlify", "bogus"}, Remote: true, Limit: 10, Client: srv.Client(),
		BaseURL: map[string]string{"greenhouse": srv.URL + "/gh", "lever": srv.URL + "/lever", "remotive": srv.URL + "/remotive", "remoteok": srv.URL + "/remoteok",
			"arbeitnow": srv.URL + "/arbeitnow", "jobicy": srv.URL + "/jobicy", "himalayas": srv.URL + "/himalayas", "hn": srv.URL + "/hn"},
	})
	var titles []string
	for _, p := range r.Postings {
		titles = append(titles, p.Source+"/"+p.Title)
	}
	got := strings.Join(titles, " ")
	want := "greenhouse/Senior Go Engineer lever/Go Platform Engineer remoteok/Go Backend"
	if got != want {
		t.Errorf("postings:\n got %s\nwant %s", got, want)
	}
	if r.Postings[0].Description != "Build services in Go" || r.Postings[0].Remote != "remote" {
		t.Errorf("greenhouse posting: %+v", r.Postings[0])
	}
	if r.Postings[2].SalaryMax != 150000 || r.Postings[2].Currency != "USD" {
		t.Errorf("remoteok salary: %+v", r.Postings[2])
	}
	if len(r.Errors) != 3 || !strings.Contains(r.Errors[0], `"bogus"`) || !strings.Contains(r.Errors[1], "jobicy") || !strings.Contains(r.Errors[2], "hn:go: no who-is-hiring thread") {
		t.Errorf("errors: %v", r.Errors)
	}
	off := Run(context.Background(), Options{
		Queries: []string{"go"}, Remote: true, Off: []string{"remoteok", "jobicy", "hn"}, Limit: 10, Client: srv.Client(),
		BaseURL: map[string]string{"remotive": srv.URL + "/remotive", "remoteok": srv.URL + "/remoteok",
			"arbeitnow": srv.URL + "/arbeitnow", "jobicy": srv.URL + "/jobicy", "himalayas": srv.URL + "/himalayas", "hn": srv.URL + "/hn"},
	})
	if len(off.Postings) != 1 || off.Postings[0].Source != "remotive" || len(off.Errors) != 0 {
		t.Errorf("boards turned off were searched: %+v %v", off.Postings, off.Errors)
	}
}

func TestWordsMatch(t *testing.T) {
	if wordsMatch("go", "Good Sales Rep") {
		t.Error("go matched good")
	}
	if !wordsMatch("senior go", "Senior Go Engineer (m/f/d)") {
		t.Error("multi-word query failed")
	}
	if !wordsMatch("c++", "C++ developer") {
		t.Error("punctuation query failed")
	}
}

func TestMoreSources(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/workable/acme/jobs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("workable method %s", r.Method)
		}
		w.Write([]byte(`{"results":[{"title":"Go Developer","shortcode":"AB12","remote":true,"workplace":"hybrid","department":"Eng","published":"2026-10-01T00:00:00Z","location":{"city":"Lisbon","country":"Portugal"}}]}`))
	})
	mux.HandleFunc("/sr/Acme/postings", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"content":[{"id":"744","name":"Backend Engineer (Go)","releasedDate":"2026-09-30T10:00:00.000Z","location":{"city":"Berlin","country":"de","remote":false},"department":{"label":"Platform"},"function":{"label":"Engineering"}}]}`))
	})
	mux.HandleFunc("/personio/acme", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<?xml version="1.0"?><workzag-jobs><position><id>99</id><name> Senior Go Engineer </name><office>Remote</office><department>Tech</department><employmentType>permanent</employmentType><schedule>full-time</schedule><keywords>go,aws</keywords><createdAt>2026-09-28</createdAt><jobDescriptions><jobDescription><name>Tasks</name><value>&lt;p&gt;Build services in Go&lt;/p&gt;</value></jobDescription></jobDescriptions></position></workzag-jobs>`))
	})
	mux.HandleFunc("/feed.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<?xml version="1.0"?><rss version="2.0"><channel><title>Jobs Feed</title><item><title>Go Engineer at Zed</title><link>https://zed/1</link><description>&lt;b&gt;Go&lt;/b&gt; and k8s</description><pubDate>Mon, 05 Oct 2026 08:00:00 +0000</pubDate><category>go</category></item><item><title>Rust Dev</title><link>https://r/2</link></item></channel></rss>`))
	})
	mux.HandleFunc("/atom.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<?xml version="1.0"?><feed xmlns="http://www.w3.org/2005/Atom"><title>Atom Jobs</title><entry><title>Platform Engineer (Go) - Okta</title><link href="https://okta/3"/><summary>remote first, Go</summary><updated>2026-10-02T00:00:00Z</updated></entry></feed>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	r := Run(context.Background(), Options{
		Queries: []string{"go"}, Boards: []string{"workable:acme", "smartrecruiters:Acme", "personio:acme", "rss:" + srv.URL + "/feed.xml", "rss:" + srv.URL + "/atom.xml"},
		Limit: 10, Client: srv.Client(),
		BaseURL: map[string]string{"workable": srv.URL + "/workable", "smartrecruiters": srv.URL + "/sr", "personio": srv.URL + "/personio"},
	})
	if len(r.Errors) > 0 {
		t.Fatalf("errors: %v", r.Errors)
	}
	got := map[string]Posting{}
	for _, p := range r.Postings {
		got[p.Source+"/"+p.Company] = p
	}
	if len(got) != 5 {
		t.Fatalf("want 5 postings, got %d: %v", len(got), r.Postings)
	}
	w := got["workable/acme"]
	if w.URL != "https://apply.workable.com/acme/j/AB12/" || w.Remote != "remote" || w.Location != "Lisbon, Portugal" || w.PostedAt != "2026-10-01" {
		t.Errorf("workable: %+v", w)
	}
	s := got["smartrecruiters/Acme"]
	if s.URL != "https://jobs.smartrecruiters.com/Acme/744" || s.Tags != "Platform,Engineering" || s.PostedAt != "2026-09-30" {
		t.Errorf("smartrecruiters: %+v", s)
	}
	pe := got["personio/acme"]
	if pe.Title != "Senior Go Engineer" || pe.URL != "https://acme.jobs.personio.de/job/99" || pe.Remote != "remote" || !strings.Contains(pe.Description, "Build services in Go") {
		t.Errorf("personio: %+v", pe)
	}
	z := got["rss/Zed"]
	if z.Title != "Go Engineer" || z.URL != "https://zed/1" || z.PostedAt != "2026-10-05" || z.Tags != "go" || z.Description != "Go and k8s" {
		t.Errorf("rss: %+v", z)
	}
	o := got["rss/Okta"]
	if o.Title != "Platform Engineer (Go)" || o.URL != "https://okta/3" || o.PostedAt != "2026-10-02" {
		t.Errorf("atom: %+v", o)
	}
	if _, ok := got["rss/Jobs Feed"]; ok {
		t.Errorf("Rust Dev should not match the go query")
	}
}
