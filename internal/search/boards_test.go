package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseBoard(t *testing.T) {
	good := map[string]string{
		"greenhouse:stripe":                                           "greenhouse:stripe",
		" lever:netlify ":                                             "lever:netlify",
		"https://boards.greenhouse.io/stripe":                         "greenhouse:stripe",
		"https://job-boards.greenhouse.io/stripe/jobs/123":            "greenhouse:stripe",
		"https://boards.greenhouse.io/embed/job_app?for=acme&token=9": "greenhouse:acme",
		"https://jobs.lever.co/netlify/":                              "lever:netlify",
		"https://jobs.ashbyhq.com/linear/abc":                         "ashby:linear",
		"https://apply.workable.com/acme/":                            "workable:acme",
		"https://acme.workable.com/jobs/1":                            "workable:acme",
		"https://jobs.smartrecruiters.com/Acme/744-go-engineer":       "smartrecruiters:Acme",
		"https://acme.jobs.personio.de/job/99":                        "personio:acme",
		"https://acme.recruitee.com/o/go-dev":                         "recruitee:acme",
		"https://acme.bamboohr.com/careers/42":                        "bamboohr:acme",
		"https://example.com/jobs.xml":                                "rss:https://example.com/jobs.xml",
		"https://example.com/careers/feed/":                           "rss:https://example.com/careers/feed/",
		"rss:https://example.com/jobs.rss":                            "rss:https://example.com/jobs.rss",
	}
	for in, want := range good {
		got, err := ParseBoard(in)
		if err != nil || got != want {
			t.Errorf("ParseBoard(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := map[string]string{
		"":                                     "empty",
		"stripe":                               "source:slug",
		"monster:x":                            "unknown source",
		"https://www.linkedin.com/jobs/view/1": "LinkedIn has no public job API",
		"https://de.indeed.com/viewjob?jk=1":   "Indeed has no public job API",
		"https://example.com/careers":          "no known ATS",
		"https://www.recruitee.com/":           "no known ATS",
	}
	for in, want := range bad {
		if _, err := ParseBoard(in); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ParseBoard(%q) error = %v; want %q", in, err, want)
		}
	}
	boards, errs := NormalizeBoards([]string{"https://jobs.lever.co/netlify", "lever:netlify", "nope"})
	if len(boards) != 1 || boards[0] != "lever:netlify" || len(errs) != 1 {
		t.Errorf("NormalizeBoards: %v %v", boards, errs)
	}
}

func atsServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/recruitee/acme", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"offers":[{"title":"Go Developer","careers_url":"https://acme.recruitee.com/o/go-developer","location":"Berlin, Germany","remote":true,"department":"Engineering","published_at":"2026-10-03T10:00:00Z","description":"<p>Write Go</p>","tags":["go"]},{"title":"Sales Lead","careers_url":"https://acme.recruitee.com/o/sales-lead","location":"Berlin"}]}`))
	})
	mux.HandleFunc("/bamboohr/acme/list", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"result":[{"id":42,"jobOpeningName":"Backend Engineer (Go)","departmentLabel":"Platform","employmentStatusLabel":"Full-Time","isRemote":false,"datePosted":"2026-10-02","location":{"city":"Austin","state":"TX"}}]}`))
	})
	mux.HandleFunc("/bamboohr/acme/42/detail", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"result":{"jobOpening":{"id":"42","jobOpeningName":"Backend Engineer (Go)","departmentLabel":"Platform","description":"<p>Build APIs in Go</p>","location":{"city":"Austin","state":"TX"}}}}`))
	})
	mux.HandleFunc("/hn/search_by_date", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"hits":[{"objectID":"500","title":"Ask HN: Who wants to be hired? (October 2026)"},{"objectID":"501","title":"Ask HN: Who is hiring? (October 2026)"}]}`))
	})
	mux.HandleFunc("/hn/search", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("tags") != "comment,story_501" || r.URL.Query().Get("query") != "go" {
			http.Error(w, "wrong query "+r.URL.RawQuery, 400)
			return
		}
		w.Write([]byte(`{"hits":[{"objectID":"777","parent_id":501,"created_at":"2026-10-01T12:00:00Z","comment_text":"Tailscale | Senior Go Engineer | Remote (US, EU) | Full-time<p>We build <i>Go</i> things."},{"objectID":"778","parent_id":777,"comment_text":"reply, not a posting"}]}`))
	})
	mux.HandleFunc("/gh/acme/jobs/123", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"title":"Staff Go Engineer","absolute_url":"https://boards.greenhouse.io/acme/jobs/123","updated_at":"2026-09-30T00:00:00Z","content":"&lt;p&gt;Go, Postgres&lt;/p&gt;","location":{"name":"Remote - Europe"}}`))
	})
	mux.HandleFunc("/lever/acme/abc", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"text":"Go Engineer","hostedUrl":"https://jobs.lever.co/acme/abc","workplaceType":"remote","descriptionPlain":"Build with Go","categories":{"location":"Lisbon","team":"Core"}}`))
	})
	mux.HandleFunc("/workable/acme/jobs/AB12", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"title":"Go Developer","description":"<p>Go</p>","workplace":"hybrid","published_on":"2026-10-01","department":"Eng","location":{"city":"Lisbon","country":"Portugal"}}`))
	})
	mux.HandleFunc("/sr/Acme/postings/744", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"name":"Go Engineer","releasedDate":"2026-09-30T00:00:00Z","location":{"city":"Berlin","country":"de","remote":true},"department":{"label":"Platform"},"jobAd":{"sections":{"jobDescription":{"text":"<p>Go services</p>"}}}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestNewSources(t *testing.T) {
	srv := atsServer(t)
	base := map[string]string{"recruitee": srv.URL + "/recruitee", "bamboohr": srv.URL + "/bamboohr", "hn": srv.URL + "/hn",
		"remotive": srv.URL + "/none", "remoteok": srv.URL + "/none", "arbeitnow": srv.URL + "/none", "jobicy": srv.URL + "/none", "himalayas": srv.URL + "/none"}
	r := Run(context.Background(), Options{Queries: []string{"go"}, Boards: []string{"https://acme.recruitee.com/", "bamboohr:acme"}, Remote: true, Limit: 10, Client: srv.Client(), BaseURL: base})
	got := map[string]Posting{}
	for _, p := range r.Postings {
		got[p.Source] = p
	}
	rc := got["recruitee"]
	if rc.Title != "Go Developer" || rc.Remote != "remote" || rc.URL != "https://acme.recruitee.com/o/go-developer" || rc.Tags != "Engineering,go" || rc.PostedAt != "2026-10-03" || rc.Description != "Write Go" {
		t.Errorf("recruitee: %+v", rc)
	}
	bh := got["bamboohr"]
	if bh.Title != "Backend Engineer (Go)" || bh.URL != "https://acme.bamboohr.com/careers/42" || bh.Location != "Austin, TX" || bh.Tags != "Platform,Full-Time" || bh.PostedAt != "2026-10-02" {
		t.Errorf("bamboohr: %+v", bh)
	}
	hn := got["hn"]
	if hn.Company != "Tailscale" || hn.Title != "Senior Go Engineer" || hn.Remote != "remote" || hn.URL != "https://news.ycombinator.com/item?id=777" || hn.Location != "Remote (US, EU) · Full-time" || !strings.Contains(hn.Description, "We build Go things") {
		t.Errorf("hn: %+v", hn)
	}
	if len(got) != 3 {
		t.Errorf("want recruitee, bamboohr, hn; got %v (errors %v)", got, r.Errors)
	}
}

func TestFetchPosting(t *testing.T) {
	srv := atsServer(t)
	base := map[string]string{"greenhouse": srv.URL + "/gh", "lever": srv.URL + "/lever", "workable_job": srv.URL + "/workable", "smartrecruiters": srv.URL + "/sr", "recruitee": srv.URL + "/recruitee", "bamboohr": srv.URL + "/bamboohr"}
	cases := map[string]Posting{
		"https://boards.greenhouse.io/acme/jobs/123?gh_src=x":   {Company: "acme", Title: "Staff Go Engineer", Remote: "remote", Location: "Remote - Europe", Source: "greenhouse"},
		"https://jobs.lever.co/acme/abc":                        {Company: "acme", Title: "Go Engineer", Remote: "remote", Location: "Lisbon", Source: "lever"},
		"https://apply.workable.com/acme/j/AB12/":               {Company: "acme", Title: "Go Developer", Remote: "hybrid", Location: "Lisbon, Portugal", Source: "workable"},
		"https://jobs.smartrecruiters.com/Acme/744-go-engineer": {Company: "Acme", Title: "Go Engineer", Remote: "remote", Location: "Berlin, de", Source: "smartrecruiters"},
		"https://acme.recruitee.com/o/go-developer":             {Company: "acme", Title: "Go Developer", Remote: "remote", Location: "Berlin, Germany", Source: "recruitee"},
		"https://acme.bamboohr.com/careers/42":                  {Company: "acme", Title: "Backend Engineer (Go)", Remote: "unknown", Location: "Austin, TX", Source: "bamboohr"},
	}
	for u, want := range cases {
		p, err := FetchPosting(context.Background(), u, srv.Client(), base)
		if err != nil {
			t.Errorf("%s: %v", u, err)
			continue
		}
		if p.Company != want.Company || p.Title != want.Title || p.Remote != want.Remote || p.Location != want.Location || p.Source != want.Source || p.URL == "" || p.Description == "" {
			t.Errorf("%s: %+v", u, p)
		}
	}
	for u, want := range map[string]string{
		"https://jobs.lever.co/acme":           "is a board, not a posting",
		"https://www.linkedin.com/jobs/view/1": "LinkedIn has no public job API",
		"https://example.com/job/1":            "no known ATS",
		"https://acme.recruitee.com/o/missing": "not found on the board",
	} {
		if _, err := FetchPosting(context.Background(), u, srv.Client(), base); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", u, err, want)
		}
	}
	if got := Excerpt("one two three four", 9); got != "one two …" {
		t.Errorf("Excerpt = %q", got)
	}
}

func TestFetchRemotivePosting(t *testing.T) {
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query().Get("search")
		_, _ = w.Write([]byte(`{"jobs":[
			{"company_name":"Other","title":"Senior Independent AI Engineer","url":"https://remotive.com/remote-jobs/software-development/senior-independent-ai-engineer-1919266","description":"<p>AI</p>"},
			{"company_name":"A.Team","title":"Senior Independent Software Developer","url":"https://remotive.com/remote-jobs/software-development/senior-independent-software-developer-1919265","description":"<p>Build Go services.</p>"}]}`))
	}))
	defer srv.Close()
	base := map[string]string{"remotive": srv.URL}
	p, err := FetchPosting(context.Background(), "https://remotive.com/remote-jobs/software-development/senior-independent-software-developer-1919265", srv.Client(), base)
	if err != nil {
		t.Fatal(err)
	}
	if query != "senior independent software developer" || p.Company != "A.Team" || !strings.Contains(p.Description, "Build Go services.") {
		t.Errorf("query %q, posting %+v", query, p)
	}
	if _, err := FetchPosting(context.Background(), "https://remotive.com/remote-jobs/software-development", srv.Client(), base); err == nil {
		t.Error("a category page is not a posting")
	}
}
