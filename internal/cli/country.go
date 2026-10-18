package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/zoolcoder/scoutjobs/internal/country"
	"github.com/zoolcoder/scoutjobs/internal/search"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

// Suggestion is a preference the country implies and the user has to confirm.
type Suggestion struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Why    string `json:"why"`
	Status string `json:"status"` // proposed | kept: you stated it | the reason it was not proposed
}

func runCountry(s *store.Store, rest []string, out io.Writer) error {
	sc, rest := sub(rest)
	switch sc {
	case "list":
		return emit(out, hasFlag(rest, "--json"), country.All(), func() {
			for _, c := range country.All() {
				fmt.Fprintf(out, "%s  %-22s %s  %s\n", c.Code, c.Name, c.Currency, strings.Join(c.Boards, ", "))
			}
		})
	case "show", "set":
		return runCountrySet(s, sc == "set", rest, out)
	}
	return errors.New("usage: scoutjobs country list | show <code|name> | set <code|name> [--json]")
}

// runCountrySet reads what the country implies. set writes the facts to config.yaml (country,
// currency, local boards) and proposes the rest as preferences for the user to confirm; nothing
// about the person, salary included, is assumed.
func runCountrySet(s *store.Store, write bool, rest []string, out io.Writer) error {
	fs := flag.NewFlagSet("country", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "")
	pos, err := parse(fs, rest)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return errors.New("name a country: scoutjobs country set DK")
	}
	name := strings.Join(pos, " ")
	c, known := country.Lookup(name)
	if !known {
		if _, ok := search.Regions[strings.ToLower(name)]; !ok {
			return fmt.Errorf("%s is not a country scoutjobs knows; see scoutjobs country list", name)
		}
		// a country without a table row: no local boards or currency, the remote boards and the
		// location filter still work
		c = country.Country{Name: name}
	}
	cfg := LoadConfig(s.Home)
	// another country's local boards go when the country changes; the user's own boards stay
	var boards []string
	for _, b := range cfg.SearchBoards {
		if owner := country.BoardOwner(b); owner == "" || owner == c.Code {
			boards = append(boards, b)
		}
	}
	for _, b := range c.Boards {
		if !slices.Contains(boards, b) {
			boards = append(boards, b)
		}
	}
	config := map[string]string{"country": c.Code, "search_boards": strings.Join(boards, ", ")}
	if c.Currency != "" {
		config["currency"] = c.Currency
	}
	if c.Code == "" {
		config["country"] = name
	}
	suggestions := countrySuggestions(c)
	if write {
		path := filepath.Join(s.Home, "config.yaml")
		for _, k := range []string{"country", "currency", "search_boards"} {
			if v, ok := config[k]; ok {
				if err := setConfigValue(path, k, v); err != nil {
					return err
				}
			}
		}
		for i, sg := range suggestions {
			err := s.ProposePreference(store.Proposal{Key: sg.Key, Value: sg.Value, Confidence: 0.6, Evidence: sg.Why})
			switch {
			case errors.Is(err, store.ErrStated):
				suggestions[i].Status = "kept: you stated it"
			case err != nil:
				suggestions[i].Status = err.Error()
			default:
				suggestions[i].Status = "proposed"
			}
		}
	}
	ask := []string{"salary floor, in " + cmp(c.Currency, cfg.Currency), "remote, hybrid or on-site", "roles and seniority"}
	if c.FreeMovement() {
		ask = append(ask, "work rights: EU/EEA citizens need no permit in "+c.Name+"; anyone else needs one")
	} else {
		ask = append(ask, "work rights: do you hold a permit or need sponsorship in "+c.Name)
	}
	return emit(out, *asJSON, map[string]any{"country": c, "known": known, "config": config, "written": write, "suggestions": suggestions, "ask": ask}, func() {
		fmt.Fprintf(out, "%s", c.Name)
		if known {
			fmt.Fprintf(out, " (%s): %s, %s, %s\n", c.Code, c.Currency, strings.Join(c.Languages, " and "), c.TimeZone)
			fmt.Fprintf(out, "CV: photo %s, %s\n", c.Photo, c.Pages)
		} else {
			fmt.Fprintln(out, ": no local boards or currency known; the remote boards and the location filter still apply")
		}
		verb := "would set"
		if write {
			verb = "set"
		}
		for _, k := range []string{"country", "currency", "search_boards"} {
			if v, ok := config[k]; ok && v != "" {
				fmt.Fprintf(out, "%s %s: %s\n", verb, k, v)
			}
		}
		for _, sg := range suggestions {
			status := "suggest"
			if sg.Status != "" {
				status = sg.Status
			}
			fmt.Fprintf(out, "%-8s %s = %s (%s)\n", status, sg.Key, sg.Value, sg.Why)
		}
		if write {
			fmt.Fprintln(out, "review them: scoutjobs pref review, or the Preferences page")
		} else {
			fmt.Fprintln(out, "apply: scoutjobs country set "+cmp(c.Code, name))
		}
		fmt.Fprintln(out, "ask the user: "+strings.Join(ask, "; "))
	})
}

// countrySuggestions are the preferences a country implies; each one waits for the user.
func countrySuggestions(c country.Country) []Suggestion {
	out := []Suggestion{{Key: "locations", Value: c.Name, Why: "you search from " + c.Name + "; remote jobs open to its region match too"}}
	if c.Code == "" {
		return out
	}
	out = append(out,
		Suggestion{Key: "languages", Value: strings.Join(c.Languages, ", "), Why: "postings in " + c.Name + " are written in these"},
		Suggestion{Key: "timezone", Value: c.TimeZone, Why: "remote teams overlap with " + c.Name + " working hours"},
		Suggestion{Key: "cv_norms", Value: "photo " + c.Photo + ", " + c.Pages, Why: "what employers in " + c.Name + " expect"})
	return out
}

func cmp(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func hasFlag(args []string, f string) bool { return slices.Contains(args, f) }

// setConfigValue replaces key's value in config.yaml, keeping its comment, or appends the key.
func setConfigValue(path, key, value string) error {
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(b) == 0 {
		lines = nil
	}
	for i, l := range lines {
		k, rest, ok := strings.Cut(l, ":")
		if !ok || strings.TrimSpace(k) != key || strings.HasPrefix(strings.TrimSpace(l), "#") {
			continue
		}
		line := key + ": " + value
		if j := strings.Index(rest, "#"); j >= 0 {
			line = fmt.Sprintf("%-23s %s", line, rest[j:])
		}
		lines[i] = line
		return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
	}
	return os.WriteFile(path, []byte(strings.Join(append(lines, key+": "+value), "\n")+"\n"), 0o600)
}
