// Package docx fills a Word template's {{slot}} placeholders without touching its design, checks
// that a filled copy kept the template's structure, and renders it to PDF with LibreOffice.
package docx

import (
	"archive/zip"
	"bytes"
	"fmt"
	"html"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	placeholder = regexp.MustCompile(`\{\{\s*([a-z][a-z0-9_]*)\s*\}\}`)
	textRun     = regexp.MustCompile(`<w:t(?:\s[^>]*)?>([^<]*)</w:t>`)
	paraTag     = regexp.MustCompile(`<w:p(?:\s[^>]*)?/>|<w:p(?:\s[^>]*)?>|</w:p>`)
	escaper     = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
)

// textPart is true for the parts that carry the letter's words: body, headers and footers.
func textPart(name string) bool {
	if name == "word/document.xml" {
		return true
	}
	base := strings.TrimPrefix(name, "word/")
	return base != name && !strings.Contains(base, "/") && strings.HasSuffix(base, ".xml") &&
		(strings.HasPrefix(base, "header") || strings.HasPrefix(base, "footer"))
}

// span is a leaf paragraph: one that holds no other paragraph, such as the paragraphs of a text box.
type span struct{ start, end int }

func leafParagraphs(xml string) []span {
	var out []span
	type open struct {
		start  int
		nested bool
	}
	var stack []open
	for _, m := range paraTag.FindAllStringIndex(xml, -1) {
		tag := xml[m[0]:m[1]]
		switch {
		case strings.HasSuffix(tag, "/>"):
		case tag == "</w:p>":
			if len(stack) == 0 {
				continue
			}
			top := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if !top.nested {
				out = append(out, span{top.start, m[1]})
			}
			if len(stack) > 0 {
				stack[len(stack)-1].nested = true
			}
		default:
			stack = append(stack, open{start: m[0]})
		}
	}
	return out
}

// paraText is the visible text of a paragraph's runs.
func paraText(p string) string {
	var b strings.Builder
	for _, m := range textRun.FindAllStringSubmatch(p, -1) {
		b.WriteString(html.UnescapeString(m[1]))
	}
	return b.String()
}

// fillParagraph replaces the placeholders of one paragraph. Each value goes into the run where its
// placeholder starts, so it keeps that run's formatting even when Word split the placeholder.
func fillParagraph(p string, values map[string]string) (out string, empty bool) {
	runs := textRun.FindAllStringSubmatchIndex(p, -1)
	texts := make([]string, len(runs))
	bounds := make([]int, len(runs)+1)
	var whole strings.Builder
	for i, r := range runs {
		texts[i] = html.UnescapeString(p[r[2]:r[3]])
		whole.WriteString(texts[i])
		bounds[i+1] = whole.Len()
	}
	full := whole.String()
	matches := placeholder.FindAllStringSubmatchIndex(full, -1)
	var filled []int
	for _, m := range matches {
		if _, ok := values[full[m[2]:m[3]]]; ok {
			filled = append(filled, m[0], m[1], m[2], m[3])
		}
	}
	if len(filled) == 0 {
		return p, false
	}
	runAt := func(pos int) int {
		for i := range runs {
			if pos < bounds[i+1] {
				return i
			}
		}
		return len(runs) - 1
	}
	next := make([]strings.Builder, len(runs))
	copyRange := func(a, b int) {
		for i := range runs {
			lo, hi := max(a, bounds[i]), min(b, bounds[i+1])
			if lo < hi {
				next[i].WriteString(full[lo:hi])
			}
		}
	}
	pos := 0
	for k := 0; k < len(filled); k += 4 {
		copyRange(pos, filled[k])
		next[runAt(filled[k])].WriteString(oneLine(values[full[filled[k+2]:filled[k+3]]]))
		pos = filled[k+1]
	}
	copyRange(pos, len(full))

	var b strings.Builder
	last, visible := 0, ""
	for i, r := range runs {
		t := next[i].String()
		visible += t
		if t == texts[i] {
			continue
		}
		b.WriteString(p[last:r[0]])
		b.WriteString(`<w:t xml:space="preserve">`)
		b.WriteString(escaper.Replace(t))
		b.WriteString(`</w:t>`)
		last = r[1]
	}
	b.WriteString(p[last:])
	onlySlots := strings.TrimSpace(placeholder.ReplaceAllString(full, "")) == ""
	return b.String(), onlySlots && strings.TrimSpace(visible) == ""
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// inCell is true when the paragraph is the only one of a table cell, which Word needs to keep.
func inCell(xml string, s span) bool {
	before := xml[:s.start]
	cell := max(strings.LastIndex(before, "<w:tc>"), strings.LastIndex(before, "<w:tc "))
	if cell < 0 {
		return false
	}
	rest := before[cell:]
	first := !strings.Contains(rest, "<w:p") && !strings.Contains(rest, "</w:tc>")
	return first && strings.HasPrefix(strings.TrimSpace(xml[s.end:]), "</w:tc>")
}

// fillPart fills every leaf paragraph of a part; a paragraph that held only slots left empty is
// removed, so an optional line leaves no gap.
func fillPart(xml string, values map[string]string) (string, int) {
	var b strings.Builder
	last, removed := 0, 0
	for _, s := range leafParagraphs(xml) {
		p, empty := fillParagraph(xml[s.start:s.end], values)
		b.WriteString(xml[last:s.start])
		if empty && !inCell(xml, s) {
			removed++
		} else {
			b.WriteString(p)
		}
		last = s.end
	}
	b.WriteString(xml[last:])
	return b.String(), removed
}

type part struct {
	f    *zip.File
	data []byte
}

func readParts(path string) (*zip.ReadCloser, []part, error) {
	z, err := zip.OpenReader(path)
	if err != nil {
		return nil, nil, fmt.Errorf("%s is not a Word file: %w", filepath.Base(path), err)
	}
	var parts []part
	for _, f := range z.File {
		r, err := f.Open()
		if err != nil {
			_ = z.Close()
			return nil, nil, err
		}
		b, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			_ = z.Close()
			return nil, nil, err
		}
		parts = append(parts, part{f, b})
	}
	if !hasPart(parts, "word/document.xml") {
		_ = z.Close()
		return nil, nil, fmt.Errorf("%s has no word/document.xml: not a Word document", filepath.Base(path))
	}
	return z, parts, nil
}

