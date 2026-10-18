package docx

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Check is one finding of Verify.
type Check struct {
	Name string `json:"name"`
	OK   bool   `json:"ok"`
	Note string `json:"note,omitempty"`
}

// VerifyOptions say what a PDF must hold.
type VerifyOptions struct {
	MaxPages int      // 0 skips the page check
	Contains []string // phrases the text layer must hold, compared with typographic punctuation folded
}

var (
	emailRe = regexp.MustCompile(`[\w.+-]+@[\w-]+\.[\w.-]+`)
	// a year joined to a dash other than "-": Workday and other ATS parsers split ranges on "-" only
	dashRangeRe = regexp.MustCompile(`\b(?:19|20)\d{2}[ \t]*([\x{2010}-\x{2015}\x{2212}])[ \t]*(?:(?:19|20)\d{2}\b|[A-Za-z]+)`)
	// pdftotext prints a page per form feed; a page with fewer letters than this is treated as an image
	minPageText = 200
	folds       = strings.NewReplacer("‘", "'", "’", "'", "“", `"`, "”", `"`, "–", "-", "—", "-", " ", " ")
)

// Verify checks a rendered CV or cover letter the way an ATS reads it: the page count, a text
// layer on every page, an e-mail address, the phrases that must survive, and date ranges an ATS
// cannot split. Text checks need pdftotext (Poppler); without it they are reported as skipped.
func Verify(ctx context.Context, path string, o VerifyOptions) ([]Check, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(string(b[:min(len(b), 5)]), "%PDF") {
		return nil, fmt.Errorf("%s is not a PDF", path)
	}
	var out []Check
	pages := Pages(b)
	if o.MaxPages > 0 {
		out = append(out, Check{"pages", pages > 0 && pages <= o.MaxPages, fmt.Sprintf("%d of at most %d", pages, o.MaxPages)})
	}
	text, ok, err := PDFText(ctx, path)
	if err != nil {
		return nil, err
	}
	if !ok {
		return append(out, Check{"text layer", true, "skipped: install poppler (pdftotext) to check the text an ATS reads"}), nil
	}
	out = append(out, textLayer(text))
	out = append(out, Check{"e-mail", emailRe.MatchString(text), "an ATS fills the contact fields from the text layer"})
	flat := strings.Join(strings.Fields(folds.Replace(text)), " ")
	for _, want := range o.Contains {
		w := strings.Join(strings.Fields(folds.Replace(want)), " ")
		out = append(out, Check{"contains " + want, strings.Contains(strings.ToLower(flat), strings.ToLower(w)), ""})
	}
	var bad []string
	for line := range strings.SplitSeq(text, "\n") {
		if m := dashRangeRe.FindStringSubmatch(line); m != nil {
			bad = append(bad, fmt.Sprintf("%q uses U+%04X", strings.TrimSpace(line), []rune(m[1])[0]))
		}
	}
	note := "write date ranges with a plain hyphen"
	if len(bad) > 0 {
		note = strings.Join(bad[:min(len(bad), 3)], "; ") + ": " + note
	}
	out = append(out, Check{"date ranges", len(bad) == 0, note})
	return out, nil
}

// textLayer fails when a page has almost no text: it was exported as an image, so an ATS sees nothing.
func textLayer(text string) Check {
	pages := strings.Split(strings.TrimRight(text, "\f\n "), "\f")
	var thin []string
	for i, p := range pages {
		if utf8.RuneCountInString(strings.Join(strings.Fields(p), " ")) < minPageText {
			thin = append(thin, fmt.Sprint(i+1))
		}
	}
	if len(thin) == 0 {
		return Check{"text layer", true, fmt.Sprintf("%d pages of selectable text", len(pages))}
	}
	return Check{"text layer", false, "little or no text on page " + strings.Join(thin, ", ") + ": export it as text, not an image"}
}
