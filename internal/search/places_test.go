package search

import (
	"reflect"
	"testing"
)

func TestPlaces(t *testing.T) {
	for text, want := range map[string][]string{
		"Berlin, Germany":          {"berlin", "europe", "germany"},
		"Remote - US":              {"north america", "united states"},
		"Anywhere in the world":    {"worldwide"},
		"Tell us about you":        {},
		"Remote (EMEA)":            {"emea"},
		"Amsterdam or London (UK)": {"amsterdam", "europe", "london", "netherlands", "united kingdom"},
	} {
		if got := Places(text); !reflect.DeepEqual(got, want) {
			t.Errorf("Places(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestPostingPlacesWorldwide(t *testing.T) {
	brag := Posting{Remote: "remote", Description: "We serve customers worldwide. Must live in the US."}
	if got := PostingPlaces(brag); !reflect.DeepEqual(got, []string{"north america", "united states"}) {
		t.Errorf("customers worldwide: %v", got)
	}
	open := Posting{Remote: "remote", Description: "Work from anywhere in the world."}
	if got := PostingPlaces(open); !reflect.DeepEqual(got, []string{Worldwide}) {
		t.Errorf("anywhere: %v", got)
	}
}

func TestLocationMatch(t *testing.T) {
	remote := func(loc string) Posting { return Posting{Location: loc, Remote: "remote"} }
	onsite := func(loc string) Posting { return Posting{Location: loc, Remote: "onsite"} }
	for _, c := range []struct {
		name string
		p    Posting
		want []string
		ok   bool
	}{
		{"remote europe for berlin", remote("Europe"), []string{"Berlin"}, true},
		{"remote emea for germany", remote("EMEA"), []string{"Germany"}, true},
		{"remote worldwide", remote("Worldwide"), []string{"Berlin"}, true},
		{"remote US for berlin", remote("USA only"), []string{"Berlin"}, false},
		{"remote no place", remote(""), []string{"Berlin"}, true},
		{"onsite same city", onsite("Berlin, Germany"), []string{"Berlin"}, true},
		{"onsite other city", onsite("Munich, Germany"), []string{"Berlin"}, false},
		{"onsite country wanted", onsite("Munich, Germany"), []string{"Germany"}, true},
		{"onsite region wanted", onsite("Paris, France"), []string{"Europe"}, true},
		{"onsite abroad", onsite("Austin, TX, United States"), []string{"Europe"}, false},
		{"onsite smaller german city", onsite("Karlsruhe"), []string{"Germany"}, true},
		{"onsite jerusalem for germany", Posting{Location: "Jerusalem, Jerusalem District", Remote: "hybrid"}, []string{"Germany"}, false},
		{"no want", onsite("Austin"), nil, true},
	} {
		if ok, note := LocationMatch(c.p, c.want); ok != c.ok {
			t.Errorf("%s: ok = %v (%s), want %v", c.name, ok, note, c.ok)
		}
	}
	if ok, note := LocationMatch(onsite("г Москва"), []string{"Germany"}); !ok || note != "location not recognised: г Москва" {
		t.Errorf("unknown place: %v %q", ok, note)
	}
	if ok, note := LocationMatch(remote("Remote"), []string{"Germany"}); !ok || note != "location not stated" {
		t.Errorf("no place: %v %q", ok, note)
	}
}
