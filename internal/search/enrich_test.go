package search

import (
	"reflect"
	"testing"
)

func TestSkills(t *testing.T) {
	got := Skills("We use Go, PostgreSQL and k8s. Go-to-market team. Ready to Go to production.")
	want := []string{"go", "kubernetes", "postgres"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Skills = %v, want %v", got, want)
	}
	if got := Skills("Let's go! Python only."); !reflect.DeepEqual(got, []string{"python"}) {
		t.Errorf("lower-case go counted: %v", got)
	}
}

func TestLevelEmploymentVisa(t *testing.T) {
	for _, c := range []struct{ title, desc, want string }{
		{"Senior Backend Engineer", "", "senior"},
		{"Staff Engineer", "", "staff"},
		{"Backend Engineer", "You have 6+ years of professional experience.", "senior"},
		{"Backend Engineer", "3 years experience with Go", "mid"},
		{"Backend Engineer", "", ""},
		{"Junior Developer", "", "junior"},
	} {
		if got := Level(c.title, c.desc); got != c.want {
			t.Errorf("Level(%q, %q) = %q, want %q", c.title, c.desc, got, c.want)
		}
	}
	for _, c := range []struct{ hint, title, desc, want string }{
		{"full_time", "", "", "full-time"},
		{"", "Freelance Go Developer", "", "freelance"},
		{"", "Go Developer", "This is a full-time, permanent role.", "full-time"},
		{"", "Go Developer", "A 6 month contract role.", "contract"},
		{"", "Go Developer", "", ""},
	} {
		if got := Employment(c.hint, c.title, c.desc); got != c.want {
			t.Errorf("Employment(%q, %q) = %q, want %q", c.hint, c.title, got, c.want)
		}
	}
	if Visa("We offer visa sponsorship.") != "yes" || Visa("We are unable to sponsor visas.") != "no" || Visa("Great team.") != "" {
		t.Error("Visa misread")
	}
}

func TestEnrichTags(t *testing.T) {
	p := Posting{Title: "Senior Go Engineer", Location: "Berlin, Germany", Remote: "hybrid", Tags: "Backend,golang",
		Description: "Kafka and Postgres. Full-time. Visa sponsorship available."}
	Enrich(&p)
	want := "Backend,go,kafka,postgres,level:senior,type:full-time,region:europe,region:germany,visa:yes"
	if got := p.AllTags(); got != want {
		t.Errorf("AllTags = %q\nwant      %q", got, want)
	}
}
