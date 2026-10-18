package search

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestArbeitsagentur(t *testing.T) {
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != jobsucheKey {
			t.Errorf("api key: %q", r.Header.Get("X-API-Key"))
		}
		if strings.HasPrefix(r.URL.Path, "/detail/") {
			if r.URL.Path != "/detail/MTAwMDEtMQ==" { // base64 of 10001-1
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Write([]byte(`{"stellenangebotsTitel":"Backend Engineer Go (m/w/d)","firma":"Acme GmbH","referenznummer":"10001-1",
				"stellenangebotsBeschreibung":"Wir suchen Go und Postgres.","stellenlokationen":[{"adresse":{"ort":"Berlin"}}]}`))
			return
		}
		queries = append(queries, r.URL.RawQuery)
		w.Write([]byte(`{"ergebnisliste":[
			{"stellenangebotsTitel":"Backend Engineer Go (m/w/d)","firma":"Acme GmbH","referenznummer":"10001-1","datumErsteVeroeffentlichung":"2026-10-09",
			 "hauptberuf":"Softwareentwickler/in","arbeitszeitVollzeit":true,"homeofficemoeglich":true,"gehaltsspanneVon":65000,"gehaltsspanneBis":75000,
			 "stellenlokationen":[{"adresse":{"ort":"Berlin","land":"DEUTSCHLAND"}}]},
			{"stellenangebotsTitel":"Go Developer","firma":"Zed AG","referenznummer":"10001-2","externeURL":"https://zed.example/jobs/2",
			 "arbeitszeitVollzeit":false,"vertragsdauer":"BEFRISTET","stellenlokationen":[]}]}`))
	}))
	defer srv.Close()
	f := fetcher{client: srv.Client(), base: map[string]string{"arbeitsagentur": srv.URL, "arbeitsagentur-detail": srv.URL + "/detail"}}

	ps, err := f.arbeitsagentur(context.Background(), "go", "berlin")
	if err != nil {
		t.Fatal(err)
	}
	if want := "angebotsart=1&size=50&umkreis=25&was=go&wo=berlin"; queries[0] != want {
		t.Errorf("query %q, want %q", queries[0], want)
	}
	if len(ps) != 2 {
		t.Fatalf("got %d postings", len(ps))
	}
	a, b := ps[0], ps[1]
	if a.URL != "https://www.arbeitsagentur.de/jobsuche/jobdetail/10001-1" || a.Location != "Berlin, Germany" || a.Remote != "hybrid" ||
		a.SalaryMin != 65000 || a.Currency != "EUR" || a.PostedAt != "2026-10-09" {
		t.Errorf("first posting: %+v", a)
	}
	if b.URL != "https://www.arbeitsagentur.de/jobsuche/jobdetail/10001-2" || b.Location != "Germany" || b.Remote != "unknown" || b.Tags != "part-time,fixed-term" {
		t.Errorf("second posting: %+v", b)
	}

	if _, err := f.arbeitsagentur(context.Background(), "", AllGermany); err != nil {
		t.Fatal(err)
	}
	if want := "angebotsart=1&size=50"; queries[1] != want {
		t.Errorf("whole country query %q, want %q", queries[1], want)
	}

	p, err := FetchPosting(context.Background(), "https://www.arbeitsagentur.de/jobsuche/jobdetail/10001-1", srv.Client(), f.base)
	if err != nil {
		t.Fatal(err)
	}
	if p.Description != "Wir suchen Go und Postgres." || p.Company != "Acme GmbH" || !strings.Contains(strings.Join(p.Skills, ","), "go") {
		t.Errorf("detail posting: %+v", p)
	}
	if _, err := FetchPosting(context.Background(), "https://www.arbeitsagentur.de/jobsuche/jobdetail/10001-9", srv.Client(), f.base); !errors.Is(err, ErrGone) {
		t.Errorf("closed posting: %v", err)
	}
}
