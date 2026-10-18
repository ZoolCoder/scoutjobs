// Package match scores job postings against the user's profile and preferences, so a search shows
// the jobs worth applying to first and can hide the ones that break a stated must.
package match

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/zoolcoder/scoutjobs/internal/search"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

// Profile is what a posting is measured against, read from profile.md and the preferences.
type Profile struct {
	Roles          []string `json:"roles,omitempty"` // "backend engineer", "go engineer"
	Level          string   `json:"level,omitempty"`
	Remote         string   `json:"remote,omitempty"` // remote, hybrid, onsite, or empty
	Places         []string `json:"places,omitempty"` // where the user wants to work, as written
	Eligible       []string `json:"eligible,omitempty"`
	Strong         []string `json:"strong,omitempty"`
	Working        []string `json:"working,omitempty"`
	Learning       []string `json:"learning,omitempty"`
	Avoid          []string `json:"avoid,omitempty"`       // skills the user avoids
	AvoidTypes     []string `json:"avoid_types,omitempty"` // employment types ruled out
	AvoidOnCall    bool     `json:"avoid_on_call,omitempty"`
	OnsiteBreaker  bool     `json:"onsite_breaker,omitempty"` // on-site work is a deal-breaker
	SalaryFloor    int64    `json:"salary_floor,omitempty"`
	SalaryCurrency string   `json:"salary_currency,omitempty"`
	Speaks         []string `json:"speaks,omitempty"` // languages the user works in: german, english
	// Answers is the answer rate of each source that clearly beats or trails the rest, from the user's own applications.
	Answers         map[string]SourceRate `json:"-"`
	locationWritten string
}

// SourceRate is how often one source answered the user, against how often all sources did.
type SourceRate struct {
	Rate, Overall, Applied int
}

const (
	// sourceMin is how many applications a source needs before its answer rate counts.
	sourceMin = 5
	// sourceGap is how many points a source's answer rate must differ from the overall rate to count.
	sourceGap = 15
	// sourceWeight is what a clearly better or worse source adds to or takes off a score.
	sourceWeight = 5
)

// SourceRates picks the sources whose answer rate differs clearly from the overall rate.
func SourceRates(f store.Funnel) map[string]SourceRate {
	applied, answered := 0, 0
	for _, r := range f.Source {
		applied, answered = applied+r.Applied, answered+r.Responded
	}
	if applied == 0 {
		return nil
	}
	overall := answered * 100 / applied
	out := map[string]SourceRate{}
	for _, r := range f.Source {
		if d := r.Rate() - overall; r.Applied >= sourceMin && (d >= sourceGap || d <= -sourceGap) {
			out[strings.ToLower(r.Group)] = SourceRate{r.Rate(), overall, r.Applied}
		}
	}
	return out
}

// Empty is true when the profile gives nothing to match on.
func (p Profile) Empty() bool {
	return len(p.Roles) == 0 && len(p.Strong) == 0 && len(p.Working) == 0 && p.Level == "" && len(p.Places) == 0
}

// Wants is where the user wants to work: the places they named, else their location, else the
// regions they may work from.
func (p Profile) Wants() []string {
	if len(p.Places) > 0 {
		return p.Places
	}
	return p.Eligible
}

// Load reads profile.md in the data folder and the stored preferences.
func Load(s *store.Store) Profile {
	b, _ := os.ReadFile(filepath.Join(s.Home, "profile.md"))
	prefs, _ := s.Preferences()
	p := Parse(string(b), prefs)
	if f, err := s.Funnel(); err == nil {
		p.Answers = SourceRates(f)
	}
	return p
}

