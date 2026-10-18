package search

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// skillWords maps a skill to the lower-case spellings matched as whole words in a posting.
// Go is matched apart (see hasGo): "go" is too common an English word.
var skillWords = map[string][]string{
	"python": {"python"}, "java": {"java"}, "kotlin": {"kotlin"}, "scala": {"scala"}, "rust": {"rust"},
	"ruby": {"ruby"}, "rails": {"rails", "ruby on rails"}, "php": {"php"}, "laravel": {"laravel"},
	"javascript": {"javascript"}, "typescript": {"typescript"}, "node.js": {"node.js", "nodejs"},
	"react": {"react", "react.js", "reactjs"}, "react native": {"react native"}, "vue": {"vue", "vue.js", "vuejs"},
	"angular": {"angular"}, "svelte": {"svelte"}, "next.js": {"next.js", "nextjs"}, "c#": {"c#"}, ".net": {".net", "dotnet"},
	"c++": {"c++"}, "swift": {"swift"}, "objective-c": {"objective-c"}, "flutter": {"flutter"}, "dart": {"dart"},
	"elixir": {"elixir"}, "erlang": {"erlang"}, "haskell": {"haskell"}, "clojure": {"clojure"}, "sql": {"sql"},
	"postgres": {"postgres", "postgresql"}, "mysql": {"mysql"}, "mongodb": {"mongodb", "mongo"}, "redis": {"redis"},
	"elasticsearch": {"elasticsearch", "opensearch"}, "kafka": {"kafka"}, "rabbitmq": {"rabbitmq"}, "dynamodb": {"dynamodb"},
	"cassandra": {"cassandra"}, "snowflake": {"snowflake"}, "bigquery": {"bigquery"}, "spark": {"spark", "pyspark"},
	"airflow": {"airflow"}, "dbt": {"dbt"}, "kubernetes": {"kubernetes", "k8s"}, "docker": {"docker"},
	"terraform": {"terraform"}, "ansible": {"ansible"}, "aws": {"aws", "amazon web services"}, "gcp": {"gcp", "google cloud"},
	"azure": {"azure"}, "linux": {"linux"}, "graphql": {"graphql"}, "grpc": {"grpc"}, "rest": {"rest api", "rest apis", "restful"},
	"microservices": {"microservices", "microservice"}, "ci/cd": {"ci/cd"}, "machine learning": {"machine learning"},
	"llm": {"llm", "llms", "large language models"}, "pytorch": {"pytorch"}, "tensorflow": {"tensorflow"},
	"django": {"django"}, "flask": {"flask"}, "fastapi": {"fastapi"}, "spring": {"spring boot", "spring framework"},
	"wordpress": {"wordpress"}, "shopify": {"shopify"}, "salesforce": {"salesforce"}, "figma": {"figma"},
	"ios": {"ios"}, "android": {"android"}, "distributed systems": {"distributed systems"}, "prometheus": {"prometheus"},
	"grafana": {"grafana"}, "opentelemetry": {"opentelemetry"}, "testing": {"unit testing", "test automation", "tdd"},
	"on-call": {"on-call", "on call rotation", "pager duty", "pagerduty"},
}

// Languages are the skills a search query can be built from ("go engineer").
var Languages = map[string]bool{"go": true, "python": true, "java": true, "kotlin": true, "scala": true, "rust": true,
	"ruby": true, "php": true, "javascript": true, "typescript": true, "node.js": true, "c#": true, ".net": true, "c++": true,
	"swift": true, "elixir": true, "react": true, "flutter": true}

// Skills finds the known skills in free text, sorted.
func Skills(text string) []string {
	low := strings.ToLower(text)
	set := map[string]bool{}
	for skill, spellings := range skillWords {
		for _, w := range spellings {
			if hasWord(low, w) {
				set[skill] = true
				break
			}
		}
	}
	if hasGo(text) {
		set["go"] = true
	}
	return sortedKeys(set)
}