func hasPart(parts []part, name string) bool {
	for _, p := range parts {
		if p.f.Name == name {
			return true
		}
	}
	return false
}

// Slots lists the template's placeholders in document order, once each.
func Slots(path string) ([]string, error) {
	z, parts, err := readParts(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = z.Close() }()
	return slotsOf(parts), nil
}

func slotsOf(parts []part) []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range parts {
		if !textPart(p.f.Name) {
			continue
		}
		for _, s := range leafParagraphs(string(p.data)) {
			for _, m := range placeholder.FindAllStringSubmatch(paraText(string(p.data)[s.start:s.end]), -1) {
				if !seen[m[1]] {
					seen[m[1]] = true
					out = append(out, m[1])
				}
			}
		}
	}
	return out
}

// Fill writes dst: the template with each slot replaced by its value. Every other part is copied
// byte for byte. left lists placeholders still in the result.
func Fill(tmpl, dst string, values map[string]string) (left []string, err error) {
	z, parts, err := readParts(tmpl)
	if err != nil {
		return nil, err
	}
	defer func() { _ = z.Close() }()
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".fill-*.docx")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	w := zip.NewWriter(tmp)
	var out []part
	for _, p := range parts {
		if !textPart(p.f.Name) {
			raw, err := p.f.OpenRaw()
			if err == nil {
				var fw io.Writer
				if fw, err = w.CreateRaw(&p.f.FileHeader); err == nil {
					_, err = io.Copy(fw, raw)
				}
			}
			if err != nil {
				tmp.Close()
				return nil, err
			}
			out = append(out, p)
			continue
		}
		xml, _ := fillPart(string(p.data), values)
		h := p.f.FileHeader
		fw, err := w.CreateHeader(&h)
		if err == nil {
			_, err = fw.Write([]byte(xml))
		}
		if err != nil {
			tmp.Close()
			return nil, err
		}
		out = append(out, part{p.f, []byte(xml)})
	}
	if err := w.Close(); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return nil, err
	}
	return slotsOf(out), nil
}

