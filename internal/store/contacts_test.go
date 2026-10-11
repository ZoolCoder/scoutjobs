package store

import "testing"

func TestCompanyKey(t *testing.T) {
	for in, want := range map[string]string{"Stripe, Inc.": "stripe", "stripe": "stripe", "Acme GmbH": "acme", "The Company": "the-company", "Delivery Hero SE": "delivery-hero", "": ""} {
		if got := CompanyKey(in); got != want {
			t.Errorf("CompanyKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestContacts(t *testing.T) {
	s := open(t)
	list := []Contact{{Name: "Ada", Company: "Stripe, Inc.", ConnectedOn: "2021-01-01"}, {Name: "Bo", Company: "stripe", ConnectedOn: "2023-05-01"}, {Name: "Cy", Company: "Acme"}}
	if err := s.ReplaceContacts("linkedin", list); err != nil {
		t.Fatal(err)
	}
	got, err := s.ContactsAt("Stripe")
	if err != nil || len(got) != 2 || got[0].Name != "Bo" {
		t.Fatalf("ContactsAt: %v %+v", err, got)
	}
	if err := s.ReplaceContacts("linkedin", list[2:]); err != nil {
		t.Fatal(err)
	}
	if n := s.ContactCount(); n != 1 {
		t.Errorf("re-import kept %d contacts, want 1", n)
	}
	if got, _ := s.ContactsAt(""); got != nil {
		t.Errorf("empty company matched %+v", got)
	}
}