// Parse reads the "- Key: value" lines of profile.md, then lets stated or confident preferences add
// to them.
func Parse(profile string, prefs []store.Preference) Profile {
	var p Profile
	var dealBreakers string
	for _, line := range strings.Split(profile, "\n") {
		key, val, ok := strings.Cut(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-")), ":")
		val = strings.TrimSpace(val)
		if !ok || val == "" {
			continue
		}
		switch k := strings.ToLower(strings.TrimSpace(key)); {
		case k == "roles":
			p.Roles = roles(val)
		case k == "seniority":
			p.Level = search.Level(val, "")
		case strings.HasPrefix(k, "remote"):
			p.Remote = remoteKind(val)
		case strings.HasPrefix(k, "locations"):
			p.Places = placeList(val)
		case strings.HasPrefix(k, "location"):
			p.locationWritten = val
		case strings.HasPrefix(k, "work authori"):
			p.Eligible = append(p.Eligible, regionsOf(val)...)
		case strings.HasPrefix(k, "salary"):
			p.SalaryFloor, p.SalaryCurrency = salary(val)
		case strings.HasPrefix(k, "deal-breaker") || strings.HasPrefix(k, "dealbreaker"):
			dealBreakers += " " + val
		case k == "languages":
			p.Speaks = languages(val)
		case k == "strong":
			p.Strong = search.Skills(val)
		case k == "working":
			p.Working = search.Skills(val)
		case k == "learning":
			p.Learning = search.Skills(val)
		}
	}
	for _, pr := range prefs {
		if pr.Source != "stated" && pr.Confidence < 0.5 {
			continue
		}
		v := strings.TrimSpace(pr.Value)
		switch strings.ToLower(pr.Key) {
		case "stack_avoid", "avoid_stack", "avoid":
			p.Avoid = append(p.Avoid, avoided(v)...)
		case "on_call":
			p.AvoidOnCall = p.AvoidOnCall || strings.Contains(strings.ToLower(v), "avoid") || strings.EqualFold(v, "no")
		case "employment_avoid", "avoid_employment":
			dealBreakers += " " + v
		case "remote", "work_mode":
			p.Remote = remoteKind(v)
		case "location", "locations":
			p.Places = placeList(v)
		case "salary_floor", "salary_min":
			p.SalaryFloor, p.SalaryCurrency = salary(v)
		case "seniority", "level":
			p.Level = search.Level(v, "")
		case "roles", "target_roles":
			p.Roles = roles(v)
		case "languages", "spoken_languages":
			p.Speaks = languages(v)
		}
	}
	if len(p.Places) == 0 && p.locationWritten != "" {
		for _, part := range placeList(p.locationWritten) {
			if exp := search.Expand(part); len(exp) > 1 || search.Regions[exp[0]] == exp[0] && !timeZone(part) {
				p.Places = append(p.Places, part)
			}
		}
	}
	for _, place := range p.Places {
		if exp := search.Expand(place); len(exp) > 0 {
			p.Eligible = append(p.Eligible, exp[len(exp)-1])
		}
	}
	p.Eligible = unique(p.Eligible)
	d := strings.ToLower(dealBreakers)
	for _, t := range []string{"freelance", "contract", "part-time", "internship"} {
		if strings.Contains(d, strings.TrimSuffix(t, "-time")) {
			p.AvoidTypes = append(p.AvoidTypes, t)
		}
	}
	p.Avoid = unique(append(p.Avoid, avoided(dealBreakers)...))
	p.AvoidOnCall = p.AvoidOnCall || strings.Contains(d, "on-call") || strings.Contains(d, "on call")
	p.OnsiteBreaker = strings.Contains(d, "on-site") || strings.Contains(d, "onsite") || strings.Contains(d, "office")
	return p
}

var nouns = map[string]bool{"engineer": true, "developer": true, "programmer": true, "architect": true, "sre": true,
	"devops": true, "manager": true, "designer": true, "scientist": true, "analyst": true, "lead": true}

// roles splits "Backend / Go engineer" into "backend engineer" and "go engineer".
// languages reads "German (C1), English" into the spoken languages it names.
func languages(v string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ';' || r == '/' || r == '|' }) {
		if w := strings.Fields(strings.ReplaceAll(part, "(", " ")); len(w) > 0 {
			if l := search.Language(w[0]); l != "" {
				out = append(out, l)
			}
		}
	}
	return unique(out)
}

