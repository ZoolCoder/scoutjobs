package search

import (
	"reflect"
	"testing"
)

func TestSpeaks(t *testing.T) {
	for _, c := range []struct {
		title, desc string
		want        []string
	}{
		{"Backend Engineer", "You speak fluent German and English.", []string{"english", "german"}},
		{"Backend Engineer", "German (C1) is required; French is a plus.", []string{"german"}},
		{"Backend Engineer", "Fluent English. German is a plus.", []string{"english"}},
		{"Backend Engineer", "German language skills are nice to have.", nil},
		{"Backend Entwickler (m/w/d)", "Fließende Deutschkenntnisse in Wort und Schrift.", []string{"german"}},
		{"Backend Entwickler (m/w/d)", "Englischkenntnisse sind von Vorteil.", nil},
		{"Backend Entwickler (m/w/d)", "Wir suchen für unser Team in Berlin eine Person, die mit uns die Plattform baut und die Kunden der Region mit " +
			"Software versorgt. Bei uns arbeiten Sie mit modernen Werkzeugen für unsere Produkte und die Zukunft der Firma.", []string{"german"}},
		{"Backend Engineer", "We build the platform for our customers in Germany and you will own the services with the team.", nil},
	} {
		if got := Speaks(c.title, c.desc); !reflect.DeepEqual(got, c.want) && (len(got) > 0 || len(c.want) > 0) {
			t.Errorf("Speaks(%q) = %v, want %v", c.desc, got, c.want)
		}
	}
}
