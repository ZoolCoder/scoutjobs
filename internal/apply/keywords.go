package apply

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// KeywordGap is a requirement the tailored CV does not speak to yet.
type KeywordGap struct {
	Requirement string
	Wording     string // the agreed CV wording from the fit review; empty when there is none
	Evidence    string // where the profile backs it; empty means it must not be claimed
	Source      string // "fit review" or "CV check"
}

// Addable reports whether the gap may go into the CV: it has agreed wording and evidence behind it.
func (g KeywordGap) Addable() bool { return g.Wording != "" && g.Evidence != "" }

// KeywordGaps lists what the tailored resume.md misses: fit-review wordings not in it, must-haves
// without evidence, and the gaps of the job's last CV check. It is empty until resume.md exists.
func KeywordGaps(dir, cvGaps string) []KeywordGap {
	raw, err := os.ReadFile(filepath.Join(dir, "resume.md"))
	if err != nil {
		return nil
	}
	resume := fold(string(raw))
	var out []KeywordGap
	if review, err := LoadReview(dir); err == nil {
		for _, q := range review.Requirements {
			switch {
			case q.Strength == "gap" && q.Kind == "must":
				out = append(out, KeywordGap{Requirement: q.Requirement, Source: "fit review"})
			case q.Strength != "gap" && q.Wording != "" && !strings.Contains(resume, fold(q.Wording)):
				out = append(out, KeywordGap{Requirement: q.Requirement, Wording: q.Wording, Evidence: q.Evidence, Source: "fit review"})
			}
		}
	}
	for _, g := range strings.FieldsFunc(cvGaps, func(r rune) bool { return r == ',' || r == ';' || r == '\n' }) {
		if g = strings.TrimSpace(g); g != "" {
			out = append(out, KeywordGap{Requirement: g, Source: "CV check"})
		}
	}
	return out
}

// KeywordTask is the extra tailor instruction that works the addable gaps into the CV; "" when none is.
func KeywordTask(gaps []KeywordGap) string {
	var items []string
	for _, g := range gaps {
		if g.Addable() {
			items = append(items, fmt.Sprintf("%q for %q (evidence: %s)", g.Wording, g.Requirement, g.Evidence))
		}
	}
	if len(items) == 0 {
		return ""
	}
	return "Keep the current resume.md and work in this agreed wording, each where its evidence belongs, word for word: " +
		strings.Join(items, "; ") + ". Add nothing else the profile does not back."
}