func capital(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func roles(v string) []string {
	parts := strings.FieldsFunc(strings.ToLower(v), func(r rune) bool { return r == '/' || r == ',' || r == ';' || r == '|' })
	noun := ""
	for _, part := range parts {
		w := strings.Fields(part)
		if len(w) > 0 && nouns[w[len(w)-1]] {
			noun = w[len(w)-1]
			break
		}
	}
	var out []string
	for _, part := range parts {
		for _, alt := range strings.Split(part, " or ") {
			w := strings.Fields(alt)
			if len(w) == 0 {
				continue
			}
			if noun != "" && !nouns[w[len(w)-1]] {
				w = append(w, noun)
			}
			out = append(out, strings.Join(w, " "))
		}
	}
	return unique(out)
}

// avoided reads the skills in a list of things to avoid; on-call is weighed apart.
func avoided(v string) []string {
	var out []string
	for _, s := range append(search.ListSkills(v), search.Skills(v)...) {
		if s != "on-call" {
			out = append(out, s)
		}
	}
	return out
}

// timeZone is true for a "Location / time zone" part that names only a zone.
func timeZone(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "cet", "cest", "utc", "gmt", "est", "pst", "ist":
		return true
	}
	return false
}

func remoteKind(v string) string {
	v = strings.ToLower(v)
	switch {
	case strings.Contains(v, "remote") && !strings.Contains(v, "hybrid") && !strings.Contains(v, "onsite") && !strings.Contains(v, "on-site"):
		return "remote"
	case strings.Contains(v, "hybrid"):
		return "hybrid"
	case strings.Contains(v, "onsite") || strings.Contains(v, "on-site") || strings.Contains(v, "office"):
		return "onsite"
	}
	return ""
}

func placeList(v string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ';' || r == '/' }) {
		for _, alt := range strings.Split(part, " or ") {
			if alt = strings.TrimSpace(alt); alt != "" {
				out = append(out, alt)
			}
		}
	}
	return out
}

// regionsOf reads the regions a work authorization covers: "EU citizen" is Europe.
func regionsOf(v string) []string {
	var out []string
	for _, place := range search.Places(v) {
		if r, ok := search.Regions[place]; ok {
			out = append(out, r)
		}
	}
	return out
}

func salary(v string) (int64, string) {
	lo, _, cur := store.ParseSalary(v)
	if lo == 0 {
		lo, _ = strconv.ParseInt(strings.TrimSpace(strings.Trim(v, "€$£ ")), 10, 64)
	}
	return lo, cur
}

// Result is how well one posting fits.
type Result struct {
	Score int      `json:"score"`
	Why   []string `json:"why,omitempty"`
	Miss  []string `json:"miss,omitempty"`
	Hard  []string `json:"hard,omitempty"` // breaks a stated must: wrong place, ruled-out engagement, below the salary floor
}

var levels = map[string]int{"intern": 0, "junior": 1, "mid": 2, "senior": 3, "lead": 4, "staff": 4, "principal": 5, "manager": 4}

