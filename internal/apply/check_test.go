package apply

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/store"
)

func gate(r Report, name string) Gate {
	for _, g := range r.Gates {
		if g.Name == name {
			return g
		}
	}
	return Gate{}
}

func goodReview() FitReview {
	return FitReview{Score: 74, Requirements: []Requirement{
		{Requirement: "5+ years Go", Kind: "must", Evidence: "profile.md: Go since 2018", Strength: "direct", Confidence: "high", Wording: "six years of Go in production"},
		{Requirement: "Kubernetes", Kind: "nice", Strength: "gap"},
	}}
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

var filler = strings.Repeat("Shipped services in Go with Postgres behind them. ", 6)

func TestReviewValidateAndSave(t *testing.T) {
	s, j := testJob(t)
	dir := Dir(s, j)
	bad := goodReview()
	bad.Requirements[0].Strength = "strong"
	if _, err := SaveReview(dir, bad); err == nil || !strings.Contains(err.Error(), "strength") {
		t.Errorf("unknown strength must fail: %v", err)
	}
	bad = goodReview()
	bad.Score = 101
	if _, err := SaveReview(dir, bad); err == nil {
		t.Error("score over 100 must fail")
	}
	if _, err := SaveReview(dir, FitReview{Score: 50}); err == nil {
		t.Error("empty review must fail")
	}
	r := goodReview()
	r.Vetoes = []Veto{{Rule: "work permit", Reason: "EU only, no sponsorship"}}
	if _, err := SaveReview(dir, r); err != nil {
		t.Fatal(err)
	}
	back, err := LoadReview(dir)
	if err != nil || back.VetoText() != "work permit: EU only, no sponsorship" || len(back.Gaps()) != 0 {
		t.Errorf("round trip: %+v %v", back, err)
	}
	if _, err := LoadReview(t.TempDir()); !os.IsNotExist(err) {
		t.Errorf("missing review: %v", err)
	}
}

func TestCheckGates(t *testing.T) {
	s, j := testJob(t)
	dir := Dir(s, j)
	rep := Check(s, j, nil, CheckOptions{})
	if rep.OK() || gate(rep, "documents").State != Fail || gate(rep, "fit_review").State != Warn || gate(rep, "coverage").State != Skip {
		t.Errorf("empty folder: %+v", rep)
	}
	write(t, dir, "resume.md", "# Me\n\nSix years of Go in production.\n\n"+filler)
	write(t, dir, "cover-letter.md", "Dear team,\n\n"+filler)
	r := goodReview()
	r.Requirements = append(r.Requirements, Requirement{Requirement: "CISSP", Kind: "must", Strength: "gap"})
	if _, err := SaveReview(dir, r); err != nil {
		t.Fatal(err)
	}
	rep = Check(s, j, nil, CheckOptions{})
	if g := gate(rep, "coverage"); g.State != Fail || len(g.Items) != 1 || g.Items[0] != "CISSP" {
		t.Errorf("must-have gap: %+v", g)
	}
	if g := gate(rep, "grounding"); g.State != Fail || !strings.Contains(g.Detail, "no reviewer verdict") {
		t.Errorf("no review: %+v", g)
	}
	r = goodReview()
	r.Requirements[0].Wording = "ten years of Rust"
	SaveReview(dir, r)
	if g := gate(Check(s, j, nil, CheckOptions{}), "coverage"); g.State != Fail || !strings.Contains(g.Items[0], "ten years of Rust") {
		t.Errorf("wording absent: %+v", g)
	}
	SaveReview(dir, goodReview())
	if err := s.AddEvent(j.ID, "review:fix", "claim X not in profile"); err != nil {
		t.Fatal(err)
	}
	events, _ := s.Events(j.ID, 50)
	if g := gate(Check(s, j, events, CheckOptions{}), "grounding"); g.State != Fail || !strings.Contains(g.Detail, "claim X") {
		t.Errorf("review:fix: %+v", g)
	}
	if err := s.AddEvent(j.ID, "review:send", ""); err != nil {
		t.Fatal(err)
	}
	events, _ = s.Events(j.ID, 50)
	rep = Check(s, j, events, CheckOptions{})
	if !rep.OK() {
		t.Errorf("should pass: %v", rep.Unresolved())
	}
	for _, n := range []string{"documents", "fit_review", "coverage", "grounding", "duplicates", "filenames"} {
		if gate(rep, n).State != Pass {
			t.Errorf("%s: %+v", n, gate(rep, n))
		}
	}
	if gate(rep, "ats_text").State != Warn {
		t.Errorf("no pdf yet: %+v", gate(rep, "ats_text"))
	}
	// veto blocks even when the files are fine
	v := "no work permit"
	s.UpdateJob(j.ID, store.JobPatch{Veto: &v})
	j, _ = s.JobByID(j.ID)
	if g := gate(Check(s, j, events, CheckOptions{}), "fit_review"); g.State != Fail || !strings.Contains(g.Detail, "no work permit") {
		t.Errorf("veto: %+v", g)
	}
	v = ""
	s.UpdateJob(j.ID, store.JobPatch{Veto: &v})
	j, _ = s.JobByID(j.ID)
	// duplicates: repeated paragraph, and a cover letter reused elsewhere
	write(t, dir, "cover-letter.md", "Dear team,\n\n"+filler+"\n\n"+filler)
	if g := gate(Check(s, j, events, CheckOptions{}), "duplicates"); g.State != Fail || !strings.Contains(g.Items[0], "repeated paragraph") {
		t.Errorf("repeat: %+v", g)
	}
	write(t, dir, "cover-letter.md", "Dear team,\n\n"+filler)
	write(t, filepath.Join(s.Home, "applications", "other_job"), "cover-letter.md", "Dear  team,\n\n"+filler)
	if g := gate(Check(s, j, events, CheckOptions{}), "duplicates"); g.State != Fail || !strings.Contains(g.Items[0], "other_job") {
		t.Errorf("reused cover: %+v", g)
	}
	os.RemoveAll(filepath.Join(s.Home, "applications", "other_job"))
	// the drafts changed after the review
	later := time.Now().Add(5 * time.Second)
	os.Chtimes(filepath.Join(dir, "cover-letter.md"), later, later)
	if g := gate(Check(s, j, events, CheckOptions{}), "grounding"); g.State != Fail || !strings.Contains(g.Detail, "edited after") {
		t.Errorf("stale review: %+v", g)
	}
	// export mode skips the kit gates
	rep = Check(s, j, events, CheckOptions{Export: true})
	if gate(rep, "documents").Name != "" || gate(rep, "ats_text").Name != "" || gate(rep, "filenames").Name != "" {
		t.Errorf("export mode ran kit gates: %+v", rep)
	}
}

func TestCheckATSAndFilenames(t *testing.T) {
	s, j := testJob(t)
	dir := Dir(s, j)
	write(t, dir, "resume.pdf", "%PDF-1.4\n1 0 obj << /Type /XObject /Subtype /Image >> endobj\n")
	if g := gate(Check(s, j, nil, CheckOptions{}), "ats_text"); g.State != Fail || !strings.Contains(g.Detail, "no text layer") {
		t.Errorf("image pdf: %+v", g)
	}
	write(t, dir, "resume.pdf", "%PDF-1.4\n/Font /F1\nBT (hello) Tj ET\n/Encrypt 5 0 R\n")
	if g := gate(Check(s, j, nil, CheckOptions{}), "ats_text"); g.State != Fail || !strings.Contains(g.Detail, "encrypted") {
		t.Errorf("encrypted pdf: %+v", g)
	}
	write(t, dir, "resume.pdf", "%PDF-1.4\n/Font /F1\nBT (hello) Tj ET\n")
	if g := gate(Check(s, j, nil, CheckOptions{}), "ats_text"); g.State != Pass {
		t.Errorf("text pdf: %+v", g)
	}
	write(t, dir, "Resume final (1).pdf", "%PDF-1.4")
	if g := gate(Check(s, j, nil, CheckOptions{}), "filenames"); g.State != Warn || !strings.Contains(g.Detail, "Resume final (1).pdf") {
		t.Errorf("sloppy name: %+v", g)
	}
	write(t, dir, "lebenslauf-ä.pdf", "%PDF-1.4")
	write(t, dir, "photo.png", "x")
	g := gate(Check(s, j, nil, CheckOptions{}), "filenames")
	if g.State != Fail || len(g.Items) != 2 {
		t.Errorf("bad names: %+v", g)
	}
}

func TestFormatGateFollowsTheMainCV(t *testing.T) {
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "cv"), 0o755)
	main := "# Ada\nada@x.io\n\n## Summary\nGo.\n\n## Experience\n- a\n\n## Skills\nGo\n"
	_ = os.WriteFile(filepath.Join(home, "cv", "resume.md"), []byte(main), 0o600)
	if g := gateFormat(home, strings.Replace(main, "- a", "- reworded for Acme", 1)); g.State != Pass {
		t.Errorf("same layout: %+v", g)
	}
	g := gateFormat(home, "# Ada\n\n## Experience\n- a\n\n## Summary\nGo.\n\n## Projects\nx\n")
	if g.State != Warn || strings.Join(g.Items, "; ") != "missing section ## skills; section not in the main CV ## projects" {
		t.Errorf("changed layout: %+v", g)
	}
	if g := gateFormat(home, "# Ada\n\n## Experience\n- a\n\n## Summary\nGo.\n\n## Skills\nGo\n"); g.State != Warn || !strings.Contains(g.Items[0], "different order") {
		t.Errorf("reordered: %+v", g)
	}
}

