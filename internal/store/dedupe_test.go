package store

import "testing"

func TestTitleKeyAndURL(t *testing.T) {
	cases := map[string]string{
		"Sr. Go Engineer (m/f/d)":            "senior-go-engineer",
		"Senior Go Engineer (Remote)":        "senior-go-engineer",
		"Senior Go Engineer - all genders":   "senior-go-engineer",
		"Jr Backend Dev":                     "junior-backend-developer",
		"Backend Engineer, Platform (w/m/d)": "backend-engineer-platform",
		"Staff Engineer – Payments":          "staff-engineer-payments",
		"C# Developer":                       "csharp-developer",
		"Engineering Manager (Hybrid) m/w/d": "engineering-manager",
	}
	for in, want := range cases {
		if got := TitleKey(in); got != want {
			t.Errorf("TitleKey(%q) = %q, want %q", in, got, want)
		}
	}
	urls := map[string]string{
		"https://www.Example.com/jobs/12/?utm_source=linkedin&gh_src=abc": "example.com/jobs/12",
		"http://example.com/jobs/12":                                      "example.com/jobs/12",
		"https://example.com/jobs/12#apply":                               "example.com/jobs/12",
		"https://example.com/jobs?id=12&utm_medium=x":                     "example.com/jobs?id=12",
	}
	for in, want := range urls {
		if got := NormalizeURL(in); got != want {
			t.Errorf("NormalizeURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUpsertDedupe(t *testing.T) {
	s := open(t)
	a, created, _ := s.UpsertJob(Job{Company: "Acme", Title: "Sr. Go Engineer (m/f/d)", URL: "https://acme.com/jobs/1?utm_source=x"})
	if !created {
		t.Fatal("first row")
	}
	if _, created, _ = s.UpsertJob(Job{Company: "Acme", Title: "Senior Go Engineer (Remote)"}); created {
		t.Error("title variant created a duplicate")
	}
	b, created, _ := s.UpsertJob(Job{Company: "Acme Inc", Title: "Golang Engineer", URL: "http://www.acme.com/jobs/1/"})
	if created || b.ID != a.ID {
		t.Errorf("url match should return the first row: created=%v id=%d", created, b.ID)
	}
	if _, created, _ = s.UpsertJob(Job{Company: "Acme", Title: "Go Engineer", URL: "https://acme.com/jobs/2"}); !created {
		t.Error("a different posting must still be created")
	}
}

func TestRekeyJobs(t *testing.T) {
	s := open(t)
	if _, err := s.DB.Exec(`INSERT INTO jobs (key,company,title,status,created_at,updated_at) VALUES ('acme|sr-go-engineer-m-f-d','Acme','Sr Go Engineer (m/f/d)','new','x','x'), ('acme|senior-go-engineer','Acme','Senior Go Engineer','new','x','x'), ('zed|jr-dev','Zed','Jr Dev','new','x','x')`); err != nil {
		t.Fatal(err)
	}
	if err := s.rekeyJobs(); err != nil {
		t.Fatal(err)
	}
	var k1, k3 string
	_ = s.DB.QueryRow(`SELECT key FROM jobs WHERE id=1`).Scan(&k1)
	_ = s.DB.QueryRow(`SELECT key FROM jobs WHERE id=3`).Scan(&k3)
	if k1 != "acme|sr-go-engineer-m-f-d" {
		t.Errorf("collision must keep the old key, got %q", k1)
	}
	if k3 != "zed|junior-developer" {
		t.Errorf("rekey: %q", k3)
	}
}

func TestCompanyName(t *testing.T) {
	for in, want := range map[string]string{
		"Acme GmbH": "Acme", "Acme GmbH & Co. KG": "Acme", "Acme Corp, Inc.": "Acme", "Zed AG": "Zed",
		"Nordic ApS": "Nordic", "Acme": "Acme", "GmbH": "GmbH",
	} {
		if got := CompanyName(in); got != want {
			t.Errorf("CompanyName(%q) = %q, want %q", in, got, want)
		}
	}
}