// Score measures a posting against the profile, 0 to 100: target role 20, skills 45, level 15,
// work mode 10, place 10, minus what the user avoids.
func (pr Profile) Score(p search.Posting) Result {
	var r Result
	score := 0.0
	title := strings.ToLower(p.Title)

	if role, inTitle := pr.roleHit(title, p.Skills); role != "" && inTitle {
		score += 20
		r.Why = append(r.Why, "target role: "+role)
	} else if role != "" {
		score += 12
		r.Why = append(r.Why, "close to "+role)
	} else if len(pr.Roles) > 0 {
		for n := range nouns {
			if strings.Contains(title, n) {
				score += 8
				break
			}
		}
		r.Miss = append(r.Miss, "not one of your target roles")
	}

	r, score = pr.skillFit(p.Skills, r, score)

	if want, ok := levels[pr.Level]; ok && p.Level != "" {
		got := levels[p.Level]
		switch got - want {
		case 0:
			score += 15
			r.Why = append(r.Why, p.Level+" level")
		case 1, -1:
			score += 7
			r.Miss = append(r.Miss, p.Level+" level, you target "+pr.Level)
		default:
			r.Miss = append(r.Miss, p.Level+" level, you target "+pr.Level)
		}
	} else {
		score += 7
	}

	remote := search.IsRemote(p)
	switch {
	case pr.Remote == "remote" && remote:
		score += 10
		r.Why = append(r.Why, "remote")
	case pr.Remote == "remote" && isOnsite(p.Remote):
		if pr.OnsiteBreaker {
			r.Hard = append(r.Hard, strings.ToLower(p.Remote)+", on-site is a deal-breaker")
		} else {
			r.Miss = append(r.Miss, strings.ToLower(p.Remote)+", you want remote")
		}
	case pr.Remote == "remote":
		score += 4
	default:
		score += 10
	}

	if wants := pr.Wants(); len(wants) > 0 {
		ok, note := search.LocationMatch(p, wants)
		switch {
		case !ok:
			r.Hard = append(r.Hard, "location: "+note+", you want "+strings.Join(wants, ", "))
		case note != "":
			score += 5
			r.Miss = append(r.Miss, note)
		default:
			score += 10
			r.Why = append(r.Why, "location fits")
		}
	} else {
		score += 5
	}

	if len(pr.Speaks) > 0 {
		for _, l := range p.Speaks {
			if !has(pr.Speaks, l) {
				score -= 15
				r.Miss = append(r.Miss, "asks for "+capital(l)+", which you do not list")
			} else if l != "english" {
				r.Why = append(r.Why, "asks for "+capital(l)+", which you speak")
			}
		}
	}

	avoided := 0
	for _, a := range pr.Avoid {
		if has(p.Skills, a) && avoided < 2 {
			avoided++
			score -= 15
			r.Miss = append(r.Miss, "uses "+a+", which you avoid")
		}
	}
	if pr.AvoidOnCall && has(p.Skills, "on-call") {
		score -= 10
		r.Miss = append(r.Miss, "on-call, which you avoid")
	}

	switch {
	case p.Employment == "":
	case has(pr.AvoidTypes, p.Employment):
		r.Hard = append(r.Hard, p.Employment+" engagement, a deal-breaker")
	case p.Employment != "full-time":
		score -= 5
		r.Miss = append(r.Miss, p.Employment+" engagement")
	}
	if p.Visa == "yes" {
		r.Why = append(r.Why, "visa sponsorship")
	}

	if pr.SalaryFloor > 0 && p.SalaryMax > 0 && (p.Currency == "" || pr.SalaryCurrency == "" || strings.EqualFold(p.Currency, pr.SalaryCurrency)) {
		if p.SalaryMax < pr.SalaryFloor {
			r.Hard = append(r.Hard, fmt.Sprintf("pays up to %d, your floor is %d", p.SalaryMax, pr.SalaryFloor))
		} else if p.SalaryMin >= pr.SalaryFloor {
			r.Why = append(r.Why, "pays above your floor")
		}
	}
	if a, ok := pr.Answers[strings.ToLower(strings.TrimSpace(p.Source))]; ok {
		if a.Rate > a.Overall {
			score += sourceWeight
			r.Why = append(r.Why, fmt.Sprintf("%s answered %d%% of your %d applications", p.Source, a.Rate, a.Applied))
		} else {
			score -= sourceWeight
			r.Miss = append(r.Miss, fmt.Sprintf("%s answered %d%% of your %d applications, %d%% overall", p.Source, a.Rate, a.Applied, a.Overall))
		}
	}
	r.Score = int(math.Round(math.Max(0, math.Min(100, score))))
	return r
}

// skillFit weighs the posting's skills, 45 points: 15 for its main language, 30 for the rest. A
// skill the profile lacks counts half against a known one; on-call is weighed apart.
func (pr Profile) skillFit(skills []string, r Result, score float64) (Result, float64) {
	var langs, langHit, matched, other []string
	var hit, miss float64
	for _, s := range skills {
		known := has(pr.Strong, s) || has(pr.Working, s)
		switch {
		case s == "on-call" || has(pr.Learning, s) && !known:
		case search.Languages[s]:
			langs = append(langs, s)
			if known {
				langHit = append(langHit, s)
			}
		case has(pr.Strong, s):
			hit += 2
			matched = append(matched, s)
		case has(pr.Working, s):
			hit++
			matched = append(matched, s)
		default:
			miss++
			other = append(other, s)
		}
	}
	switch {
	case len(langs) == 0:
		score += 8
	case len(langHit) == 0:
		score -= 10
		r.Miss = append(r.Miss, "asks for "+strings.Join(first(langs, 3), ", "))
	default:
		score += 8
		for _, l := range langHit {
			if has(pr.Strong, l) {
				score += 7
				break
			}
		}
		other = append(other, without(langs, langHit)...)
	}
	if hit+miss == 0 {
		score += 10
	} else {
		score += 30 * hit / math.Max(hit+miss/2, 3)
	}
	if all := append(langHit, matched...); len(all) > 0 {
		r.Why = append(r.Why, "skills: "+strings.Join(all, ", "))
	}
	if len(other) > 0 {
		r.Miss = append(r.Miss, "not in your profile: "+strings.Join(first(other, 4), ", "))
	}
	return r, score
}

