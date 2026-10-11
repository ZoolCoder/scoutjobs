package apply

import (
	"archive/zip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const letterXML = `<?xml version="1.0"?><w:document xmlns:w="w"><w:body>` +
	`<w:p><w:r><w:rPr><w:b/></w:rPr><w:t>{{employer_name}}</w:t></w:r></w:p>` +
	`<w:p><w:r><w:t>{{recipient_name_or_team}}</w:t></w:r></w:p>` +
	`<w:p><w:r><w:t xml:space="preserve">Dear {{salutation}},</w:t></w:r></w:p>` +
	`<w:p><w:pPr><w:spacing w:after="240"/></w:pPr><w:r><w:t>{{body_paragraph_1}}</w:t></w:r></w:p>` +
	`<w:p><w:r><w:t>{{signoff}}</w:t></w:r></w:p><w:p><w:r><w:t>{{candidate_name}}</w:t></w:r></w:p>` +
	`<w:sectPr><w:pgSz w:w="11906" w:h="16838"/></w:sectPr></w:body></w:document>`

func wordTemplate(t *testing.T, home string) string {
	t.Helper()
	p := filepath.Join(home, "cv", "letter.docx")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for name, body := range map[string]string{"[Content_Types].xml": "<Types/>", "word/document.xml": letterXML, "word/styles.xml": "<w:styles/>"} {
		fw, _ := w.Create(name)
		fw.Write([]byte(body))
	}
	w.Close()
	f.Close()
	return p
}

// fakeOffice stands in for LibreOffice: it writes a PDF of $FAKE_PAGES pages for the letter and
// one page for anything else.
func fakeOffice(t *testing.T) {
	bin := t.TempDir()
	script := `#!/bin/sh
while [ $# -gt 1 ]; do [ "$1" = --outdir ] && out=$2; shift; done
name=${1##*/}; name=${name%.docx}; pages=1
[ "$name" = cover-letter ] && pages=${FAKE_PAGES:-1}
printf '%%PDF-1.4\n1 0 obj<</Type/Pages/Count %s>>endobj /Font BT\n' "$pages" > "$out/$name.pdf"
`
	p := filepath.Join(bin, "soffice")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SCOUTJOBS_SOFFICE", p)
	t.Setenv("PATH", bin) // no pdftotext: the text layer is checked by the docx tests
}

func slots(t *testing.T, dir string, v map[string]string) {
	t.Helper()
	b, _ := json.Marshal(v)
	write(t, dir, SlotsName, string(b))
}

func letter() map[string]string {
	return map[string]string{"employer_name": "Acme Corp", "recipient_name_or_team": "", "salutation": "Hiring Team",
		"body_paragraph_1": "I build payment systems in Go. " + filler, "signoff": "Kind regards", "candidate_name": "Jane Doe"}
}

func TestCoverLetterKeepsTheWordTemplate(t *testing.T) {
	s, j := testJob(t)
	dir := Dir(s, j)
	tmpl := wordTemplate(t, s.Home)
	t.Setenv("SCOUTJOBS_SOFFICE", "off")
	slots(t, dir, letter())
	rec, err := BuildCover(context.Background(), s, j, tmpl)
	if err != nil {
		t.Fatal(err)
	}
	if rec.State != CoverWarn || rec.Rendered || len(rec.Problems) > 0 {
		t.Fatalf("valid slots without LibreOffice: %+v", rec)
	}
	md, _ := os.ReadFile(filepath.Join(dir, "cover-letter.md"))
	if !strings.HasPrefix(string(md), "Acme Corp\n\nDear Hiring Team,\n\nI build payment systems") || strings.Contains(string(md), "{{") {
		t.Errorf("cover-letter.md carries the DOCX wording:\n%s", md)
	}
	if got := strings.Join(ReviewedFiles(dir), ","); got != "cover-letter.md" {
		t.Errorf("the built DOCX is the reviewed Markdown, not a file of its own: %s", got)
	}
	events, _ := s.Events(j.ID, 50)
	if g := gate(Check(s, j, events, CheckOptions{}), "cover_template"); g.State != Warn {
		t.Errorf("unrendered letter: %+v", g)
	}

	write(t, dir, "cover-letter.md", string(md)+"\nP.S. edited by hand\n")
	if g := gate(Check(s, j, events, CheckOptions{}), "cover_template"); g.State != Fail || !strings.Contains(g.Detail, "cover-letter.md") {
		t.Errorf("Markdown edited after the build must not be sent as if it matched the DOCX: %+v", g)
	}
}

func TestCoverSlotsAreGroundedAndChecked(t *testing.T) {
	s, j := testJob(t)
	dir := Dir(s, j)
	tmpl := wordTemplate(t, s.Home)
	t.Setenv("SCOUTJOBS_SOFFICE", "off")
	write(t, dir, "posting.md", "Go Engineer at Acme Corp. Questions to Dana Lee.")
	v := letter()
	v["employer_name"] = "Globex"
	v["recipient_name_or_team"] = "Mr. John Smith"
	v["signoff"] = ""
	v["closing_line"] = "x"
	slots(t, dir, v)
	rec, err := BuildCover(context.Background(), s, j, tmpl)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(rec.Problems, "\n")
	for _, want := range []string{"employer_name \"Globex\" is not Acme Corp", "recipient_name_or_team is not stated", "signoff is required", "closing_line is not a slot"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing problem %q in:\n%s", want, got)
		}
	}
	if rec.State != CoverFail {
		t.Errorf("state %s", rec.State)
	}
	v = letter()
	v["recipient_name_or_team"] = "Dana Lee"
	slots(t, dir, v)
	if rec, _ := BuildCover(context.Background(), s, j, tmpl); len(rec.Problems) > 0 {
		t.Errorf("a recipient the posting names is fine: %v", rec.Problems)
	}
}

func TestRenderedCoverLetterMustFitTheTemplatePages(t *testing.T) {
	s, j := testJob(t)
	dir := Dir(s, j)
	tmpl := wordTemplate(t, s.Home)
	fakeOffice(t)
	slots(t, dir, letter())
	t.Setenv("FAKE_PAGES", "2")
	rec, err := BuildCover(context.Background(), s, j, tmpl)
	if err != nil {
		t.Fatal(err)
	}
	if rec.State != CoverFail || rec.Pages != 2 || rec.TemplatePages != 1 || !strings.Contains(rec.Summary(), "2 pages, the template has 1") {
		t.Fatalf("overflow to a second page: %+v", rec)
	}
	t.Setenv("FAKE_PAGES", "1")
	rec, err = BuildCover(context.Background(), s, j, tmpl)
	if err != nil || rec.State != CoverPass || !rec.Rendered {
		t.Fatalf("one page: %+v %v", rec, err)
	}
	if ExportState(dir, "cover-letter") != ExportCurrent {
		t.Errorf("cover-letter.pdf is the rendered DOCX of the current Markdown: %q", ExportState(dir, "cover-letter"))
	}
	if changed := CoverChanges(dir); len(changed) > 0 {
		t.Errorf("fresh build: %v", changed)
	}
}
