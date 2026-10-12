package search

import (
	"sort"
	"strings"
)

// Region names. A posting open to everyone is Worldwide.
const (
	Europe       = "europe"
	NorthAmerica = "north america"
	LatAm        = "latam"
	Americas     = "americas"
	EMEA         = "emea"
	MEA          = "middle east & africa"
	APAC         = "apac"
	Worldwide    = "worldwide"
)

// Regions maps a region alias or a country to its region; a region maps to itself.
var Regions = map[string]string{
	"europe": Europe, "european union": Europe, "cet": Europe, "cest": Europe, "eea": Europe,
	"uk": Europe, "united kingdom": Europe, "england": Europe, "scotland": Europe, "ireland": Europe, "germany": Europe,
	"deutschland": Europe, "france": Europe, "spain": Europe, "portugal": Europe, "italy": Europe, "netherlands": Europe,
	"belgium": Europe, "luxembourg": Europe, "switzerland": Europe, "austria": Europe, "poland": Europe, "czechia": Europe,
	"czech republic": Europe, "slovakia": Europe, "hungary": Europe, "romania": Europe, "bulgaria": Europe, "greece": Europe,
	"croatia": Europe, "slovenia": Europe, "serbia": Europe, "denmark": Europe, "sweden": Europe, "norway": Europe,
	"finland": Europe, "iceland": Europe, "estonia": Europe, "latvia": Europe, "lithuania": Europe, "ukraine": Europe,
	"cyprus": Europe, "malta": Europe,
	"north america": NorthAmerica, "usa": NorthAmerica, "united states": NorthAmerica, "canada": NorthAmerica,
	"latam": LatAm, "latin america": LatAm, "south america": LatAm, "mexico": LatAm, "brazil": LatAm, "argentina": LatAm,
	"colombia": LatAm, "chile": LatAm, "peru": LatAm, "uruguay": LatAm, "costa rica": LatAm,
	"americas": Americas, "emea": EMEA,
	"middle east": MEA, "africa": MEA, "israel": MEA, "uae": MEA, "united arab emirates": MEA, "saudi arabia": MEA,
	"egypt": MEA, "nigeria": MEA, "kenya": MEA, "south africa": MEA, "turkey": MEA, "morocco": MEA, "jordan": MEA,
	"apac": APAC, "asia": APAC, "india": APAC, "singapore": APAC, "japan": APAC, "australia": APAC, "new zealand": APAC,
	"philippines": APAC, "vietnam": APAC, "indonesia": APAC, "china": APAC, "south korea": APAC, "malaysia": APAC,
	"thailand": APAC, "pakistan": APAC, "bangladesh": APAC,
}

// cities maps the cities postings name most to their country.
var cities = map[string]string{
	"berlin": "germany", "munich": "germany", "hamburg": "germany", "frankfurt": "germany", "cologne": "germany",
	"stuttgart": "germany", "düsseldorf": "germany", "london": "united kingdom", "manchester": "united kingdom",
	"edinburgh": "united kingdom", "dublin": "ireland", "paris": "france", "lyon": "france", "amsterdam": "netherlands",
	"rotterdam": "netherlands", "madrid": "spain", "barcelona": "spain", "lisbon": "portugal", "porto": "portugal",
	"milan": "italy", "rome": "italy", "vienna": "austria", "zurich": "switzerland", "geneva": "switzerland",
	"warsaw": "poland", "krakow": "poland", "prague": "czechia", "budapest": "hungary", "bucharest": "romania",
	"stockholm": "sweden", "copenhagen": "denmark", "oslo": "norway", "helsinki": "finland", "brussels": "belgium",
	"tallinn": "estonia", "athens": "greece", "kyiv": "ukraine",
	"new york": "united states", "san francisco": "united states", "seattle": "united states", "austin": "united states",
	"boston": "united states", "chicago": "united states", "los angeles": "united states", "denver": "united states",
	"toronto": "canada", "vancouver": "canada", "montreal": "canada",
	"são paulo": "brazil", "sao paulo": "brazil", "buenos aires": "argentina", "mexico city": "mexico", "bogota": "colombia",
	"bangalore": "india", "bengaluru": "india", "hyderabad": "india", "pune": "india", "tokyo": "japan",
	"sydney": "australia", "melbourne": "australia", "tel aviv": "israel", "dubai": "uae",
}

// aliases are other names of a place in Regions.
var aliases = map[string]string{"usa": "united states", "uk": "united kingdom", "deutschland": "germany", "czech republic": "czechia",
	"united arab emirates": "uae", "european union": Europe, "cet": Europe, "cest": Europe, "eea": Europe,
	"latin america": LatAm, "south america": LatAm, "england": "united kingdom", "scotland": "united kingdom"}

func canon(name string) string {
	if a, ok := aliases[name]; ok {
		return a
	}
	return name
}

// worldwideWords mark a posting open from anywhere.
var worldwideWords = []string{"worldwide", "anywhere", "global", "globally", "any location", "all countries"}