func without(list, drop []string) []string {
	var out []string
	for _, x := range list {
		if !has(drop, x) {
			out = append(out, x)
		}
	}
	return out
}

// roleHit is the target role the title matches: every word of a role other than the noun is in
// the title ("backend engineer" fits "Senior Backend Engineer"), or, with inTitle false, some only
// in the posting's skills ("go engineer" is close to "Platform Engineer" asking for Go).
func (pr Profile) roleHit(title string, skills []string) (role string, inTitle bool) {
	words := " " + strings.NewReplacer("(", " ", ")", " ", ",", " ", "/", " ", "-", " ").Replace(title) + " "
	if strings.Contains(words, " golang ") {
		words += "go "
	}
	var near string
	for _, role := range pr.Roles {
		var keys []string
		for _, w := range strings.Fields(role) {
			if !nouns[w] && w != "software" {
				keys = append(keys, w)
			}
		}
		if len(keys) == 0 {
			continue
		}
		all, known := true, true
		for _, k := range keys {
			if !strings.Contains(words, " "+k+" ") {
				all = false
				known = known && has(skills, k)
			}
		}
		switch {
		case all:
			return role, true
		case known && near == "" && hasNoun(words) && !otherSpecialty(words, role):
			near = role
		}
	}
	return near, false
}

// specialties are kinds of work a title can name that a skill match does not make up for.
var specialties = []string{"qa", "quality", "test", "tester", "frontend", "front end", "mobile", "ios", "android", "data",
	"ml", "security", "sales", "support", "solutions", "consultant", "implementation", "designer", "recruiter",
	"marketing", "analyst", "embedded", "game", "salesforce", "sap"}

// otherSpecialty is true when the title names a specialty the target role does not.
func otherSpecialty(words, role string) bool {
	for _, s := range specialties {
		if strings.Contains(words, " "+s+" ") && !strings.Contains(" "+role+" ", " "+s+" ") {
			return true
		}
	}
	return false
}

func hasNoun(words string) bool {
	for n := range nouns {
		if strings.Contains(words, " "+n+" ") {
			return true
		}
	}
	return false
}

func isOnsite(mode string) bool {
	m := strings.ToLower(mode)
	return strings.Contains(m, "onsite") || strings.Contains(m, "on-site") || strings.Contains(m, "office") || strings.Contains(m, "hybrid")
}

// Suggestion is a ready search built from the profile.
type Suggestion struct {
	Name       string `json:"name"`
	Queries    string `json:"queries"`
	Location   string `json:"location,omitempty"`
	RemoteOnly bool   `json:"remote_only,omitempty"`
}

// Suggest builds up to three searches from the profile: the target roles, the same at the
// target level, and the strongest languages.
func (pr Profile) Suggest() []Suggestion {
	loc := strings.Join(pr.Places, ", ")
	remote := pr.Remote == "remote"
	var out []Suggestion
	seen := map[string]bool{}
	add := func(name string, queries []string) {
		q := strings.Join(unique(queries), ", ")
		if q == "" || seen[q] {
			return
		}
		seen[q] = true
		out = append(out, Suggestion{Name: name, Queries: q, Location: loc, RemoteOnly: remote})
	}
	add("Your target roles", pr.Roles)
	if pr.Level != "" && pr.Level != "mid" {
		var qs []string
		for _, r := range pr.Roles {
			qs = append(qs, pr.Level+" "+r)
		}
		add(strings.ToUpper(pr.Level[:1])+pr.Level[1:]+" roles", qs)
	}
	var langs []string
	for _, s := range pr.Strong {
		if search.Languages[s] {
			langs = append(langs, s+" developer")
		}
	}
	add("Your strongest languages", first(langs, 3))
	return out
}

func has(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func first(list []string, n int) []string {
	if len(list) > n {
		return list[:n]
	}
	return list
}

func unique(list []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range list {
		if x = strings.TrimSpace(x); x != "" && !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
