package store

import "strings"

// PrepItem is one step of the interview prep checklist.
type PrepItem struct {
	Key, Label string
}

// PrepItems is the fixed checklist every interview carries; Interview.Prep stores the done keys.
var PrepItems = []PrepItem{
	{"company", "Research the company and product"},
	{"posting", "Map the posting to your stories"},
	{"stories", "Rehearse three STAR stories"},
	{"questions", "Write questions to ask"},
	{"mock", "Run a mock interview"},
	{"logistics", "Test the link, room or route"},
}

// checkedPrep keeps known keys only, in checklist order, so stored values stay comparable.
func checkedPrep(v string) string {
	have := map[string]bool{}
	for _, k := range strings.Split(v, ",") {
		have[strings.TrimSpace(k)] = true
	}
	var out []string
	for _, it := range PrepItems {
		if have[it.Key] {
			out = append(out, it.Key)
		}
	}
	return strings.Join(out, ",")
}

// PrepStatusOf derives todo, in_progress or ready from the done keys.
func PrepStatusOf(done string) string {
	n := 0
	if done != "" {
		n = len(strings.Split(done, ","))
	}
	switch {
	case n == 0:
		return "todo"
	case n >= len(PrepItems):
		return "ready"
	}
	return "in_progress"
}

// PrepDone reports whether a checklist key is ticked on the interview.
func (i Interview) PrepDone(key string) bool {
	for _, k := range strings.Split(i.Prep, ",") {
		if k == key {
			return true
		}
	}
	return false
}

// PrepCount is the number of ticked checklist items.
func (i Interview) PrepCount() int {
	if i.Prep == "" {
		return 0
	}
	return len(strings.Split(i.Prep, ","))
}
