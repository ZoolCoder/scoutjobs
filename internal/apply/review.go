package apply

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ReviewName is the evidence-backed fit review the fit-review skill writes through
// `scoutjobs job fit`. One row per material requirement, hard vetoes kept apart from the score.
const ReviewName = "fit-review.json"

// Strengths and Confidences are the only values a requirement row may carry.
var (
	Strengths   = []string{"direct", "adjacent", "partial", "gap"}
	Confidences = []string{"high", "medium", "low"}
	Kinds       = []string{"must", "nice"}
)

// Requirement maps one posting requirement to the candidate's evidence.
type Requirement struct {
	Requirement string `json:"requirement"`          // the posting's wording
	Kind        string `json:"kind"`                 // must | nice
	Evidence    string `json:"evidence,omitempty"`   // where in profile.md or the CV the proof is
	Strength    string `json:"strength"`             // direct | adjacent | partial | gap
	Confidence  string `json:"confidence,omitempty"` // high | medium | low, how sure the mapping is
	Wording     string `json:"wording,omitempty"`    // defensible CV wording; must appear in the drafts
	Risk        string `json:"risk,omitempty"`       // what a reviewer could challenge
}

// Veto is a hard eligibility failure. It never changes the score; it blocks the application.
type Veto struct {
	Rule   string `json:"rule"`
	Reason string `json:"reason"`
}

// FitReview is the file; Score and Notes are copied onto the job by `scoutjobs job fit`.
type FitReview struct {
	Requirements []Requirement `json:"requirements"`
	Vetoes       []Veto        `json:"vetoes,omitempty"`
	Score        int64         `json:"score"`
	Notes        string        `json:"notes,omitempty"`
}

// Validate rejects unknown enum values and empty requirements so the file stays machine-checkable.
func (r FitReview) Validate() error {
	if len(r.Requirements) == 0 {
		return fmt.Errorf("fit review: no requirements")
	}
	if r.Score < 0 || r.Score > 100 {
		return fmt.Errorf("fit review: score %d out of 0-100", r.Score)
	}
	for i, q := range r.Requirements {
		if strings.TrimSpace(q.Requirement) == "" {
			return fmt.Errorf("fit review: requirement %d has no text", i+1)
		}
		if !oneOf(q.Kind, Kinds) {
			return fmt.Errorf("fit review: %q: kind %q, want %s", q.Requirement, q.Kind, strings.Join(Kinds, "|"))
		}
		if !oneOf(q.Strength, Strengths) {
			return fmt.Errorf("fit review: %q: strength %q, want %s", q.Requirement, q.Strength, strings.Join(Strengths, "|"))
		}
		if q.Confidence != "" && !oneOf(q.Confidence, Confidences) {
			return fmt.Errorf("fit review: %q: confidence %q, want %s", q.Requirement, q.Confidence, strings.Join(Confidences, "|"))
		}
	}
	for i, v := range r.Vetoes {
		if strings.TrimSpace(v.Rule) == "" {
			return fmt.Errorf("fit review: veto %d has no rule", i+1)
		}
	}
	return nil
}

// VetoText is the one-line summary stored on the job; empty when nothing blocks.
func (r FitReview) VetoText() string {
	var parts []string
	for _, v := range r.Vetoes {
		if v.Reason != "" {
			parts = append(parts, v.Rule+": "+v.Reason)
		} else {
			parts = append(parts, v.Rule)
		}
	}
	return strings.Join(parts, "; ")
}

// Gaps lists the must-have requirements with no evidence.
func (r FitReview) Gaps() []string {
	var out []string
	for _, q := range r.Requirements {
		if q.Kind == "must" && q.Strength == "gap" {
			out = append(out, q.Requirement)
		}
	}
	return out
}

func oneOf(v string, set []string) bool {
	for _, s := range set {
		if v == s {
			return true
		}
	}
	return false
}

// SaveReview validates and writes fit-review.json into the application folder.
func SaveReview(dir string, r FitReview) (string, error) {
	if err := r.Validate(); err != nil {
		return "", err
	}
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	p := filepath.Join(dir, ReviewName)
	return p, os.WriteFile(p, raw, 0o600)
}

// LoadReview reads fit-review.json back; os.IsNotExist tells a missing review from a broken one.
func LoadReview(dir string) (FitReview, error) {
	raw, err := os.ReadFile(filepath.Join(dir, ReviewName))
	if err != nil {
		return FitReview{}, err
	}
	var r FitReview
	if err := json.Unmarshal(raw, &r); err != nil {
		return FitReview{}, fmt.Errorf("%s: %w", ReviewName, err)
	}
	return r, nil
}
