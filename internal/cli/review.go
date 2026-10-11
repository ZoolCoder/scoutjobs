package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/zoolcoder/scoutjobs/internal/apply"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

// job fit: the evidence-backed fit review. The skill writes the JSON, the CLI validates it,
// stores it next to the drafts and copies score, notes and veto onto the job.
func runJobFit(s *store.Store, rest []string, out io.Writer) error {
	fs := flag.NewFlagSet("job fit", flag.ContinueOnError)
	file := fs.String("file", "", "review JSON file, - for stdin")
	raw := fs.String("review", "", "review JSON inline")
	asJSON := fs.Bool("json", false, "")
	pos, err := parse(fs, rest)
	if err != nil {
		return err
	}
	if len(pos) == 0 || (*file == "" && *raw == "") {
		return errors.New("usage: job fit <id> --file review.json | --review '{\"requirements\":[...],\"vetoes\":[...],\"score\":72}'")
	}
	j, err := s.ResolveJob(pos[0])
	if err != nil {
		return err
	}
	body := []byte(*raw)
	switch {
	case *file == "-":
		body, err = io.ReadAll(os.Stdin)
	case *file != "":
		body, err = os.ReadFile(*file)
	}
	if err != nil {
		return err
	}
	var r apply.FitReview
	if err := json.Unmarshal(body, &r); err != nil {
		return fmt.Errorf("review: %w", err)
	}
	path, err := apply.SaveReview(apply.Dir(s, j), r)
	if err != nil {
		return err
	}
	veto := r.VetoText()
	notes := r.Notes
	if notes == "" {
		notes = summarize(r)
	}
	if err := s.UpdateJob(j.ID, store.JobPatch{FitScore: &r.Score, FitNotes: &notes, Veto: &veto}); err != nil {
		return err
	}
	_ = s.AddEvent(j.ID, "fit:review", fmt.Sprintf("%d requirements, %d gaps, %d vetoes, score %d", len(r.Requirements), len(r.Gaps()), len(r.Vetoes), r.Score))
	return emit(out, *asJSON, map[string]any{"job": j.ID, "file": path, "score": r.Score, "veto": veto, "gaps": r.Gaps()}, func() {
		fmt.Fprintf(out, "#%d %s / %s: fit %d, %d requirements", j.ID, j.Company, j.Title, r.Score, len(r.Requirements))
		if veto != "" {
			fmt.Fprintf(out, "\nVETO: %s", veto)
		}
		for _, g := range r.Gaps() {
			fmt.Fprintf(out, "\ngap: %s", g)
		}
		fmt.Fprintf(out, "\nwrote %s\n", path)
	})
}

func summarize(r apply.FitReview) string {
	count := map[string]int{}
	for _, q := range r.Requirements {
		count[q.Strength]++
	}
	var parts []string
	for _, k := range apply.Strengths {
		if count[k] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", count[k], k))
		}
	}
	return strings.Join(parts, ", ")
}

// job review: the draft-reviewer's verdict, logged as an event the grounding gate reads.
func runJobReview(s *store.Store, rest []string, out io.Writer) error {
	fs := flag.NewFlagSet("job review", flag.ContinueOnError)
	verdict := fs.String("verdict", "", "send | fix")
	notes := fs.String("notes", "", "what was checked, or what must change")
	pos, err := parse(fs, rest)
	if err != nil {
		return err
	}
	if len(pos) == 0 || (*verdict != "send" && *verdict != "fix") {
		return errors.New("usage: job review <id> --verdict send|fix [--notes '...']")
	}
	j, err := s.ResolveJob(pos[0])
	if err != nil {
		return err
	}
	if err := s.AddEvent(j.ID, "review:"+*verdict, *notes); err != nil {
		return err
	}
	fmt.Fprintf(out, "review:%s logged for #%d\n", *verdict, j.ID)
	return nil
}

// job check: the quality gates. Exit code 1 while a failure remains.
func runJobCheck(s *store.Store, rest []string, out io.Writer) error {
	fs := flag.NewFlagSet("job check", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "")
	pos, err := parse(fs, rest)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return errors.New("usage: job check <id> [--json]")
	}
	j, err := s.ResolveJob(pos[0])
	if err != nil {
		return err
	}
	events, _ := s.Events(j.ID, 200)
	rep := apply.Check(s, j, events, apply.CheckOptions{})
	if err := emit(out, *asJSON, map[string]any{"job": j.ID, "ok": rep.OK(), "report": rep}, func() { printReport(out, j, rep) }); err != nil {
		return err
	}
	return unresolved(rep)
}

func printReport(out io.Writer, j store.Job, rep apply.Report) {
	fmt.Fprintf(out, "#%d %s / %s\n", j.ID, j.Company, j.Title)
	for _, g := range rep.Gates {
		fmt.Fprintf(out, "%-5s %-11s %s\n", strings.ToUpper(g.State), g.Name, g.Detail)
		for _, it := range g.Items {
			fmt.Fprintf(out, "      - %s\n", it)
		}
	}
}

func unresolved(rep apply.Report) error {
	if rep.OK() {
		return nil
	}
	u := rep.Unresolved()
	return fmt.Errorf("%d unresolved failure(s):\n  %s", len(u), strings.Join(u, "\n  "))
}
