package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/zoolcoder/scoutjobs/internal/store"
)

const testProfile = `- Location / time zone: Berlin, CET
- Roles: Backend / Go engineer
- Seniority: Senior
- Remote / hybrid / onsite: Remote
- Strong: Go, Postgres, Kafka
- Deal-breakers: freelance
`

func TestSearchRanksByProfileAndFiltersByPlace(t *testing.T) {
	s := testStore(t)
	if err := os.WriteFile(filepath.Join(s.Home, "profile.md"), []byte(testProfile), 0o600); err != nil {
		t.Fatal(err)
	}
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"jobs":[
			{"id":1,"title":"Java Engineer","absolute_url":"https://gh/1","content":"Java and Spring Boot","location":{"name":"Remote, Europe"}},
			{"id":2,"title":"Senior Go Engineer","absolute_url":"https://gh/2","content":"Go, Postgres and Kafka","location":{"name":"Remote, Europe"}},
			{"id":3,"title":"Go Engineer","absolute_url":"https://gh/3","content":"Go","location":{"name":"Austin, TX, United States"}},
			{"id":4,"title":"Freelance Go Engineer","absolute_url":"https://gh/4","content":"Go","location":{"name":"Remote, Europe"}}]}`))
	}))
	defer gh.Close()
	h, _ := Handler(s, Config{SearchBaseURL: map[string]string{"greenhouse": gh.URL}})

	if b := get(h, "/search").Body.String(); !strings.Contains(b, "Suggested from your profile") || !strings.Contains(b, `name="location" value="Berlin"`) {
		t.Errorf("search page lacks the suggestions or the profile location: %s", b)
	}

	b := post(h, "/search", url.Values{"queries": {"engineer"}, "boards": {"greenhouse:acme"}, "location": {"Berlin"}, "hide_mismatch": {"1"}}).Body.String()
	if strings.Contains(b, "Austin") || !strings.Contains(b, "1 hidden by filters") {
		t.Errorf("location filter kept the US job: %s", b)
	}
	if strings.Contains(b, "Freelance Go Engineer") || !strings.Contains(b, "1 hidden as mismatches") {
		t.Errorf("hide mismatches kept the freelance job: %s", b)
	}
	if i, k := strings.Index(b, "Senior Go Engineer"), strings.Index(b, "Java Engineer"); i < 0 || k < 0 || i > k {
		t.Errorf("not ranked by profile: go at %d, java at %d", i, k)
	}
	for _, want := range []string{`class="fit `, "✓ target role", `data-tagbar`, `|level:senior|`} {
		if !strings.Contains(b, want) {
			t.Errorf("results lack %q", want)
		}
	}

	// the Add button tracks the derived tags too; the best fit is the first row
	if m := regexp.MustCompile(`name="tags" value="([^"]*)"`).FindStringSubmatch(b); m == nil || m[1] != "go,level:senior,region:europe" {
		t.Errorf("add form tags = %v", m)
	}

	// saved searches keep the location and the mismatch switch
	post(h, "/search", url.Values{"queries": {"engineer"}, "boards": {"greenhouse:acme"}, "location": {"Berlin"}, "hide_mismatch": {"1"}, "name": {"Berlin"}, "save": {"1"}})
	saved, _ := s.SavedSearches()
	if len(saved) != 1 || !strings.Contains(saved[0].Filters, "location=Berlin") || !strings.Contains(saved[0].Filters, "hide_mismatch=1") {
		t.Errorf("saved %+v", saved)
	}
}

func TestPipelineLocationAndTagFilters(t *testing.T) {
	s := testStore(t)
	for _, j := range []store.Job{
		{Company: "A", Title: "Go Engineer", Location: "Berlin, Germany", Remote: "onsite", Tags: "go,level:senior"},
		{Company: "B", Title: "Go Engineer", Location: "Europe", Remote: "remote", Tags: "go"},
		{Company: "C", Title: "Go Engineer", Location: "Austin, TX", Remote: "onsite", Tags: "go,rust"},
		{Company: "D", Title: "Go Engineer", Remote: "onsite", Tags: "rust"},
	} {
		if _, _, err := s.UpsertJob(j); err != nil {
			t.Fatal(err)
		}
	}
	h, _ := Handler(s, Config{})
	has := func(b, company string) bool { return strings.Contains(b, `data-company="`+company+`"`) }

	b := get(h, "/pipeline?loc=Berlin").Body.String()
	if !has(b, "A") || !has(b, "B") || has(b, "C") || has(b, "D") {
		t.Errorf("loc=Berlin: A %v B %v C %v D %v", has(b, "A"), has(b, "B"), has(b, "C"), has(b, "D"))
	}
	if !strings.Contains(b, "Berlin, Germany") {
		t.Error("cards do not show the location")
	}
	b = get(h, "/pipeline?tag=rust").Body.String()
	if has(b, "A") || has(b, "B") || !has(b, "C") || !has(b, "D") {
		t.Errorf("tag=rust: A %v B %v C %v D %v", has(b, "A"), has(b, "B"), has(b, "C"), has(b, "D"))
	}
	if !strings.Contains(b, `<option value="rust" selected>`) || !strings.Contains(b, `<option value="go">`) {
		t.Error("tag picker missing")
	}
	if b := get(h, "/pipeline?view=list&loc=Germany").Body.String(); !strings.Contains(b, `<td class="where">Berlin, Germany</td>`) || strings.Contains(b, "Austin") {
		t.Errorf("list view location: %s", b)
	}
}
