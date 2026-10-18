package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/docx"
	"github.com/zoolcoder/scoutjobs/internal/match"
	"github.com/zoolcoder/scoutjobs/internal/search"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

// githubAPI is swapped for a test server in tests.
var githubAPI = "https://api.github.com"

var githubUserRe = regexp.MustCompile(`github\.com/([A-Za-z0-9](?:[A-Za-z0-9-]{0,38}))`)

// Found is one skill the profile does not list yet, with the places it showed up.
type Found struct {
	Skill   string   `json:"skill"`
	Sources []string `json:"sources"`
}

func runProfile(s *store.Store, rest []string, out io.Writer) error {
	sc, rest := sub(rest)
	if sc != "expand" {
		return errors.New("usage: scoutjobs profile expand [--github user] [--add skill,skill|all] [--json]")
	}
	return runProfileExpand(s, rest, out)
}

// runProfileExpand finds skills in the CV files, the LinkedIn export and public GitHub repositories
// that profile.md does not list, on the machine: no agent reads those sources. --add appends the
// chosen ones to the "- Working:" line, so nothing is written without the user naming it.
func runProfileExpand(s *store.Store, rest []string, out io.Writer) error {
	fs := flag.NewFlagSet("profile expand", flag.ContinueOnError)
	user := fs.String("github", "", "GitHub user; default the github.com link in profile.md")
	add := fs.String("add", "", "comma separated skills to append to Working, or all")
	asJSON := fs.Bool("json", false, "")
	if _, err := parse(fs, rest); err != nil {
		return err
	}
	path := filepath.Join(s.Home, "profile.md")
	b, _ := os.ReadFile(path)
	profile := string(b)
	if *user == "" {
		if m := githubUserRe.FindStringSubmatch(profile); m != nil {
			*user = m[1]
		}
	}
	known := map[string]bool{}
	pr := match.Parse(profile, nil)
	for _, list := range [][]string{pr.Strong, pr.Working, pr.Learning} {
		for _, sk := range list {
			known[sk] = true
		}
	}
	sources := map[string][]string{}
	note := func(where string, skills []string) {
		for _, sk := range skills {
			if !known[sk] && !slices.Contains(sources[sk], where) {
				sources[sk] = append(sources[sk], where)
			}
		}
	}
	var skipped []string
	for _, dir := range []string{"cv", "linkedin"} {
		readFolder(filepath.Join(s.Home, dir), dir, note)
	}
	if *user == "" {
		skipped = append(skipped, "GitHub: no github.com link in profile.md; pass --github")
	} else if err := githubSkills(*user, note); err != nil {
		skipped = append(skipped, "GitHub: "+err.Error())
	}
	var found []Found
	for _, sk := range slices.Sorted(maps.Keys(sources)) {
		found = append(found, Found{sk, sources[sk]})
	}
	// skills seen in more places first: they are the surest
	slices.SortStableFunc(found, func(a, b Found) int { return len(b.Sources) - len(a.Sources) })

	var added []string
	if *add != "" {
		for _, f := range found {
			if *add == "all" || slices.Contains(splitList(*add), f.Skill) {
				added = append(added, f.Skill)
			}
		}
		if len(added) > 0 {
			if err := os.WriteFile(path, []byte(appendWorking(profile, added)), 0o600); err != nil {
				return err
			}
		}
	}
	if found == nil {
		found = []Found{}
	}
	return emit(out, *asJSON, map[string]any{"found": found, "added": added, "skipped": skipped}, func() {
		if len(found) == 0 {
			fmt.Fprintln(out, "no skills beyond what profile.md lists")
		}
		for _, f := range found {
			fmt.Fprintf(out, "+ %-16s %s\n", f.Skill, strings.Join(f.Sources, ", "))
		}
		for _, m := range skipped {
			fmt.Fprintln(out, "skipped "+m)
		}
		if len(added) > 0 {
			fmt.Fprintf(out, "added to Working in profile.md: %s\n", strings.Join(added, ", "))
		} else if len(found) > 0 {
			fmt.Fprintln(out, "confirm the ones that are true, then: scoutjobs profile expand --add skill,skill")
		}
	})
}

func readFolder(dir, label string, note func(string, []string)) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.EqualFold(name, "README.md") {
			continue
		}
		p := filepath.Join(dir, name)
		var text string
		switch strings.ToLower(filepath.Ext(name)) {
		case ".md", ".txt", ".csv", ".tex", ".json":
			b, _ := os.ReadFile(p)
			text = string(b)
		case ".pdf":
			text, _, _ = docx.PDFText(context.Background(), p)
		}
		if text != "" {
			note(label+"/"+name, search.Skills(text))
		}
	}
}

// githubSkills reads the user's own public repositories: language, topics and description.
func githubSkills(user string, note func(string, []string)) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	u := githubAPI + "/users/" + url.PathEscape(user) + "/repos?per_page=100&type=owner&sort=pushed"
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "scoutjobs")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("%s: http %d", u, res.StatusCode)
	}
	var repos []struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Language    string   `json:"language"`
		Topics      []string `json:"topics"`
		Fork        bool     `json:"fork"`
		Archived    bool     `json:"archived"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 5<<20)).Decode(&repos); err != nil {
		return err
	}
	for _, r := range repos {
		if r.Fork { // a fork is someone else's code
			continue
		}
		where := "github/" + r.Name
		note(where, search.ListSkills(strings.Join(append([]string{r.Language}, r.Topics...), ",")))
		note(where, search.Skills(r.Description))
	}
	return nil
}

// appendWorking adds skills to the "- Working:" line, or adds that line under "## Skills".
func appendWorking(profile string, skills []string) string {
	lines := strings.Split(profile, "\n")
	for i, l := range lines {
		key, val, ok := strings.Cut(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "-")), ":")
		if ok && strings.EqualFold(strings.TrimSpace(key), "working") {
			if val = strings.TrimSpace(val); val != "" {
				val += ", "
			}
			lines[i] = "- Working: " + val + strings.Join(skills, ", ")
			return strings.Join(lines, "\n")
		}
	}
	for i, l := range lines {
		if strings.EqualFold(strings.TrimSpace(l), "## Skills") {
			return strings.Join(slices.Insert(lines, i+1, "- Working: "+strings.Join(skills, ", ")), "\n")
		}
	}
	return strings.TrimRight(profile, "\n") + "\n\n## Skills\n- Working: " + strings.Join(skills, ", ") + "\n"
}
