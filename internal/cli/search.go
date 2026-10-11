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
	fs.Var(&boards, "board", "company board, repeatable: greenhouse:<slug> | lever:<slug> | ashby:<slug> | workable:<slug> | smartrecruiters:<company> | personio:<slug> | rss:<url>")
	var feeds listFlag
	fs.Var(&feeds, "rss", "RSS or Atom job feed url, repeatable (same as --board rss:<url>)")
	remote := fs.Bool("remote", cfg.SearchRemote, "query the remote boards too")
	limit := fs.Int("limit", 40, "max postings")
	add := fs.Bool("add", false, "add matches to the tracker as status new")
	asJSON := fs.Bool("json", false, "print JSON lines (description included)")
	compact := fs.Bool("compact", false, "with --json: drop descriptions")
	if err := fs.Parse(rest); err != nil {
		return err
	}
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
	r := search.Run(ctx, search.Options{Queries: queries, Boards: boards, Remote: *remote, Limit: *limit})
	for _, e := range r.Errors {
		fmt.Fprintln(os.Stderr, "warn:", e)
	}
	var added int
	type row struct {
		search.Posting
		ID      int64 `json:"id,omitempty"`
		Created bool  `json:"created,omitempty"`
	}
	rows := make([]row, 0, len(r.Postings))
	for _, p := range r.Postings {
		rw := row{Posting: p}
		if *add {
			j, created, err := s.UpsertJob(p.Job())
			if err != nil {
				return err
			}
			rw.ID, rw.Created = j.ID, created
			if created {
				added++
			}
		}
		rows = append(rows, rw)
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
		tbl = append(tbl, []string{id, rw.Source, trunc(rw.Company, 22), trunc(rw.Title, 44), rw.Remote, trunc(rw.Location, 20), rw.PostedAt})
	}
	table(out, "ID\tSOURCE\tCOMPANY\tTITLE\tREMOTE\tLOCATION\tPOSTED", tbl)
	fmt.Fprintf(out, "%d postings", len(rows))
	if *add {
		fmt.Fprintf(out, ", %d added (= already tracked)", added)
	}
	fmt.Fprintln(out)
	return nil
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
		}
		dst, size, err := apply.ExportPDF(dir, *name, "")
		if err != nil {
			return err
		}
		if jobID != 0 {
			_ = s.AddEvent(jobID, "cv:export", filepath.Base(dst))
		}
		return emit(out, *asJSON, map[string]any{"job": jobID, "file": dst, "bytes": size}, func() { fmt.Fprintf(out, "wrote %s (%d bytes)\n", dst, size) })
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
