package match

import (
	"reflect"
	"strings"
	"testing"

	"github.com/zoolcoder/scoutjobs/internal/search"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

const profile = `# Profile

## Identity
- Name: Dana
- Location / time zone: Berlin, CET
- Work authorization: EU citizen

## Target
- Roles: Backend / Go engineer
- Seniority: Senior
- Remote / hybrid / onsite: Remote
- Locations or countries:
- Salary floor (currency): 80000 EUR
- Deal-breakers: freelance, on-call every week

## Skills
- Strong: Go, Postgres, Kafka, distributed systems
- Working: Kubernetes, Terraform
- Learning: Rust
`

func testProfile() Profile {
	return Parse(profile, []store.Preference{
		{Key: "stack_avoid", Value: "java, spring", Confidence: 0.8, Source: "inferred"},
		{Key: "company_size", Value: "50-500", Source: "stated"},
	})
}

func TestParse(t *testing.T) {
	p := testProfile()
	if !reflect.DeepEqual(p.Roles, []string{"backend engineer", "go engineer"}) {
		t.Errorf("Roles = %v", p.Roles)
	}
	if p.Level != "senior" || p.Remote != "remote" || !p.AvoidOnCall {
		t.Errorf("Level %q Remote %q AvoidOnCall %v", p.Level, p.Remote, p.AvoidOnCall)
	}
	if !reflect.DeepEqual(p.Places, []string{"Berlin"}) || !reflect.DeepEqual(p.Eligible, []string{"europe"}) {
		t.Errorf("Places %v Eligible %v", p.Places, p.Eligible)
	}
	if !reflect.DeepEqual(p.Avoid, []string{"java", "spring"}) || !reflect.DeepEqual(p.AvoidTypes, []string{"freelance"}) {
		t.Errorf("Avoid %v AvoidTypes %v", p.Avoid, p.AvoidTypes)
	}
	if p.SalaryFloor != 80000 || p.SalaryCurrency != "EUR" {
		t.Errorf("salary %d %s", p.SalaryFloor, p.SalaryCurrency)
	}
}

func posting(title, loc, remote, desc string) search.Posting {
	p := search.Posting{Title: title, Location: loc, Remote: remote, Description: desc}
	search.Enrich(&p)
	return p
}

func TestScore(t *testing.T) {
	pr := testProfile()
	good := pr.Score(posting("Senior Backend Engineer (Go)", "Europe", "remote", "Go, Postgres and Kafka. Full-time."))
	java := pr.Score(posting("Senior Backend Engineer", "Europe", "remote", "Java and Spring Boot microservices."))
	abroad := pr.Score(posting("Senior Go Engineer", "USA only", "remote", "Go and Postgres."))
	freelance := pr.Score(posting("Freelance Go Developer", "Europe", "remote", "Go."))
	if good.Score < 80 || len(good.Hard) > 0 {
		t.Errorf("good = %+v", good)
	}
	if java.Score >= good.Score-30 {
		t.Errorf("java %d not well below good %d: %+v", java.Score, good.Score, java)
	}
	if len(abroad.Hard) == 0 || !strings.Contains(abroad.Hard[0], "united states") {
		t.Errorf("abroad = %+v", abroad)
	}
	near := pr.Score(posting("Platform Engineer", "Europe", "remote", "Go."))
	if near.Score >= good.Score-25 || !strings.Contains(strings.Join(near.Why, ";"), "close to go engineer") {
		t.Errorf("a role matched only by skills scores like a target role: %+v", near)
	}
	qa := pr.Score(posting("QA Engineer", "Europe", "remote", "Go."))
	if strings.Contains(strings.Join(qa.Why, ";"), "go engineer") {
		t.Errorf("a QA role counts as close to go engineer: %+v", qa)
	}
	if len(freelance.Hard) == 0 {
		t.Errorf("freelance = %+v", freelance)
	}
}

func TestSuggest(t *testing.T) {
	got := testProfile().Suggest()
	want := []Suggestion{
		{Name: "Your target roles", Queries: "backend engineer, go engineer", Location: "Berlin", RemoteOnly: true},
		{Name: "Senior roles", Queries: "senior backend engineer, senior go engineer", Location: "Berlin", RemoteOnly: true},
		{Name: "Your strongest languages", Queries: "go developer", Location: "Berlin", RemoteOnly: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Suggest =\n%+v\nwant\n%+v", got, want)
	}
}

func TestSourceAnswersMoveTheScore(t *testing.T) {
	f := store.Funnel{Source: []store.FunnelRow{
		{Group: "greenhouse", Applied: 6, Responded: 4},
		{Group: "remotive", Applied: 10, Responded: 0},
		{Group: "lever", Applied: 2, Responded: 2},
		{Group: "ashby", Applied: 6, Responded: 1},
	}}
	rates := SourceRates(f) // overall 7 of 24, 29%
	if _, ok := rates["lever"]; ok {
		t.Error("two applications are too few to judge a source")
	}
	if _, ok := rates["ashby"]; ok {
		t.Error("ashby at 16% is within the gap of 29% overall")
	}
	pr := testProfile()
	p := posting("Senior Backend Engineer (Go)", "Europe", "remote", "Go and Postgres. Full-time.")
	base := pr.Score(p).Score
	pr.Answers = rates
	p.Source = "Greenhouse"
	if up := pr.Score(p); up.Score != min(100, base+sourceWeight) || !strings.Contains(strings.Join(up.Why, ";"), "Greenhouse answered 66% of your 6") {
		t.Errorf("greenhouse = %+v, base %d", up, base)
	}
	p.Source = "remotive"
	if down := pr.Score(p); down.Score != base-sourceWeight || len(down.Miss) == 0 {
		t.Errorf("remotive = %+v, base %d", down, base)
	}
}

func TestLanguagesMoveTheScore(t *testing.T) {
	pr := Parse(profile, []store.Preference{{Key: "languages", Value: "English (C2), Arabic", Source: "stated"}})
	if strings.Join(pr.Speaks, ",") != "english,arabic" {
		t.Fatalf("speaks %v", pr.Speaks)
	}
	en := pr.Score(posting("Senior Backend Engineer (Go)", "Europe", "remote", "Go and Postgres. Fluent English."))
	de := pr.Score(posting("Senior Backend Engineer (Go)", "Europe", "remote", "Go and Postgres. Fluent German and English."))
	if de.Score > en.Score-10 || !strings.Contains(strings.Join(de.Miss, ";"), "asks for German, which you do not list") {
		t.Errorf("german required: %d vs %d, %+v", de.Score, en.Score, de)
	}
	pr.Speaks = append(pr.Speaks, "german")
	if de := pr.Score(posting("Senior Backend Engineer (Go)", "Europe", "remote", "Go and Postgres. Fluent German.")); !strings.Contains(strings.Join(de.Why, ";"), "which you speak") {
		t.Errorf("german spoken: %+v", de)
	}
}