// ListSkills reads a comma list, such as board tags: each item is a skill only when it names one exactly.
func ListSkills(tags string) []string {
	set := map[string]bool{}
	for _, t := range strings.Split(strings.ToLower(tags), ",") {
		t = strings.TrimSpace(t)
		if t == "go" || t == "golang" {
			set["go"] = true
			continue
		}
		for skill, spellings := range skillWords {
			if t == skill || contains(spellings, t) {
				set[skill] = true
			}
		}
	}
	return sortedKeys(set)
}

// hasWord finds w in s with no letter or digit right before or after it.
func hasWord(s, w string) bool {
	for i := 0; ; {
		k := strings.Index(s[i:], w)
		if k < 0 {
			return false
		}
		k += i
		end := k + len(w)
		if (k == 0 || !alnum(s[k-1])) && (end == len(s) || !alnum(s[end])) {
			return true
		}
		i = k + 1
	}
}

func alnum(b byte) bool { return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' }

// hasGo finds the language: "golang", or "Go" capitalised as a word that is not "Go-to-market"
// or "Go to".
func hasGo(text string) bool {
	if hasWord(strings.ToLower(text), "golang") {
		return true
	}
	for i := 0; ; {
		k := strings.Index(text[i:], "Go")
		if k < 0 {
			return false
		}
		k += i
		end := k + 2
		rest := text[end:]
		if (k == 0 || !alnum(text[k-1])) && (end == len(text) || !alnum(text[end]) && text[end] != '-') &&
			!strings.HasPrefix(rest, " to ") && !strings.HasPrefix(rest, " live") {
			return true
		}
		i = k + 1
	}
}

var years = regexp.MustCompile(`(\d{1,2})\s*\+?\s*(?:years|yrs)[^.]{0,40}experience`)

// Level is the seniority a posting asks for: from the title, else from the years of experience.
func Level(title, description string) string {
	t := " " + strings.ToLower(title) + " "
	for _, l := range []struct{ level, words string }{
		{"intern", "intern,internship,trainee,werkstudent"},
		{"junior", "junior,jr,jr.,graduate,entry level,entry-level"},
		{"manager", "manager,head of,director,vp,vice president"},
		{"principal", "principal,distinguished"},
		{"staff", "staff"},
		{"lead", "lead,tech lead,team lead"},
		{"senior", "senior,sr,sr.,snr"},
		{"mid", "mid,mid-level,intermediate,medior"},
	} {
		for _, w := range strings.Split(l.words, ",") {
			if hasWord(t, w) {
				return l.level
			}
		}
	}
	if m := years.FindStringSubmatch(strings.ToLower(description)); m != nil {
		n, _ := strconv.Atoi(m[1])
		switch {
		case n >= 5:
			return "senior"
		case n >= 2:
			return "mid"
		default:
			return "junior"
		}
	}
	return ""
}

// Employment is the kind of engagement: full-time, part-time, contract, freelance or internship.
// hint is the board's own field (Remotive's job_type), trusted first.
func Employment(hint, title, description string) string {
	switch h := strings.NewReplacer("_", "-", " ", "-").Replace(strings.ToLower(hint)); {
	case strings.Contains(h, "freelance"):
		return "freelance"
	case strings.Contains(h, "contract"):
		return "contract"
	case strings.Contains(h, "part"):
		return "part-time"
	case strings.Contains(h, "intern"):
		return "internship"
	case strings.Contains(h, "full"):
		return "full-time"
	}
	t := strings.ToLower(title)
	switch {
	case hasWord(t, "freelance") || hasWord(t, "freelancer") || hasWord(t, "independent") || hasWord(t, "freiberuflich"):
		return "freelance"
	case hasWord(t, "contract") || hasWord(t, "contractor"):
		return "contract"
	case hasWord(t, "part-time") || hasWord(t, "part time") || hasWord(t, "teilzeit"):
		return "part-time"
	case hasWord(t, "intern") || hasWord(t, "internship") || hasWord(t, "praktikum") || hasWord(t, "werkstudent"):
		return "internship"
	}
	d := strings.ToLower(description)
	switch {
	case hasWord(d, "freelance") || hasWord(d, "freelancer") || hasWord(d, "independent contractor"):
		return "freelance"
	case hasWord(d, "full-time") || hasWord(d, "full time") || hasWord(d, "permanent position") || hasWord(d, "permanent role") ||
		hasWord(d, "vollzeit") || hasWord(d, "festanstellung") || hasWord(d, "unbefristet"):
		return "full-time"
	case hasWord(d, "contractor") || hasWord(d, "contract role") || hasWord(d, "contract position") || hasWord(d, "b2b contract") || hasWord(d, "1099"):
		return "contract"
	case hasWord(d, "part-time") || hasWord(d, "part time") || hasWord(d, "teilzeit"):
		return "part-time"
	}
	return ""
}

// Visa reads sponsorship: "yes" when offered, "no" when ruled out, "" when not mentioned.
func Visa(description string) string {
	d := strings.ToLower(description)
	for _, w := range []string{"no visa sponsorship", "not able to sponsor", "unable to sponsor", "cannot sponsor", "can't sponsor",
		"do not sponsor", "does not sponsor", "will not sponsor", "won't sponsor", "not offer visa sponsorship", "without sponsorship",
		"not provide visa sponsorship", "sponsorship is not available"} {
		if strings.Contains(d, w) {
			return "no"
		}
	}
	for _, w := range []string{"visa sponsorship", "sponsor your visa", "sponsor visas", "relocation and visa", "visa support", "work permit support"} {
		if strings.Contains(d, w) {
			return "yes"
		}
	}
	return ""
}

// Enrich derives the skills, level, employment type, regions, visa stance and languages of a posting from its
// title, the board's tags and the description.
func Enrich(p *Posting) {
	head := p.Description
	if len(head) > 6000 {
		head = head[:6000]
	}
	set := map[string]bool{}
	for _, s := range append(ListSkills(p.Tags), Skills(p.Title+"\n"+head)...) {
		set[s] = true
	}
	p.Skills = sortedKeys(set)
	p.Level = Level(p.Title, head)
	hint := p.Employment
	if hint == "" {
		hint = Employment("", p.Tags, "") // a board tag such as "Full-Time" or "Contract"
	}
	p.Employment = Employment(hint, p.Title, head)
	p.Regions = PostingPlaces(*p)
	p.Visa = Visa(head)
	p.Speaks = Speaks(p.Title, head)
}

// AllTags is the board's tags plus the derived ones: skills as plain words, the rest as level:,
// type:, region:, visa: and speaks: tags, so the pipeline can filter on them.
func (p Posting) AllTags() string {
	var tags []string
	have := map[string]bool{}
	add := func(t string) {
		if t != "" && !have[strings.ToLower(t)] {
			have[strings.ToLower(t)] = true
			tags = append(tags, t)
		}
	}
	for _, t := range splitTags(p.Tags) {
		if strings.EqualFold(t, "golang") { // one tag for the language, so filtering on "go" finds both
			t = "go"
		}
		add(t)
	}
	for _, s := range p.Skills {
		add(s)
	}
	if p.Level != "" {
		add("level:" + p.Level)
	}
	if p.Employment != "" {
		add("type:" + p.Employment)
	}
	for _, r := range p.Regions {
		if _, ok := Regions[r]; ok || r == Worldwide {
			add("region:" + r)
		}
	}
	if p.Visa != "" {
		add("visa:" + p.Visa)
	}
	for _, l := range p.Speaks {
		add("speaks:" + l)
	}
	return strings.Join(tags, ",")
}

func splitTags(s string) []string {
	var out []string
	for _, t := range strings.Split(s, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
