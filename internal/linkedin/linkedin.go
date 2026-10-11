// Package linkedin reads the archive LinkedIn hands out under Settings > Data privacy > Get a copy
// of your data. Nothing is fetched from LinkedIn: the user downloads the archive and points
// scoutjobs at the zip or the unpacked folder.
package linkedin

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/store"
)

type Profile struct {
	FirstName, LastName, Headline, Summary, Industry, Location string
}

type Position struct {
	Company, Title, Description, Location, Start, End string
}

type Education struct {
	School, Degree, Start, End string
}

// Export is what scoutjobs keeps from the archive.
type Export struct {
	Profile     Profile
	Positions   []Position
	Education   []Education
	Skills      []string
	Connections []store.Contact
}

const maxCSV = 32 << 20

// Read opens a zip archive or an unpacked export folder.
func Read(src string) (Export, error) {
	info, err := os.Stat(src)
	if err != nil {
		return Export{}, err
	}
	var fsys fs.FS
	if info.IsDir() {
		fsys = os.DirFS(src)
	} else {
		zr, err := zip.OpenReader(src)
		if err != nil {
			return Export{}, fmt.Errorf("%s is neither a folder nor a zip: %w", src, err)
		}
		defer func() { _ = zr.Close() }()
		fsys = zr
	}
	return parse(fsys)
}

func parse(fsys fs.FS) (Export, error) {
	files := map[string]string{} // lower-case base name -> path inside the archive
	_ = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files[strings.ToLower(path.Base(p))] = p
		}
		return nil
	})
	var ex Export
	table := func(name string) []map[string]string {
		p, ok := files[name]
		if !ok {
			return nil
		}
		f, err := fsys.Open(p)
		if err != nil {
			return nil
		}
		defer func() { _ = f.Close() }()
		b, err := io.ReadAll(io.LimitReader(f, maxCSV))
		if err != nil {
			return nil
		}
		return rows(b)
	}
	for _, r := range table("profile.csv") {
		ex.Profile = Profile{r["first name"], r["last name"], r["headline"], r["summary"], r["industry"], r["geo location"]}
		break
	}
	for _, r := range table("positions.csv") {
		ex.Positions = append(ex.Positions, Position{r["company name"], r["title"], r["description"], r["location"], r["started on"], r["finished on"]})
	}
	for _, r := range table("education.csv") {
		ex.Education = append(ex.Education, Education{r["school name"], r["degree name"], r["start date"], r["end date"]})
	}
	for _, r := range table("skills.csv") {
		if n := strings.TrimSpace(r["name"]); n != "" {
			ex.Skills = append(ex.Skills, n)
		}
	}
	for _, r := range table("connections.csv") {
		name := strings.TrimSpace(r["first name"] + " " + r["last name"])
		if name == "" {
			continue
		}
		ex.Connections = append(ex.Connections, store.Contact{Source: "linkedin", Name: name, URL: r["url"], Email: r["email address"],
			Company: r["company"], Position: r["position"], ConnectedOn: isoDate(r["connected on"])})
	}
	if ex.Profile == (Profile{}) && len(ex.Positions) == 0 && len(ex.Connections) == 0 {
		return ex, errors.New("no Profile.csv, Positions.csv or Connections.csv found: is this a LinkedIn data export?")
	}
	return ex, nil
}

// rows reads a CSV into maps keyed by lower-case header. Connections.csv opens with a "Notes:"
// paragraph and a blank line before the header, so that preamble is cut first.
func rows(b []byte) []map[string]string {
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	if bytes.HasPrefix(b, []byte("Notes")) {
		b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
		if _, after, ok := bytes.Cut(b, []byte("\n\n")); ok {
			b = after
		}
	}
	r := csv.NewReader(bytes.NewReader(b))
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	var header []string
	var out []map[string]string
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}
		if header == nil {
			for _, h := range rec {
				header = append(header, strings.ToLower(strings.TrimSpace(h)))
			}
			continue
		}
		m := map[string]string{}
		for i, h := range header {
			if i < len(rec) {
				m[h] = strings.TrimSpace(rec[i])
			}
		}
		out = append(out, m)
	}
	return out
}

// isoDate turns LinkedIn's "15 Mar 2023" into 2023-03-15 and leaves anything else as it was.
func isoDate(s string) string {
	for _, layout := range []string{"02 Jan 2006", "2 Jan 2006", "2006-01-02"} {
		if t, err := time.Parse(layout, strings.TrimSpace(s)); err == nil {
			return t.Format("2006-01-02")
		}
	}
	return s
}

// Markdown renders the profile part for the scoutjobs-setup skill. Connections are not included.
func (ex Export) Markdown() string {
	var b strings.Builder
	p := ex.Profile
	b.WriteString("# LinkedIn profile (from the data export)\n\n")
	b.WriteString("Imported by `scoutjobs linkedin import`. Source material for profile.md; facts here are what the user wrote on LinkedIn.\n\n")
	if name := strings.TrimSpace(p.FirstName + " " + p.LastName); name != "" {
		fmt.Fprintf(&b, "- Name: %s\n", name)
	}
	if p.Headline != "" {
		fmt.Fprintf(&b, "- Headline: %s\n", p.Headline)
	}
	if p.Location != "" {
		fmt.Fprintf(&b, "- Location: %s\n", p.Location)
	}
	if p.Industry != "" {
		fmt.Fprintf(&b, "- Industry: %s\n", p.Industry)
	}
	if p.Summary != "" {
		fmt.Fprintf(&b, "\n## About\n\n%s\n", p.Summary)
	}
	if len(ex.Positions) > 0 {
		b.WriteString("\n## Experience\n")
		for _, x := range ex.Positions {
			end := x.End
			if end == "" {
				end = "present"
			}
			fmt.Fprintf(&b, "\n### %s, %s (%s to %s)\n", x.Title, x.Company, x.Start, end)
			if x.Location != "" {
				fmt.Fprintf(&b, "%s\n", x.Location)
			}
			if x.Description != "" {
				fmt.Fprintf(&b, "\n%s\n", x.Description)
			}
		}
	}
	if len(ex.Education) > 0 {
		b.WriteString("\n## Education\n\n")
		for _, e := range ex.Education {
			fmt.Fprintf(&b, "- %s", e.School)
			if e.Degree != "" {
				fmt.Fprintf(&b, ", %s", e.Degree)
			}
			if e.Start != "" || e.End != "" {
				fmt.Fprintf(&b, " (%s to %s)", e.Start, e.End)
			}
			b.WriteString("\n")
		}
	}
	if len(ex.Skills) > 0 {
		fmt.Fprintf(&b, "\n## Skills\n\n%s\n", strings.Join(ex.Skills, ", "))
	}
	return b.String()
}