// Places finds the countries and regions a text names, each country with its region, sorted. A
// city adds itself, its country and its region.
func Places(text string) []string {
	low := strings.ToLower(text)
	set := map[string]bool{}
	for name, region := range Regions {
		if hasWord(low, name) {
			set[canon(name)], set[region] = true, true
		}
	}
	for city, country := range cities {
		if hasWord(low, city) {
			set[city], set[country], set[Regions[country]] = true, true, true
		}
	}
	// short upper-case codes would match English words in lower case ("us", "eu")
	for code, place := range map[string]string{"US": "united states", "USA": "united states", "U.S.": "united states", "EU": "europe", "UK": "united kingdom"} {
		if hasCode(text, code) {
			set[place], set[Regions[place]] = true, true
		}
	}
	for _, w := range worldwideWords {
		if hasWord(low, w) {
			set[Worldwide] = true
		}
	}
	return sortedKeys(set)
}

func hasCode(text, code string) bool {
	for i := 0; ; {
		k := strings.Index(text[i:], code)
		if k < 0 {
			return false
		}
		k += i
		end := k + len(code)
		if (k == 0 || !alnum(text[k-1])) && (end == len(text) || !alnum(text[end])) {
			return true
		}
		i = k + 1
	}
}

// PostingPlaces is where a posting is or whom it is open to: its location, else, for a remote
// posting without one, the places the start of its description names ("must be located in Europe").
func PostingPlaces(p Posting) []string {
	if loc := strings.TrimSpace(p.Location); loc != "" && !strings.EqualFold(loc, "remote") {
		if places := Places(loc); len(places) > 0 {
			return places
		}
	}
	if !IsRemote(p) {
		return nil
	}
	head := p.Description
	if len(head) > 600 {
		head = head[:600]
	}
	places := Places(head)
	// "customers worldwide" or "a global team" says nothing about who may apply
	if !anywhere(strings.ToLower(head)) {
		places = remove(places, Worldwide)
	}
	return places
}

// anywhere is true when a description opens the job to every country.
func anywhere(low string) bool {
	for _, w := range []string{"work from anywhere", "anywhere in the world", "remote worldwide", "worldwide remote",
		"fully remote worldwide", "from any country", "location: worldwide", "location: anywhere", "open to candidates worldwide"} {
		if strings.Contains(low, w) {
			return true
		}
	}
	return false
}

func remove(list []string, s string) []string {
	out := list[:0]
	for _, x := range list {
		if x != s {
			out = append(out, x)
		}
	}
	return out
}

// IsRemote is true for a fully remote posting: the board says so, or, when it does not know, the
// location reads "Remote".
func IsRemote(p Posting) bool {
	mode := strings.ToLower(p.Remote)
	if strings.Contains(mode, "remote") {
		return true
	}
	return (mode == "" || mode == "unknown") && strings.Contains(strings.ToLower(p.Location), "remote")
}

// covers is true when a posting open to place is open to someone in want: the same place, a
// country inside a region, or a region inside a wider one (Americas, EMEA).
func covers(place, want string) bool {
	if place == want || place == Worldwide {
		return true
	}
	switch place {
	case Americas:
		return want == Americas || want == NorthAmerica || want == LatAm || Regions[want] == NorthAmerica || Regions[want] == LatAm
	case EMEA:
		return want == EMEA || want == Europe || want == MEA || Regions[want] == Europe || Regions[want] == MEA
	}
	return false
}

// Expand turns what the user typed ("Berlin", "Germany", "Europe") into the places it stands for,
// most specific first: the city or country itself, then its country and region.
func Expand(want string) []string {
	w := canon(strings.ToLower(strings.TrimSpace(want)))
	if w == "us" {
		w = "united states"
	}
	if w == "" {
		return nil
	}
	out := []string{w}
	if c, ok := cities[w]; ok {
		out = append(out, c, Regions[c])
	} else if r, ok := Regions[w]; ok && r != w {
		out = append(out, r)
	}
	return out
}

// LocationMatch says whether a posting fits the places the user wants. A remote posting fits when
// it is open to their region; an on-site or hybrid one when it is in their city, or in the country
// or region they named. ok is true with a note when the posting states no location.
func LocationMatch(p Posting, wants []string) (ok bool, note string) {
	if len(wants) == 0 {
		return true, ""
	}
	places := p.Regions
	if places == nil {
		places = PostingPlaces(p)
	}
	if len(places) == 0 {
		return true, "location not stated"
	}
	remote := IsRemote(p)
	low := strings.ToLower(p.Location)
	for _, want := range wants {
		exp := Expand(want)
		if len(exp) == 0 {
			continue
		}
		if hasWord(low, exp[0]) {
			return true, ""
		}
		for _, place := range places {
			if remote {
				for _, w := range exp { // remote: anywhere in the user's country or region
					if covers(place, w) {
						return true, ""
					}
				}
			} else if covers(place, exp[0]) && cities[exp[0]] == "" { // on-site: the country or region named, never a different city
				return true, ""
			}
		}
	}
	return false, "in " + strings.Join(display(places), ", ")
}

// display keeps the most specific places for a message: countries over the regions they imply.
func display(places []string) []string {
	var out []string
	for _, p := range places {
		if _, city := cities[p]; city {
			continue
		}
		if r, ok := Regions[p]; ok && r == p && hasCountryIn(places, p) {
			continue
		}
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func hasCountryIn(places []string, region string) bool {
	for _, p := range places {
		if p != region && Regions[p] == region {
			return true
		}
	}
	return false
}
