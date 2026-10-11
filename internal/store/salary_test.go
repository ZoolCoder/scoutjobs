package store

import "testing"

func TestParseSalary(t *testing.T) {
	cases := []struct {
		in       string
		min, max int64
		cur      string
	}{
		{"$80k - $120k", 80000, 120000, "USD"},
		{"60.000 – 80.000 EUR per year", 60000, 80000, "EUR"},
		{"£45,000", 45000, 45000, "GBP"},
		{"competitive", 0, 0, ""},
		{"USD 95000", 95000, 95000, "USD"},
	}
	for _, c := range cases {
		mn, mx, cur := ParseSalary(c.in)
		if mn != c.min || mx != c.max || cur != c.cur {
			t.Errorf("ParseSalary(%q) = %d %d %q", c.in, mn, mx, cur)
		}
	}
}

func TestNormalizeSalary(t *testing.T) {
	j := Job{SalaryMin: 100000, SalaryMax: 140000, Currency: "USD", Notes: "n"}
	NormalizeSalary(&j, "EUR")
	if j.Currency != "EUR" || j.SalaryMin != 92600 || j.SalaryMax != 129600 || j.Notes != "n\nsalary quoted as 100000-140000 USD yearly" {
		t.Errorf("usd: %+v", j)
	}
	h := Job{SalaryMin: 60, SalaryMax: 80, Currency: "EUR"}
	NormalizeSalary(&h, "EUR")
	if h.SalaryMin != 124800 || h.SalaryMax != 166400 || h.Notes != "salary quoted as 60-80 EUR hourly" {
		t.Errorf("hourly: %+v", h)
	}
	m := Job{SalaryMax: 5000, Currency: "eur"}
	NormalizeSalary(&m, "EUR")
	if m.SalaryMax != 60000 || m.Notes != "salary quoted as 5000 EUR monthly" {
		t.Errorf("monthly: %+v", m)
	}
	same := Job{SalaryMin: 70000, SalaryMax: 90000, Currency: "EUR"}
	NormalizeSalary(&same, "EUR")
	if same.Notes != "" || same.SalaryMin != 70000 {
		t.Errorf("same currency yearly must not change: %+v", same)
	}
	unknown := Job{SalaryMax: 50000, Currency: "XYZ"}
	NormalizeSalary(&unknown, "EUR")
	if unknown.Currency != "XYZ" {
		t.Error("unknown currency left alone")
	}
}

func TestUpsertNormalizesSalary(t *testing.T) {
	s := open(t)
	s.Currency = "EUR"
	j, _, err := s.UpsertJob(Job{Company: "Acme", Title: "Dev", SalaryMin: 100000, SalaryMax: 120000, Currency: "USD"})
	if err != nil || j.Currency != "EUR" || j.SalaryMax != 111100 {
		t.Errorf("upsert: %v %+v", err, j)
	}
}