// Text is the visible text of the document body, one string per non-empty paragraph.
func Text(path string) ([]string, error) {
	z, parts, err := readParts(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = z.Close() }()
	var out []string
	for _, p := range parts {
		if p.f.Name != "word/document.xml" {
			continue
		}
		xml := string(p.data)
		for _, s := range leafParagraphs(xml) {
			if t := strings.TrimSpace(paraText(xml[s.start:s.end])); t != "" {
				out = append(out, t)
			}
		}
	}
	return out, nil
}

// skeleton is a paragraph without its words: what is left is formatting and structure.
func skeleton(p string) string {
	return textRun.ReplaceAllString(p, "<w:t></w:t>")
}

// Same lists how filled differs from the template beyond the words of its slots: other styles,
// fonts, page setup, headers, numbering, media, or paragraph and run formatting. A paragraph that
// held only slots may be missing.
func Same(tmpl, filled string) ([]string, error) {
	zt, want, err := readParts(tmpl)
	if err != nil {
		return nil, err
	}
	defer func() { _ = zt.Close() }()
	zf, got, err := readParts(filled)
	if err != nil {
		return nil, err
	}
	defer func() { _ = zf.Close() }()
	gotBy := map[string][]byte{}
	for _, p := range got {
		gotBy[p.f.Name] = p.data
	}
	var problems []string
	for _, p := range want {
		b, ok := gotBy[p.f.Name]
		delete(gotBy, p.f.Name)
		switch {
		case !ok:
			problems = append(problems, p.f.Name+" is missing")
		case textPart(p.f.Name):
			problems = append(problems, sameText(p.f.Name, string(p.data), string(b))...)
		case !bytes.Equal(p.data, b):
			problems = append(problems, p.f.Name+" differs from the template")
		}
	}
	var extra []string
	for n := range gotBy {
		extra = append(extra, n)
	}
	sort.Strings(extra)
	for _, n := range extra {
		problems = append(problems, n+" is not in the template")
	}
	return problems, nil
}

// sameText aligns the paragraphs of two text parts and compares their skeletons and the XML
// between them, which holds the tables and the page setup.
func sameText(name, want, got string) []string {
	ws, gs := leafParagraphs(want), leafParagraphs(got)
	wsk, gsk := make([]string, len(ws)), make([]string, len(gs))
	optional := make([]bool, len(ws))
	for i, s := range ws {
		p := want[s.start:s.end]
		wsk[i] = skeleton(p)
		t := paraText(p)
		optional[i] = placeholder.MatchString(t) && strings.TrimSpace(placeholder.ReplaceAllString(t, "")) == ""
	}
	for i, s := range gs {
		gsk[i] = skeleton(got[s.start:s.end])
	}
	if !align(wsk, optional, gsk) {
		for i := range ws {
			if i >= len(gs) || wsk[i] != gsk[i] {
				return []string{fmt.Sprintf("%s: the formatting of %q changed, or paragraphs were added or removed", name, clip(paraText(want[ws[i].start:ws[i].end])))}
			}
		}
		return []string{fmt.Sprintf("%s: %d paragraphs the template does not have", name, len(gs)-len(ws))}
	}
	// the XML around the paragraphs: removed paragraphs leave nothing behind
	var wantRest, gotRest strings.Builder
	wl := 0
	for _, s := range ws {
		wantRest.WriteString(want[wl:s.start])
		wl = s.end
	}
	wantRest.WriteString(want[wl:])
	gl := 0
	for _, s := range gs {
		gotRest.WriteString(got[gl:s.start])
		gl = s.end
	}
	gotRest.WriteString(got[gl:])
	if wantRest.String() != gotRest.String() {
		return []string{name + ": page setup, tables or sections differ from the template"}
	}
	return nil
}

// align is true when the filled paragraphs are the template's, in order and with the same
// skeletons, except optional template paragraphs that were removed.
func align(want []string, optional []bool, got []string) bool {
	n, m := len(want), len(got)
	fits := make([][]bool, n+1)
	for i := range fits {
		fits[i] = make([]bool, m+1)
	}
	fits[n][m] = true
	for i := n - 1; i >= 0; i-- {
		for g := m; g >= 0; g-- {
			fits[i][g] = (g < m && want[i] == got[g] && fits[i+1][g+1]) || (optional[i] && fits[i+1][g])
		}
	}
	return fits[0][0]
}

func clip(s string) string {
	if r := []rune(s); len(r) > 40 {
		return string(r[:40]) + "…"
	}
	return s
}
