package apply

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/store"
)

// Story is one canonical story in the story bank, stories/<id>.md, as the interview-prep skill writes it.
type Story struct {
	ID       string
	Title    string
	Context  string // company_context
	Role     string
	Timeline string
	Themes   []string
	Metrics  []string
	Facts    string // the Core Facts section
	File     string
}

// Meta is the one-line summary under a story's title: role, timeline and metrics, whichever are set.
func (st Story) Meta() string {
	var parts []string
	for _, p := range []string{st.Role, st.Timeline, strings.Join(st.Metrics, "; ")} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, " · ")
}

// Stories reads the story bank, sorted by title.
func Stories(home string) []Story {
	paths, _ := filepath.Glob(filepath.Join(home, "stories", "*.md"))
	var out []Story
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		st := parseStory(string(raw))
		st.File = filepath.Base(p)
		if st.ID == "" {
			st.ID = strings.TrimSuffix(st.File, ".md")
		}
		if st.Title == "" {
			st.Title = st.ID
		}
		out = append(out, st)
	}
	sort.Slice(out, func(i, k int) bool { return strings.ToLower(out[i].Title) < strings.ToLower(out[k].Title) })
	return out
}

// parseStory reads the flat front matter the story template uses: scalars, [a, b] lists and "  - x" lists.
func parseStory(md string) Story {
	var st Story
	md = strings.ReplaceAll(md, "\r\n", "\n")
	body := md
	if strings.HasPrefix(md, "---\n") {
		if end := strings.Index(md[4:], "\n---"); end >= 0 {
			front := md[4 : 4+end]
			body = md[4+end+4:]
			key := ""
			for _, line := range strings.Split(front, "\n") {
				if item, ok := strings.CutPrefix(strings.TrimSpace(line), "- "); ok && key != "" {
					st.set(key, []string{unquote(item)})
					continue
				}
				k, v, ok := strings.Cut(line, ":")
				if !ok {
					continue
				}
				key, v = strings.TrimSpace(k), strings.TrimSpace(v)
				if strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]") {
					var list []string
					for _, x := range strings.Split(v[1:len(v)-1], ",") {
						if x = unquote(strings.TrimSpace(x)); x != "" {
							list = append(list, x)
						}
					}
					st.set(key, list)
				} else if v != "" {
					st.set(key, []string{unquote(v)})
				}
			}
		}
	}
	st.Facts = section(body, "Core Facts")
	return st
}

func (st *Story) set(key string, v []string) {
	switch key {
	case "id":
		st.ID = v[0]
	case "title":
		st.Title = v[0]
	case "company_context":
		st.Context = v[0]
	case "role":
		st.Role = v[0]
	case "timeline":
		st.Timeline = v[0]
	case "themes":
		st.Themes = append(st.Themes, v...)
	case "metrics":
		st.Metrics = append(st.Metrics, v...)
	}
}

func unquote(s string) string { return strings.Trim(s, `"'`) }

// section returns the text under a "## name" heading up to the next heading of that level.
func section(md, name string) string {
	_, rest, ok := strings.Cut(md, "## "+name)
	if !ok {
		return ""
	}
	if end := strings.Index(rest, "\n## "); end >= 0 {
		rest = rest[:end]
	}
	return strings.TrimSpace(rest)
}

// StoryMatch pairs a must-have requirement with the story that best speaks to it; Story is nil when none does.
type StoryMatch struct {
	Requirement string
	Story       *Story
}

// MatchStories maps each must-have of a fit review to the story with the most words in common;
// themes count double. A story needs two points to count as a match.
func MatchStories(stories []Story, review FitReview) []StoryMatch {
	var out []StoryMatch
	for _, q := range review.Requirements {
		if q.Kind != "must" {
			continue
		}
		want := words(q.Requirement + " " + q.Wording)
		m, best := StoryMatch{Requirement: q.Requirement}, 1
		for i := range stories {
			if n := storyScore(stories[i], want); n > best {
				m.Story, best = &stories[i], n
			}
		}
		out = append(out, m)
	}
	return out
}

