package apply

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProvenanceBindsResultsToTheFileBytes(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "resume.md", "# Jane Doe\n\n"+filler)
	write(t, dir, "cover-letter.md", "Dear team,\n\n"+filler)
	if _, known := ReviewChanges(dir); known {
		t.Fatal("no review recorded yet")
	}
	if err := StampReview(dir); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ExportPDF(dir, "resume.md", ""); err != nil {
		t.Fatal(err)
	}
	if got := ExportState(dir, "resume"); got != ExportCurrent {
		t.Fatalf("export state %q", got)
	}
	if got := strings.Join(ReviewedFiles(dir), ","); got != "resume.md,cover-letter.md" {
		t.Errorf("a rendered PDF is not a document of its own: %s", got)
	}
	if changed, known := ReviewChanges(dir); !known || len(changed) > 0 {
		t.Errorf("rendering the reviewed resume keeps the review: %v %v", changed, known)
	}
	if err := StampATS(dir, ""); err != nil {
		t.Fatal(err)
	}
	p := LoadProvenance(dir)
	if p.ATS == nil || p.ATS.Files["resume.pdf"] == "" || p.Review.Files["resume.md"] != hashFile(filepath.Join(dir, "resume.md")) {
		t.Fatalf("hashes not recorded: %+v", p)
	}
	if st, _ := os.Stat(filepath.Join(dir, ProvenanceName)); st.Mode().Perm() != 0o600 {
		t.Errorf("provenance mode %v", st.Mode().Perm())
	}

	// the user puts the final CV in place of the rendered one
	write(t, dir, "resume.pdf", "%PDF-1.7 the styled final CV")
	if got := ExportState(dir, "resume"); got != ExportOwn {
		t.Errorf("replaced PDF: %q", got)
	}
	if changed, _ := ReviewChanges(dir); strings.Join(changed, ",") != "resume.pdf" {
		t.Errorf("the final PDF was never reviewed: %v", changed)
	}
	if changed, _ := ATSChanges(dir); strings.Join(changed, ",") != "resume.pdf" {
		t.Errorf("the score covered the rendered PDF, not the final one: %v", changed)
	}
	write(t, dir, "cover-letter.md", "Dear team, from the template.\n\n"+filler)
	if changed, _ := ReviewChanges(dir); strings.Join(changed, ",") != "cover-letter.md,resume.pdf" {
		t.Errorf("edited cover letter: %v", changed)
	}
	st := ProvenanceOf(dir)
	if st.ResumePDF != ExportOwn || len(st.ReviewChanged) != 2 || len(st.ATSChanged) != 1 {
		t.Errorf("status: %+v", st)
	}
}

func TestGroundingFollowsTheBytesNotTheClock(t *testing.T) {
	s, j := testJob(t)
	dir := Dir(s, j)
	write(t, dir, "resume.md", "# Jane Doe\n\n"+filler)
	write(t, dir, "cover-letter.md", "Dear team,\n\n"+filler)
	_ = s.AddEvent(j.ID, "review:send", "checked")
	if err := StampReview(dir); err != nil {
		t.Fatal(err)
	}
	events, _ := s.Events(j.ID, 50)
	if g := gate(Check(s, j, events, CheckOptions{}), "grounding"); g.State != Pass {
		t.Fatalf("reviewed drafts: %+v", g)
	}
	write(t, dir, "resume.pdf", "%PDF-1.7 the styled final CV")
	if g := gate(Check(s, j, events, CheckOptions{}), "grounding"); g.State != Fail || !strings.Contains(g.Detail, "resume.pdf") {
		t.Errorf("an unreviewed final PDF must block sending: %+v", g)
	}
}
