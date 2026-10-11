package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/zoolcoder/scoutjobs/internal/linkedin"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

// runLinkedIn imports the LinkedIn data export: the profile part becomes linkedin/profile.md for the
// setup skill, the connections feed the referral finder. Re-importing replaces the previous import.
func runLinkedIn(s *store.Store, rest []string, out io.Writer) error {
	sc, rest := sub(rest)
	if sc != "import" {
		return errors.New("usage: linkedin import <export.zip|folder>")
	}
	fs := flag.NewFlagSet("linkedin import", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "")
	pos, err := parse(fs, rest)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return errors.New("usage: linkedin import <export.zip|folder>  (LinkedIn: Settings > Data privacy > Get a copy of your data)")
	}
	ex, err := linkedin.Read(pos[0])
	if err != nil {
		return err
	}
	dir := filepath.Join(s.Home, "linkedin")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	profile := filepath.Join(dir, "profile.md")
	if err := os.WriteFile(profile, []byte(ex.Markdown()), 0o600); err != nil {
		return err
	}
	if len(ex.Connections) > 0 {
		if err := s.ReplaceContacts("linkedin", ex.Connections); err != nil {
			return err
		}
	}
	_ = s.AddEvent(0, "linkedin:import", fmt.Sprintf("%d positions, %d skills, %d connections", len(ex.Positions), len(ex.Skills), len(ex.Connections)))
	res := map[string]any{"profile": profile, "positions": len(ex.Positions), "skills": len(ex.Skills), "connections": len(ex.Connections)}
	return emit(out, *asJSON, res, func() {
		fmt.Fprintf(out, "imported %d positions, %d skills, %d connections\nprofile: %s\n", len(ex.Positions), len(ex.Skills), len(ex.Connections), profile)
		fmt.Fprintln(out, "Next: run the scoutjobs-setup skill to fold the profile into profile.md; `scoutjobs contacts <job>` lists who you know at a company.")
	})
}

// runContacts is the referral finder: people in the imported network who work at the job's company.
func runContacts(s *store.Store, rest []string, out io.Writer) error {
	fs := flag.NewFlagSet("contacts", flag.ContinueOnError)
	company := fs.String("company", "", "company name instead of a job")
	asJSON := fs.Bool("json", false, "")
	pos, err := parse(fs, rest)
	if err != nil {
		return err
	}
	name := *company
	if name == "" {
		if len(pos) == 0 {
			return errors.New("usage: contacts <job> | contacts --company <name>")
		}
		j, err := s.ResolveJob(pos[0])
		if err != nil {
			return err
		}
		name = j.Company
	}
	list, err := s.ContactsAt(name)
	if err != nil {
		return err
	}
	if list == nil {
		list = []store.Contact{}
	}
	return emit(out, *asJSON, map[string]any{"company": name, "contacts": list}, func() {
		if len(list) == 0 {
			if s.ContactCount() == 0 {
				fmt.Fprintln(out, "no contacts imported yet: scoutjobs linkedin import <export.zip>")
			} else {
				fmt.Fprintf(out, "nobody in your network works at %s\n", name)
			}
			return
		}
		var rows [][]string
		for _, c := range list {
			rows = append(rows, []string{c.Name, c.Position, c.ConnectedOn, c.URL})
		}
		table(out, "NAME\tPOSITION\tCONNECTED\tURL", rows)
	})
}
