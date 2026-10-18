package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/apply"
	"github.com/zoolcoder/scoutjobs/internal/country"
	"github.com/zoolcoder/scoutjobs/internal/match"
	"github.com/zoolcoder/scoutjobs/internal/search"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

func runSearch(s *store.Store, cfg Config, rest []string, out io.Writer) error {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	var queries, boards listFlag
	fs.Var(&queries, "query", "keyword query, repeatable (all words must match; any query may)")
	fs.Var(&boards, "board", "company board, repeatable: source:<slug> (greenhouse, lever, ashby, workable, smartrecruiters, personio, recruitee, bamboohr), rss:<url>, the careers page URL, a Danish board with an area: jobindex:dk, jobbank:aarhus, jobdanmark:odense, or the German Arbeitsagentur: arbeitsagentur:de, arbeitsagentur:berlin")
	var feeds listFlag
	fs.Var(&feeds, "rss", "RSS or Atom job feed url, repeatable (same as --board rss:<url>)")
	remote := fs.Bool("remote", cfg.SearchRemote, "query the remote boards too")
	limit := fs.Int("limit", 40, "max postings")
	add := fs.Bool("add", false, "add matches to the tracker as status new")
	asJSON := fs.Bool("json", false, "print JSON lines (description included)")
	compact := fs.Bool("compact", false, "with --json: drop descriptions")
	location := fs.String("location", "", "keep jobs in, or remote and open to, these comma separated cities, countries or regions (default: the profile's locations, else the country in config.yaml; any searches everywhere)")
	rank := fs.Bool("rank", true, "score each posting against profile.md and the preferences, best fit first")
	hide := fs.Bool("hide-mismatches", false, "drop postings that break a must in the profile: place, engagement, salary floor")
	suggest := fs.Bool("suggest", false, "print searches built from the profile and exit")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	pr := match.Load(s)
	if *suggest {
		sg := pr.Suggest()
		return emit(out, *asJSON, sg, func() {
			for _, x := range sg {
				fmt.Fprintf(out, "%s: scoutjobs search", x.Name)
				for _, q := range strings.Split(x.Queries, ", ") {
					fmt.Fprintf(out, " --query %q", q)
				}
				if x.Location != "" {
					fmt.Fprintf(out, " --location %q", x.Location)
				}
				if x.RemoteOnly {
					fmt.Fprint(out, " --remote")
				}
				fmt.Fprintln(out)
			}
			if len(sg) == 0 {
				fmt.Fprintln(out, "fill in Roles and Skills in profile.md to get suggestions")
			}
		})
	}
	wants := splitList(*location)
	switch {
	case strings.EqualFold(*location, "any"):
		wants = nil
	case len(wants) == 0:
		wants = focus(pr, cfg)
	}
	if len(wants) > 0 {
		pr.Places = wants // the place searched for is the place to score against
	}
	ranked := *rank && !pr.Empty()
	if len(queries) == 0 {
		queries = cfg.SearchQueries
	}
	if len(boards) == 0 {
		boards = cfg.SearchBoards
	}
	for _, u := range feeds {
		boards = append(boards, "rss:"+u)
	}
	if len(boards) == 0 && !*remote {
		return errors.New("nothing to search: give --board or --remote (or set search_boards / search_remote in config.yaml)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	opts := search.Options{Queries: queries, Boards: boards, Remote: *remote, Off: splitList(s.Setting(search.OffSetting)), Limit: *limit}
	var dropped int
	opts.Keep = func(p search.Posting) bool {
		ok, _ := search.LocationMatch(p, wants)
		if ok && *hide && ranked {
			ok = len(pr.Score(p).Hard) == 0
		}
		if !ok {
			dropped++
		}
		return ok
	}
	switch {
	case ranked:
		opts.Rank = func(p search.Posting) int { return rankKey(pr.Score(p)) }
	case len(wants) > 0: // no profile to score by: a known fit before an unknown or unstated place
		opts.Rank = func(p search.Posting) int {
			if _, note := search.LocationMatch(p, wants); note != "" {
				return 0
			}
			return 1
		}
	}
	r := search.Run(ctx, opts)
	for _, e := range r.Errors {
		fmt.Fprintln(os.Stderr, "warn:", e)
	}
	var added int
	type row struct {
		search.Posting
		Match   *match.Result `json:"match,omitempty"`
		ID      int64         `json:"id,omitempty"`
		Created bool          `json:"created,omitempty"`
	}
	rows := make([]row, 0, len(r.Postings))
	for _, p := range r.Postings {
		rw := row{Posting: p}
		if ranked {
			m := pr.Score(p)
			rw.Match = &m
		}
		if *add {
			j, created, err := s.UpsertJob(p.Job())
			if err != nil {
				return err
			}
			if err := apply.SavePosting(s, j, p.Description); err != nil {
				return err
			}
			rw.ID, rw.Created = j.ID, created
			if created {
				added++
			}
		}
		rows = append(rows, rw)
	}
	if added > 0 {
		// new jobs land ranked by the same local score, so no agent has to triage them
		if _, err := apply.Rank(s, false); err != nil {
			return err
		}
	}
	if *asJSON {
		enc := json.NewEncoder(out)
		for _, rw := range rows {
			if *compact {
				rw.Description = ""
			}
			if err := enc.Encode(rw); err != nil {
				return err
			}
		}
		return nil
	}
	var tbl [][]string
	for _, rw := range rows {
		id := "-"
		if rw.ID > 0 {
			id = fmt.Sprint(rw.ID)
			if !rw.Created {
				id += "="
			}
		}
		fitCol := "-"
		if rw.Match != nil {
			fitCol = fmt.Sprint(rw.Match.Score)
			if len(rw.Match.Hard) > 0 {
				fitCol += "!"
			}
		}
		tbl = append(tbl, []string{id, fitCol, rw.Source, trunc(rw.Company, 22), trunc(rw.Title, 44), rw.Remote, trunc(rw.Location, 20), rw.PostedAt})
	}
	table(out, "ID\tFIT\tSOURCE\tCOMPANY\tTITLE\tREMOTE\tLOCATION\tPOSTED", tbl)
	fmt.Fprintf(out, "%d postings", len(rows))
	if len(wants) > 0 {
		fmt.Fprintf(out, ", focus %s", strings.Join(wants, ", "))
	}
	if dropped > 0 {
		fmt.Fprintf(out, ", %d dropped by the focus or --hide-mismatches", dropped)
	}
	if ranked {
		fmt.Fprint(out, ", best fit first (! breaks a must)")
	}
	if *add {
		fmt.Fprintf(out, ", %d added (= already tracked)", added)
	}
	fmt.Fprintln(out)
	if len(wants) > 0 && *location == "" {
		fmt.Fprintln(out, "search everywhere: --location any; another place: --location <city, country or region>")
	}
	return nil
}

// focus is where a search looks when no place is given: the profile's locations, else the
// country in config.yaml, so results stay in one country and the remote jobs open to its region.
func focus(pr match.Profile, cfg Config) []string {
	if len(pr.Places) > 0 {
		return pr.Places
	}
	if c, ok := country.Lookup(cfg.Country); ok {
		return []string{c.Name}
	}
	return splitList(cfg.Country)
}

// runImport reads JSON lines (or a JSON array) of postings from a file or stdin and upserts them.
func runImport(s *store.Store, rest []string, out io.Writer) error {
	fs := flag.NewFlagSet("job import", flag.ContinueOnError)
	file := fs.String("file", "-", "json lines or json array; - for stdin")
	asJSON := fs.Bool("json", false, "")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	in := io.Reader(os.Stdin)
	if *file != "-" {
		f, err := os.Open(*file)
		if err != nil {
			return err
		}
		defer f.Close()
		in = f
	}
	data, err := io.ReadAll(in)
	if err != nil {
		return err
	}
	var postings []search.Posting
	if t := strings.TrimSpace(string(data)); strings.HasPrefix(t, "[") {
		if err := json.Unmarshal([]byte(t), &postings); err != nil {
			return err
		}
	} else {
		sc := bufio.NewScanner(strings.NewReader(t))
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			var p search.Posting
			if err := json.Unmarshal([]byte(line), &p); err != nil {
				return fmt.Errorf("bad line %q: %w", trunc(line, 60), err)
			}
			postings = append(postings, p)
		}
	}
	var created, existed int
	var ids []int64
	for _, p := range postings {
		if p.Company == "" || p.Title == "" {
			continue
		}
		j, c, err := s.UpsertJob(p.Job())
		if err != nil {
			return err
		}
		if err := apply.SavePosting(s, j, p.Description); err != nil {
			return err
		}
		ids = append(ids, j.ID)
		if c {
			created++
		} else {
			existed++
		}
	}
	return emit(out, *asJSON, map[string]any{"created": created, "existing": existed, "ids": ids}, func() {
		fmt.Fprintf(out, "imported %d new, %d already tracked\n", created, existed)
	})
}

// ---- cv ----

func runCV(s *store.Store, rest []string, out io.Writer) error {
	sc, rest := sub(rest)
	switch sc {
	case "export":
		fs := flag.NewFlagSet("cv export", flag.ContinueOnError)
		name := fs.String("file", "resume.md", "markdown file to render; the PDF lands next to it")
		fs.Bool("skip-checks", false, "kept for older scripts: failed gates no longer stop an export, only sending")
		asJSON := fs.Bool("json", false, "")
		pos, err := parse(fs, rest)
		if err != nil {
			return err
		}
		dir := filepath.Join(s.Home, "cv")
		var jobID int64
		if len(pos) > 0 {
			j, err := s.ResolveJob(pos[0])
			if err != nil {
				return err
			}
			dir, jobID = apply.Dir(s, j), j.ID
			events, _ := s.Events(j.ID, 200)
			// a local PDF is preparation, not sending: gates are reported here and enforced by email send
			if rep := apply.Check(s, j, events, apply.CheckOptions{Export: true}); !rep.OK() && !*asJSON {
				fmt.Fprintf(out, "exporting anyway; sending stays blocked until these pass: %v\n", unresolved(rep))
			}
		}
		dst, size, err := apply.ExportPDF(dir, *name, "")
		if err != nil {
			return err
		}
		if jobID != 0 {
			_ = s.AddEvent(jobID, "cv:export", filepath.Base(dst))
		}
		return emit(out, *asJSON, map[string]any{"job": jobID, "file": dst, "bytes": size}, func() { fmt.Fprintf(out, "wrote %s (%d bytes)\n", dst, size) })
	case "verify":
		return runCVVerify(s, rest, out)
	case "cover":
		fs := flag.NewFlagSet("cv cover", flag.ContinueOnError)
		tmpl := fs.String("template", "", "Word template with {{slot}} placeholders (default: cover_template in config.yaml)")
		asJSON := fs.Bool("json", false, "")
		pos, err := parse(fs, rest)
		if err != nil {
			return err
		}
		if len(pos) == 0 {
			return errors.New("usage: scoutjobs cv cover <job> [--template file.docx]")
		}
		j, err := s.ResolveJob(pos[0])
		if err != nil {
			return err
		}
		path := *tmpl
		if path == "" {
			path = LoadConfig(s.Home).CoverTemplate
		}
		if !apply.IsWordTemplate(path) {
			return apply.ErrNoWordTemplate
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(s.Home, path)
		}
		rec, err := apply.BuildCover(context.Background(), s, j, path)
		if err != nil {
			return err
		}
		err = emit(out, *asJSON, rec, func() {
			fmt.Fprintf(out, "%s: cover-letter.docx, cover-letter.md", rec.State)
			if rec.Rendered {
				fmt.Fprintf(out, ", cover-letter.pdf (%d of %d pages)", rec.Pages, rec.TemplatePages)
			}
			fmt.Fprintln(out)
			for _, p := range rec.Problems {
				fmt.Fprintln(out, "  - "+p)
			}
			if !rec.Rendered {
				fmt.Fprintln(out, "  not rendered: install LibreOffice (or set SCOUTJOBS_SOFFICE) to make cover-letter.pdf and check the page count")
			}
		})
		if err == nil && rec.State == apply.CoverFail {
			return fmt.Errorf("the cover letter does not fit its template: fix %s and run again", apply.SlotsName)
		}
		return err
	case "files":
		fs := flag.NewFlagSet("cv files", flag.ContinueOnError)
		asJSON := fs.Bool("json", false, "")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		files, dir := cvFiles(s.Home)
		return emit(out, *asJSON, map[string]any{"dir": dir, "files": files}, func() {
			fmt.Fprintln(out, "dir:", dir)
			for _, f := range files {
				fmt.Fprintln(out, " ", f)
			}
			if len(files) == 0 {
				fmt.Fprintln(out, "  (empty: put resume.pdf / resume.md here)")
			}
		})
	case "log":
		fs := flag.NewFlagSet("cv log", flag.ContinueOnError)
		var c store.CVCheck
		fs.StringVar(&c.File, "file", "", "file name inside cv/ (required)")
		fs.Int64Var(&c.Score, "score", -1, "overall quality 0-100")
		fs.Int64Var(&c.ATSScore, "ats", -1, "ATS parse/keyword score 0-100")
		fs.StringVar(&c.Summary, "summary", "", "one line verdict")
		fs.StringVar(&c.Gaps, "gaps", "", "comma separated fixes to make")
		job := fs.String("job", "", "job id when checked against a posting")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if c.File == "" {
			return errors.New("--file required")
		}
		if *job != "" {
			j, err := s.ResolveJob(*job)
			if err != nil {
				return err
			}
			c.JobID = j.ID
		}
		id, err := s.AddCVCheck(c)
		if err != nil {
			return err
		}
		if c.JobID != 0 {
			if err := stampATS(s, c); err != nil {
				return err
			}
		}
		fmt.Fprintf(out, "logged cv check #%d %s score %d ats %d\n", id, c.File, c.Score, c.ATSScore)
		return nil
	case "list":
		fs := flag.NewFlagSet("cv list", flag.ContinueOnError)
		asJSON := fs.Bool("json", false, "")
		limit := fs.Int("limit", 20, "")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		cs, err := s.CVChecks(*limit)
		if err != nil {
			return err
		}
		return emit(out, *asJSON, cs, func() {
			var rows [][]string
			for _, c := range cs {
				rows = append(rows, []string{c.At[:10], c.File, fit(c.Score), fit(c.ATSScore), trunc(c.Summary, 50), trunc(c.Gaps, 40)})
			}
			table(out, "DATE\tFILE\tSCORE\tATS\tSUMMARY\tGAPS", rows)
		})
	}
	return errors.New("usage: cv files|log|list|export [job]")
}

func cvFiles(home string) ([]string, string) {
	dir := home + string(os.PathSeparator) + "cv"
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, dir
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && !strings.HasPrefix(e.Name(), ".") && e.Name() != "README.md" {
			files = append(files, e.Name())
		}
	}
	return files, dir
}

// stampATS binds a CV check logged for a job to the bytes of the file it scored.
func stampATS(s *store.Store, c store.CVCheck) error {
	j, err := s.JobByID(c.JobID)
	if err != nil {
		return err
	}
	return apply.StampATS(apply.Dir(s, j), filepath.Base(c.File))
}

// rankKey sorts a posting that breaks a must after every one that does not, whatever its score:
// a strong match in the wrong country must not push the local jobs out of the limit.
func rankKey(m match.Result) int {
	if len(m.Hard) > 0 {
		return m.Score - 1000
	}
	return m.Score
}
