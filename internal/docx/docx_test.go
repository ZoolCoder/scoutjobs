package docx

import (
	"archive/zip"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	styles = `<?xml version="1.0"?><w:styles xmlns:w="w"><w:style w:styleId="Body"><w:rPr><w:rFonts w:ascii="Garamond"/><w:sz w:val="22"/></w:rPr></w:style></w:styles>`
	header = `<?xml version="1.0"?><w:hdr xmlns:w="w"><w:p><w:pPr><w:jc w:val="right"/></w:pPr><w:r><w:t>{{candidate_name}}</w:t></w:r></w:p></w:hdr>`
	// Word splits a placeholder over runs when it was typed in pieces: employer_name below.
	document = `<?xml version="1.0"?><w:document xmlns:w="w"><w:body>` +
		`<w:p><w:pPr><w:pStyle w:val="Body"/></w:pPr><w:r><w:rPr><w:b/></w:rPr><w:t>{{employ</w:t></w:r><w:r><w:t>er_name}}</w:t></w:r></w:p>` +
		`<w:p><w:r><w:t>{{recipient_name_or_team}}</w:t></w:r></w:p>` +
		`<w:p><w:r><w:t xml:space="preserve">Dear {{salutation}},</w:t></w:r></w:p>` +
		`<w:tbl><w:tr><w:tc><w:tcPr/><w:p><w:r><w:t>{{employer_address}}</w:t></w:r></w:p></w:tc></w:tr></w:tbl>` +
		`<w:p><w:pPr><w:spacing w:after="240"/></w:pPr><w:r><w:rPr><w:i/></w:rPr><w:t>{{body_paragraph_1}}</w:t></w:r></w:p>` +
		`<w:p><w:r><w:t>{{signoff}}</w:t></w:r></w:p>` +
		`<w:sectPr><w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="1134"/></w:sectPr></w:body></w:document>`
)

// writeDocx writes a minimal Word file from part name to content.
func writeDocx(t *testing.T, path string, parts map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for _, n := range []string{"[Content_Types].xml", "word/document.xml", "word/styles.xml", "word/header1.xml"} {
		fw, err := w.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		fw.Write([]byte(parts[n]))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

func template(t *testing.T) (string, map[string]string) {
	parts := map[string]string{"[Content_Types].xml": "<Types/>", "word/document.xml": document, "word/styles.xml": styles, "word/header1.xml": header}
	p := filepath.Join(t.TempDir(), "letter.docx")
	writeDocx(t, p, parts)
	return p, parts
}

func readPart(t *testing.T, path, name string) string {
	t.Helper()
	z, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	for _, f := range z.File {
		if f.Name == name {
			r, _ := f.Open()
			defer r.Close()
			var b strings.Builder
			buf := make([]byte, 4096)
			for {
				n, err := r.Read(buf)
				b.Write(buf[:n])
				if err != nil {
					break
				}
			}
			return b.String()
		}
	}
	t.Fatalf("no %s", name)
	return ""
}

func TestFillReplacesOnlyTheSlots(t *testing.T) {
	tmpl, parts := template(t)
	slots, err := Slots(tmpl)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(slots, ","); got != "employer_name,recipient_name_or_team,salutation,employer_address,body_paragraph_1,signoff,candidate_name" {
		t.Fatalf("slots in document order: %s", got)
	}
	out := filepath.Join(t.TempDir(), "cover-letter.docx")
	left, err := Fill(tmpl, out, map[string]string{
		"employer_name": "Acme & Co", "recipient_name_or_team": "", "salutation": "Hiring Team", "employer_address": "",
		"body_paragraph_1": "I build  payment\nsystems.", "signoff": "Kind regards", "candidate_name": "Jane Doe",
	})
	if err != nil || len(left) > 0 {
		t.Fatalf("fill: %v, left %v", err, left)
	}
	text, _ := Text(out)
	if got := strings.Join(text, "|"); got != "Acme & Co|Dear Hiring Team,|I build payment systems.|Kind regards" {
		t.Errorf("text: %s", got)
	}
	doc := readPart(t, out, "word/document.xml")
	if !strings.Contains(doc, `<w:rPr><w:b/></w:rPr><w:t xml:space="preserve">Acme &amp; Co</w:t>`) {
		t.Errorf("a split placeholder keeps the style of its first run:\n%s", doc)
	}
	if !strings.Contains(doc, `<w:tc><w:tcPr/><w:p><w:r><w:t xml:space="preserve"></w:t>`) {
		t.Errorf("an empty slot in a table cell keeps the cell's paragraph:\n%s", doc)
	}
	if strings.Count(doc, "<w:p>") != 5 {
		t.Errorf("the empty recipient line is removed, the rest kept:\n%s", doc)
	}
	if !strings.Contains(readPart(t, out, "word/header1.xml"), ">Jane Doe<") {
		t.Error("header slot not filled")
	}
	if readPart(t, out, "word/styles.xml") != parts["word/styles.xml"] {
		t.Error("styles changed")
	}
	if problems, err := Same(tmpl, out); err != nil || len(problems) > 0 {
		t.Errorf("filling slots keeps the design: %v %v", problems, err)
	}
}

func TestSameCatchesDesignDrift(t *testing.T) {
	tmpl, parts := template(t)
	for name, edit := range map[string]func(p map[string]string){
		"font":     func(p map[string]string) { p["word/styles.xml"] = strings.Replace(styles, "Garamond", "Arial", 1) },
		"emphasis": func(p map[string]string) { p["word/document.xml"] = strings.Replace(document, "<w:i/>", "", 1) },
		"spacing": func(p map[string]string) {
			p["word/document.xml"] = strings.Replace(document, `w:after="240"`, `w:after="0"`, 1)
		},
		"margins": func(p map[string]string) {
			p["word/document.xml"] = strings.Replace(document, `w:top="1134"`, `w:top="567"`, 1)
		},
		"alignment": func(p map[string]string) { p["word/header1.xml"] = strings.Replace(header, "right", "left", 1) },
		"paragraphs": func(p map[string]string) {
			p["word/document.xml"] = strings.Replace(document, "<w:sectPr>", "<w:p><w:r><w:t>PS</w:t></w:r></w:p><w:sectPr>", 1)
		},
	} {
		changed := maps.Clone(parts)
		edit(changed)
		out := filepath.Join(t.TempDir(), name+".docx")
		writeDocx(t, out, changed)
		if problems, _ := Same(tmpl, out); len(problems) == 0 {
			t.Errorf("%s drift not caught", name)
		}
	}
}

func TestPagesAndMissingWords(t *testing.T) {
	pdf := []byte("%PDF-1.4\n1 0 obj<</Type/Pages/Kids[3 0 R 4 0 R]/Count 2>>endobj 3 0 obj<</Type /Page>>endobj 4 0 obj<</Type /Page>>endobj")
	if n := Pages(pdf); n != 2 {
		t.Errorf("pages %d", n)
	}
	if n := Pages([]byte("<</Type /Page>> <</Type/Page>>")); n != 2 {
		t.Errorf("page objects %d", n)
	}
	if miss := Missing([]string{"Dear team, I build systems."}, "dear team i build"); strings.Join(miss, ",") != "systems" {
		t.Errorf("missing %v", miss)
	}
}
