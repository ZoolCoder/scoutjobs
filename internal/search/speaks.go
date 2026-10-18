package search

import (
	"regexp"
	"strings"
)

// spoken maps the English and German names of the languages postings ask for to one name.
var spoken = map[string]string{
	"german": "german", "deutsch": "german", "deutschkenntnisse": "german",
	"english": "english", "englisch": "english", "englischkenntnisse": "english",
	"french": "french", "französisch": "french", "dutch": "dutch", "niederländisch": "dutch",
	"spanish": "spanish", "spanisch": "spanish", "italian": "italian", "italienisch": "italian",
	"danish": "danish", "dänisch": "danish", "swedish": "swedish", "schwedisch": "swedish",
	"norwegian": "norwegian", "polish": "polish", "polnisch": "polish", "portuguese": "portuguese",
	"arabic": "arabic", "arabisch": "arabic",
}

const langNames = `german|deutsch|english|englisch|french|französisch|dutch|niederländisch|spanish|spanisch|italian|italienisch|` +
	`danish|dänisch|swedish|schwedisch|norwegian|polish|polnisch|portuguese|arabic|arabisch`

// requireRes find a language (or two: "fluent German and English") a posting demands: "fluent German", "German (C1)", "fließende
// Deutschkenntnisse", "Deutsch verhandlungssicher". The tail after a hit decides whether it is only a plus.
var requireRes = []*regexp.Regexp{
	regexp.MustCompile(`(?:fluent|fluency in|proficien\w* in|excellent|strong|native|very good|good command of|business[- ]fluent|full professional)(?: written and spoken| spoken and written| verbal and written)? (` + langNames + `)(?:,? (?:and|und|&) (` + langNames + `))?\b`),
	regexp.MustCompile(`\b(` + langNames + `) (?:is a must|is required|required|is essential|is mandatory|fluency|\(?(?:c1|c2|b2)\b|language skills|skills are required|at (?:c1|c2|b2)|on a (?:c1|c2) level|verhandlungssicher|fließend|in wort und schrift)`),
	regexp.MustCompile(`(?:fließende?n?|sehr gute|gute|verhandlungssichere?n?|exzellente) (?:\w+ )?(deutschkenntnisse|englischkenntnisse|deutsch|englisch|` + langNames + `)`),
	regexp.MustCompile(`\b(deutschkenntnisse|englischkenntnisse)\b`),
}

var optionalWords = []string{"plus", "nice to have", "bonus", "advantage", "beneficial", "preferred", "von vorteil", "wünschenswert", "wäre schön"}

// Language is the one name for a spoken language in English or German ("Deutsch" is german), or ""
// when it is not one the table knows.
func Language(name string) string { return spoken[strings.ToLower(strings.TrimSpace(name))] }

// Speaks lists the languages a posting requires, sorted. A posting written in German needs German
// even when it does not say so; one that only calls a language a plus does not require it.
func Speaks(title, description string) []string {
	low := strings.ToLower(title + "\n" + description)
	set := map[string]bool{}
	for _, re := range requireRes {
		for _, m := range re.FindAllStringSubmatchIndex(low, -1) {
			tail := low[m[1]:min(len(low), m[1]+50)]
			if i := strings.IndexAny(tail, ".;\n"); i >= 0 {
				tail = tail[:i]
			}
			if containsAny(tail, optionalWords) {
				continue
			}
			for g := 2; g+1 < len(m); g += 2 {
				if m[g] >= 0 {
					set[spoken[low[m[g]:m[g+1]]]] = true
				}
			}
		}
	}
	if writtenInGerman(low) {
		set["german"] = true
	}
	return sortedKeys(set)
}

// writtenInGerman is true when German function words clearly outnumber English ones.
func writtenInGerman(low string) bool {
	count := func(ws []string) int {
		n := 0
		for _, w := range ws {
			n += strings.Count(low, " "+w+" ")
		}
		return n
	}
	de := count([]string{"und", "der", "die", "das", "wir", "sie", "mit", "für", "ihre", "unsere", "bei", "eine"})
	en := count([]string{"and", "the", "with", "you", "we", "our", "for", "your", "are", "will"})
	return de >= 8 && de > 2*en
}

func containsAny(s string, words []string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}
