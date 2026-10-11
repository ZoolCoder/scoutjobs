// Package pdf turns a Markdown resume into a single-column PDF that ATS parsers read cleanly:
// base-14 Helvetica, real text (no outlines), no tables, no columns, no images. Pure Go, no dependencies.
package pdf

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const (
	pageW, pageH = 595.0, 842.0 // A4 in points
	margin       = 56.0
	bodySize     = 10.5
	lineH        = 14.5
	bulletIndent = 16.0
)

type line struct {
	text  string
	size  float64
	bold  bool
	x     float64 // extra indent
	after float64 // gap below
}

// Render lays out Markdown (headings, paragraphs, bullet lists, bold/italic/links stripped to text) as PDF bytes.
func Render(markdown string, title string, now time.Time) []byte {
	lines := layout(markdown)
	var pages [][]line
	var cur []line
	y := pageH - margin
	for _, l := range lines {
		need := l.size + l.after
		if y-need < margin && len(cur) > 0 {
			pages = append(pages, cur)
			cur, y = nil, pageH-margin
		}
		cur = append(cur, l)
		y -= lineHeight(l) + l.after
	}
	if len(cur) > 0 || len(pages) == 0 {
		pages = append(pages, cur)
	}
	return write(pages, title, now)
}

func lineHeight(l line) float64 {
	if l.size > bodySize {
		return l.size * 1.35
	}
	return lineH
}

var (
	reInline = regexp.MustCompile(`\*\*(.+?)\*\*|__(.+?)__|\*(.+?)\*|_(.+?)_|` + "`(.+?)`")
	reLink   = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	reBullet = regexp.MustCompile(`^\s*[-*+]\s+`)
	reNum    = regexp.MustCompile(`^\s*(\d+)[.)]\s+`)
)

// plain strips Markdown emphasis; links keep their URL so a parser still sees it.
func plain(s string) string {
	s = reLink.ReplaceAllStringFunc(s, func(m string) string {
		g := reLink.FindStringSubmatch(m)
		bare := strings.TrimPrefix(strings.TrimPrefix(g[2], "https://"), "http://")
		if g[1] == g[2] || g[1] == bare || g[1] == strings.TrimSuffix(bare, "/") {
			return g[2]
		}
		return g[1] + " (" + g[2] + ")"
	})
	for i := 0; i < 3; i++ { // nested emphasis
		s = reInline.ReplaceAllStringFunc(s, func(m string) string {
			g := reInline.FindStringSubmatch(m)
			for _, x := range g[1:] {
				if x != "" {
					return x
				}
			}
			return m
		})
	}
	return strings.TrimSpace(s)
}

func layout(md string) []line {
	var out []line
	para := func(text string, size float64, bold bool, indent float64, prefix string, after float64) {
		if bold && len(out) > 0 && out[len(out)-1].after < 5 { // breathing room above a heading
			out[len(out)-1].after = 5
		}
		width := pageW - 2*margin - indent
		first := true
		for _, w := range wrap(text, size, bold, width, prefix) {
			l := line{text: w, size: size, bold: bold, x: indent}
			if first && prefix != "" {
				l.text = prefix + w
				first = false
			}
			out = append(out, l)
		}
		if len(out) > 0 {
			out[len(out)-1].after = after
		}
	}
	var buf []string
	flush := func() {
		if len(buf) > 0 {
			para(plain(strings.Join(buf, " ")), bodySize, false, 0, "", 6)
			buf = nil
		}
	}
	for _, raw := range strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n") {
		t := strings.TrimRight(raw, " \t")
		switch {
		case strings.TrimSpace(t) == "":
			flush()
		case strings.HasPrefix(t, "# "):
			flush()
			para(plain(t[2:]), 17, true, 0, "", 4)
		case strings.HasPrefix(t, "## "):
			flush()
			para(strings.ToUpper(plain(t[3:])), 12, true, 0, "", 3)
		case strings.HasPrefix(t, "### "):
			flush()
			para(plain(t[4:]), 11, true, 0, "", 2)
		case strings.HasPrefix(t, "#### "):
			flush()
			para(plain(t[5:]), bodySize, true, 0, "", 1)
		case strings.HasPrefix(strings.TrimSpace(t), "---") && strings.Trim(strings.TrimSpace(t), "-") == "":
			flush()
			out = append(out, line{text: "", size: bodySize, after: 2})
		case reBullet.MatchString(t):
			flush()
			para(plain(reBullet.ReplaceAllString(t, "")), bodySize, false, bulletIndent, "• ", 1)
		case reNum.MatchString(t):
			flush()
			n := reNum.FindStringSubmatch(t)[1]
			para(plain(reNum.ReplaceAllString(t, "")), bodySize, false, bulletIndent, n+". ", 1)
		case strings.HasPrefix(t, "> "):
			flush()
			para(plain(t[2:]), bodySize, false, bulletIndent, "", 3)
		case strings.HasPrefix(t, "|"):
			flush() // tables are flattened to one line per row, cells separated by two spaces
			cells := strings.Split(strings.Trim(t, "|"), "|")
			if strings.Trim(strings.Join(cells, ""), " -:") == "" {
				continue
			}
			for i := range cells {
				cells[i] = plain(cells[i])
			}
			para(strings.Join(cells, "  "), bodySize, false, 0, "", 1)
		default:
			buf = append(buf, strings.TrimSpace(t))
		}
	}
	flush()
	return out
}

