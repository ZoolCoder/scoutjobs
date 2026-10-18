package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/zoolcoder/scoutjobs/internal/apply"
	"github.com/zoolcoder/scoutjobs/internal/docx"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

// runCVVerify checks an exported PDF the way an ATS reads it, without spending tokens on it.
func runCVVerify(s *store.Store, rest []string, out io.Writer) error {
	fs := flag.NewFlagSet("cv verify", flag.ContinueOnError)
	name := fs.String("file", "resume.pdf", "PDF in the job's folder, or in cv/ without a job")
	pages := fs.Int("pages", 2, "most pages allowed; 0 skips the check")
	var contains listFlag
	fs.Var(&contains, "contains", "phrase the text layer must hold, repeatable")
	asJSON := fs.Bool("json", false, "")
	pos, err := parse(fs, rest)
	if err != nil {
		return err
	}
	path := filepath.Join(s.Home, "cv", *name)
	switch {
	case len(pos) > 0 && strings.HasSuffix(strings.ToLower(pos[0]), ".pdf"):
		path = pos[0]
	case len(pos) > 0:
		j, err := s.ResolveJob(pos[0])
		if err != nil {
			return err
		}
		path = filepath.Join(apply.Dir(s, j), *name)
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("no PDF at %s: run scoutjobs cv export first", path)
	}
	checks, err := docx.Verify(context.Background(), path, docx.VerifyOptions{MaxPages: *pages, Contains: contains})
	if err != nil {
		return err
	}
	failed := 0
	for _, c := range checks {
		if !c.OK {
			failed++
		}
	}
	err = emit(out, *asJSON, map[string]any{"file": path, "ok": failed == 0, "checks": checks}, func() {
		for _, c := range checks {
			mark := "ok  "
			if !c.OK {
				mark = "FAIL"
			}
			fmt.Fprintf(out, "%s %s", mark, c.Name)
			if c.Note != "" {
				fmt.Fprintf(out, ": %s", c.Note)
			}
			fmt.Fprintln(out)
		}
	})
	if err == nil && failed > 0 {
		return errors.New(fmt.Sprint(failed, " of ", len(checks), " checks failed"))
	}
	return err
}