func TestAcknowledgeGapsForAPortalApplication(t *testing.T) {
	s, j := testJob(t)
	dir := Dir(s, j)
	write(t, dir, "resume.md", "# Me\n\nSix years of Go in production.\n\n"+filler)
	write(t, dir, "cover-letter.md", "Dear team,\n\n"+filler)
	r := goodReview()
	r.Requirements = append(r.Requirements, Requirement{Requirement: "CISSP", Kind: "must", Strength: "gap"})
	if _, err := SaveReview(dir, r); err != nil {
		t.Fatal(err)
	}
	events, _ := s.Events(j.ID, 50)
	rep := Check(s, j, events, CheckOptions{})
	if got := rep.PendingGaps(events); len(got) != 1 || got[0] != "CISSP" {
		t.Fatalf("pending %v", got)
	}
	if _, err := Acknowledge(s, j, "grounding", "", nil); err == nil {
		t.Error("only coverage gaps can be acknowledged")
	}
	if _, err := Acknowledge(s, j, "coverage", "", nil); err == nil || !strings.Contains(err.Error(), "CISSP") {
		t.Errorf("each gap must be acknowledged: %v", err)
	}
	if _, err := Acknowledge(s, j, "coverage", "adjacent experience", []string{"CISSP"}); err != nil {
		t.Fatal(err)
	}
	events, _ = s.Events(j.ID, 50)
	if events[1].Kind != AckEvent || events[1].Note != "coverage: CISSP | adjacent experience" || events[0].Note != "new -> drafted" {
		t.Errorf("events %+v", events[:2])
	}
	if g := gate(Check(s, j, events, CheckOptions{}), "coverage"); g.State != Fail {
		t.Errorf("sending still sees the gap: %+v", g)
	}
	allowed := Check(s, j, events, CheckOptions{AllowAcknowledged: true})
	if g := gate(allowed, "coverage"); g.State != Acked || g.Items[0] != "CISSP" {
		t.Errorf("acknowledged: %+v", g)
	}
	if back, _ := LoadReview(dir); len(back.Gaps()) != 1 {
		t.Error("the review keeps its gap")
	}
	if got, _ := s.JobByID(j.ID); got.Status == "applied" {
		t.Error("only the user marks a job applied")
	}

	r.Requirements = append(r.Requirements, Requirement{Requirement: "PMP", Kind: "must", Strength: "gap"})
	SaveReview(dir, r)
	if g := gate(Check(s, j, events, CheckOptions{AllowAcknowledged: true}), "coverage"); g.State != Fail {
		t.Errorf("a new gap needs a new acknowledgement: %+v", g)
	}

	r.Vetoes = []Veto{{Rule: "work permit", Reason: "EU only"}}
	SaveReview(dir, r)
	if _, err := Acknowledge(s, j, "coverage", "", []string{"CISSP", "PMP"}); err == nil {
		t.Error("a veto is never acknowledged away")
	}
}