func storyScore(st Story, want map[string]bool) int {
	n := 0
	for w := range words(strings.Join(st.Themes, " ")) {
		if want[w] {
			n += 2
		}
	}
	for w := range words(st.Title + " " + st.Facts + " " + strings.Join(st.Metrics, " ")) {
		if want[w] {
			n++
		}
	}
	return n
}

var stopWords = map[string]bool{"and": true, "the": true, "with": true, "for": true, "years": true, "experience": true, "you": true, "our": true, "are": true, "will": true, "have": true, "from": true, "that": true, "this": true, "using": true, "strong": true, "skills": true, "ability": true, "work": true, "working": true, "plus": true}

// words splits text into lowercase words of three letters or more, minus filler; "+" and "#" stay, for C++ and C#.
func words(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !wordRune(r) }) {
		if len(w) >= 3 && !stopWords[w] {
			out[w] = true
		}
	}
	return out
}

func wordRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '+' || r == '#'
}

// StoryDraft is what the story form on the dashboard asks for; the rest of the template is left for the agent or the user.
type StoryDraft struct {
	Title, Context, Role, Timeline  string
	Themes, Metrics                 []string
	Facts                           string
	Situation, Task, Action, Result string
}

// NewStory writes a story in the template the interview-prep skill reads, under a file name no other story uses.
func NewStory(home string, d StoryDraft, now time.Time) (string, error) {
	d.Title = strings.TrimSpace(d.Title)
	if d.Title == "" {
		return "", fmt.Errorf("a story needs a title")
	}
	id := store.Slug(d.Title)
	if id == "" {
		id = "story"
	}
	dir := filepath.Join(home, "stories")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	name := id + ".md"
	for n := 2; exists(filepath.Join(dir, name)); n++ {
		name = fmt.Sprintf("%s-%d.md", id, n)
	}
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "id: %s\ntitle: %s\n", strings.TrimSuffix(name, ".md"), oneLine(d.Title))
	for _, kv := range [][2]string{{"company_context", d.Context}, {"role", d.Role}, {"timeline", d.Timeline}} {
		if v := oneLine(kv[1]); v != "" {
			fmt.Fprintf(&b, "%s: %s\n", kv[0], v)
		}
	}
	fmt.Fprintf(&b, "themes: [%s]\n", strings.Join(clean(d.Themes), ", "))
	if m := clean(d.Metrics); len(m) > 0 {
		b.WriteString("metrics:\n")
		for _, x := range m {
			fmt.Fprintf(&b, "  - %s\n", x)
		}
	}
	fmt.Fprintf(&b, "last_modified: %s\n---\n\n## Core Facts\n%s\n\n## Variants\n\n### Variant: star\n", now.Format("2006-01-02"), strings.TrimSpace(d.Facts))
	for _, kv := range [][2]string{{"Situation", d.Situation}, {"Task", d.Task}, {"Action", d.Action}, {"Result", d.Result}} {
		fmt.Fprintf(&b, "**%s:** %s\n", kv[0], strings.TrimSpace(kv[1]))
	}
	_, err := WriteFile(dir, name, []byte(b.String()))
	return name, err
}

// StoryBody is a story file without its front matter, for the preview.
func StoryBody(md string) string {
	md = strings.ReplaceAll(md, "\r\n", "\n")
	if strings.HasPrefix(md, "---\n") {
		if end := strings.Index(md[4:], "\n---"); end >= 0 {
			return strings.TrimLeft(md[4+end+4:], "\n")
		}
	}
	return md
}

// clean trims list items to one line each, without the commas and brackets the front matter list uses.
func clean(items []string) []string {
	var out []string
	for _, x := range items {
		if x = strings.Trim(oneLine(x), " ,[]"); x != "" {
			out = append(out, strings.ReplaceAll(x, ",", ";"))
		}
	}
	return out
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