func wrap(text string, size float64, bold bool, width float64, prefix string) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{""}
	}
	var lines []string
	cur := ""
	avail := width - textWidth(prefix, size, bold)
	for _, w := range words {
		try := w
		if cur != "" {
			try = cur + " " + w
		}
		if textWidth(try, size, bold) <= avail || cur == "" {
			cur = try
			continue
		}
		lines = append(lines, cur)
		cur = w
	}
	return append(lines, cur)
}

func textWidth(s string, size float64, bold bool) float64 {
	w := 0
	for _, r := range s {
		w += glyphWidth(toWinAnsi(r), bold)
	}
	return float64(w) * size / 1000
}

func write(pages [][]line, title string, now time.Time) []byte {
	var b bytes.Buffer
	var offsets []int
	obj := func(body string) int {
		offsets = append(offsets, b.Len())
		n := len(offsets)
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", n, body)
		return n
	}
	b.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	// 1 catalog, 2 pages, 3 regular font, 4 bold font, then per page: content + page object
	obj("<< /Type /Catalog /Pages 2 0 R >>")
	pagesIdx := len(offsets) + 1
	offsets = append(offsets, -1) // placeholder for the pages object
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>")
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold /Encoding /WinAnsiEncoding >>")
	var kids []string
	for _, p := range pages {
		content := contentStream(p)
		c := obj(fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(content), content))
		pg := obj(fmt.Sprintf("<< /Type /Page /Parent %d 0 R /MediaBox [0 0 %.0f %.0f] /Resources << /Font << /F1 3 0 R /F2 4 0 R >> >> /Contents %d 0 R >>", pagesIdx, pageW, pageH, c))
		kids = append(kids, fmt.Sprintf("%d 0 R", pg))
	}
	offsets[pagesIdx-1] = b.Len()
	fmt.Fprintf(&b, "%d 0 obj\n<< /Type /Pages /Kids [%s] /Count %d >>\nendobj\n", pagesIdx, strings.Join(kids, " "), len(kids))
	info := obj(fmt.Sprintf("<< /Title (%s) /Producer (scoutjobs) /CreationDate (D:%s) >>", escape(title), now.UTC().Format("20060102150405")+"Z"))
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
	for _, o := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R /Info %d 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, info, xref)
	return b.Bytes()
}

func contentStream(p []line) string {
	var c strings.Builder
	y := pageH - margin
	c.WriteString("BT\n")
	for _, l := range p {
		h := lineHeight(l)
		y -= h
		if l.text != "" {
			font := "/F1"
			if l.bold {
				font = "/F2"
			}
			fmt.Fprintf(&c, "%s %.1f Tf 1 0 0 1 %.1f %.1f Tm (%s) Tj\n", font, l.size, margin+l.x, y+(h-l.size)/2, escape(l.text))
		}
		y -= l.after
	}
	c.WriteString("ET\n")
	return c.String()
}

// escape encodes a string for a PDF literal in WinAnsiEncoding.
func escape(s string) string {
	var b strings.Builder
	for _, r := range s {
		c := toWinAnsi(r)
		switch {
		case c == '(' || c == ')' || c == '\\':
			b.WriteByte('\\')
			b.WriteByte(byte(c))
		case c < 32 || c > 126:
			fmt.Fprintf(&b, "\\%03o", c)
		default:
			b.WriteByte(byte(c))
		}
	}
	return b.String()
}

// toWinAnsi maps a rune to its WinAnsi code; what the font cannot show becomes '?'.
func toWinAnsi(r rune) int {
	switch {
	case r < 128:
		return int(r)
	case r >= 0xA0 && r <= 0xFF:
		return int(r)
	}
	if c, ok := winAnsiExtra[r]; ok {
		return c
	}
	return '?'
}

var winAnsiExtra = map[rune]int{
	'€': 128, '‚': 130, 'ƒ': 131, '„': 132, '…': 133, '†': 134, '‡': 135,
	'ˆ': 136, '‰': 137, 'Š': 138, '‹': 139, 'Œ': 140, 'Ž': 142,
	'‘': 145, '’': 146, '“': 147, '”': 148, '•': 149, '–': 150, '—': 151,
	'˜': 152, '™': 153, 'š': 154, '›': 155, 'œ': 156, 'ž': 158, 'Ÿ': 159,
	' ': 32, '→': '>', '✓': 'v', '✔': 'v',
}
