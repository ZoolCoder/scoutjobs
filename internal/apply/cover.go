package apply

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/docx"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

// SlotsName holds the words of a tailored cover letter, slot by slot, for a Word template.
const SlotsName = "cover-letter.slots.json"

// Template check results.
const (
	CoverPass = "pass" // slots valid, design unchanged, rendered within the template's pages
	CoverWarn = "warn" // slots valid and design unchanged, but not rendered: page count unknown
	CoverFail = "fail"
)

// CoverRecord is the last cover letter built from a Word template: what it was made from, the
// files it wrote, and the template check.
type CoverRecord struct {
	At            string            `json:"at"`
	Template      string            `json:"template"`
	TemplateHash  string            `json:"template_hash"`
	Files         map[string]string `json:"files"` // slots, cover-letter.md, .docx and .pdf as built
	Pages         int               `json:"pages,omitempty"`
	TemplatePages int               `json:"template_pages,omitempty"`
	Rendered      bool              `json:"rendered"`
	State         string            `json:"state"`
	Problems      []string          `json:"problems,omitempty"`
}

// slotRule is how one well-known slot is checked; any other slot is optional, up to 300 characters.
type slotRule struct {
	required bool
	max      int
	stated   bool // a name or address: only when the posting or the job states it, never guessed
}

var slotRules = map[string]slotRule{
	"candidate_header":       {max: 300},
	"employer_name":          {required: true, max: 120},
	"recipient_name_or_team": {max: 120, stated: true},
	"employer_address":       {max: 300, stated: true},
	"location_and_date":      {max: 80},
	"subject":                {max: 150},
	"salutation":             {required: true, max: 80},
	"body_paragraph_1":       {required: true, max: 1200},
	"body_paragraph_2":       {max: 1200},
	"body_paragraph_3":       {max: 1200},
	"body_paragraph_4":       {max: 1200},
	"signoff":                {required: true, max: 60},
	"candidate_name":         {required: true, max: 80},
}

func ruleFor(slot string) slotRule {
	if r, ok := slotRules[slot]; ok {
		return r
	}
	return slotRule{max: 300}
}

// IsWordTemplate is true when the configured cover letter template is a Word file to fill.
func IsWordTemplate(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".docx")
}

// TemplateSlots lists a Word template's slots with their rules, for the agent's instructions.
func TemplateSlots(path string) ([]string, error) {
	slots, err := docx.Slots(path)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(slots))
	for i, s := range slots {
		r := ruleFor(s)
		switch {
		case r.required:
			out[i] = fmt.Sprintf("%s (required, up to %d characters)", s, r.max)
		case r.stated:
			out[i] = fmt.Sprintf("%s (only if the posting states it, else empty)", s)
		default:
			out[i] = fmt.Sprintf("%s (optional, up to %d characters)", s, r.max)
		}
	}
	return out, nil
}

// checkSlots validates the slot values against the template and the job.
func checkSlots(slots []string, values map[string]string, j store.Job, stated string) []string {
	var problems []string
	known := map[string]bool{}
	for _, s := range slots {
		known[s] = true
		v := strings.TrimSpace(values[s])
		r := ruleFor(s)
		switch {
		case v == "" && r.required:
			problems = append(problems, s+" is required")
		case len([]rune(v)) > r.max:
			problems = append(problems, fmt.Sprintf("%s is %d characters, the limit is %d", s, len([]rune(v)), r.max))
		case v != "" && r.stated && !generic(v) && !strings.Contains(norm(stated), norm(v)):
			problems = append(problems, s+" is not stated in posting.md or on the job: leave it empty rather than guess")
		}
		if strings.Contains(v, "{{") || strings.Contains(v, "}}") {
			problems = append(problems, s+" contains a placeholder")
		}
	}
	if v := values["employer_name"]; known["employer_name"] && v != "" && j.Company != "" && !strings.Contains(norm(v), norm(j.Company)) && !strings.Contains(norm(j.Company), norm(v)) {
		problems = append(problems, fmt.Sprintf("employer_name %q is not %s, the company of this job", v, j.Company))
	}
	var extra []string
	for k := range values {
		if !known[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	for _, k := range extra {
		problems = append(problems, k+" is not a slot of the template")
	}
	return problems
}

// generic is a greeting target that names no one, such as "Hiring team".
func generic(v string) bool {
	v = norm(v)
	for _, w := range []string{"team", "hiring", "recruit", "talent", "people", "hr", "sir or madam"} {
		if strings.Contains(v, w) {
			return true
		}
	}
	return false
}

func norm(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

// BuildCover fills the Word template with cover-letter.slots.json and writes cover-letter.docx,
// cover-letter.md (the same words, for review and the e-mail) and, when LibreOffice is installed,
// cover-letter.pdf. The record says whether the result kept the template's design and length.
func BuildCover(ctx context.Context, s *store.Store, j store.Job, template string) (CoverRecord, error) {
	dir := Dir(s, j)
	rec := CoverRecord{At: time.Now().UTC().Format(time.RFC3339), Template: template, TemplateHash: hashFile(template), Files: map[string]string{}}
	raw, err := os.ReadFile(filepath.Join(dir, SlotsName))
	if err != nil {
		return rec, fmt.Errorf("no %s in %s: write the slot values first", SlotsName, dir)
	}
	values := map[string]string{}
	if err := json.Unmarshal(raw, &values); err != nil {
		return rec, fmt.Errorf("%s: %w (one JSON object, slot name to text)", SlotsName, err)
	}
	slots, err := docx.Slots(template)
	if err != nil {
		return rec, err
	}
	if len(slots) == 0 {
		return rec, fmt.Errorf("%s has no {{slot}} placeholders: mark the parts each letter changes, such as {{employer_name}} and {{body_paragraph_1}}", filepath.Base(template))
	}
	posting, _ := os.ReadFile(filepath.Join(dir, "posting.md"))
	rec.Problems = checkSlots(slots, values, j, string(posting)+"\n"+j.Contact+"\n"+j.Notes)
	fill := map[string]string{}
	for _, sl := range slots {
		fill[sl] = values[sl]
	}

	out := filepath.Join(dir, "cover-letter.docx")
	left, err := docx.Fill(template, out, fill)
	if err != nil {
		return rec, err
	}
	for _, l := range left {
		rec.Problems = append(rec.Problems, "placeholder {{"+l+"}} is left in cover-letter.docx")
	}
	drift, err := docx.Same(template, out)
	if err != nil {
		return rec, err
	}
	rec.Problems = append(rec.Problems, drift...)
	text, err := docx.Text(out)
	if err != nil {
		return rec, err
	}
	if err := os.WriteFile(filepath.Join(dir, "cover-letter.md"), []byte(strings.Join(text, "\n\n")+"\n"), 0o644); err != nil {
		return rec, err
	}

	pdfPath := filepath.Join(dir, "cover-letter.pdf")
	if office := docx.Office(); office != "" {
		problems, err := renderCover(ctx, office, template, out, pdfPath, text, &rec)
		if err != nil {
			return rec, err
		}
		rec.Problems = append(rec.Problems, problems...)
	} else if LoadProvenance(dir).exported(dir, "cover-letter.pdf") {
		_ = os.Remove(pdfPath) // made from an older build: it no longer matches the DOCX
	}

	switch {
	case len(rec.Problems) > 0:
		rec.State = CoverFail
	case !rec.Rendered:
		rec.State = CoverWarn
	default:
		rec.State = CoverPass
	}
	names := []string{SlotsName, "cover-letter.md", "cover-letter.docx"}
	if rec.Rendered {
		names = append(names, "cover-letter.pdf")
	}
	rec.Files = snapshot(dir, names).Files
	p := LoadProvenance(dir)
	p.Cover = &rec
	if p.Exports == nil {
		p.Exports = map[string]Record{}
	}
	delete(p.Exports, "cover-letter.pdf")
	for _, n := range names[2:] { // the DOCX and PDF carry the reviewed Markdown's words
		p.Exports[n] = snapshot(dir, []string{"cover-letter.md", n})
	}
	if err := p.save(dir); err != nil {
		return rec, err
	}
	_ = s.AddEvent(j.ID, "cv:cover", rec.State+": "+rec.Summary())
	return rec, nil
}

// renderCover renders the letter and the template, compares their page counts and checks the PDF's
// text layer against the DOCX wording.
func renderCover(ctx context.Context, office, template, docxPath, pdfPath string, text []string, rec *CoverRecord) ([]string, error) {
	tmp, err := os.MkdirTemp("", "scoutjobs-cover-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	rendered, err := docx.Render(ctx, office, docxPath, tmp)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(rendered)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(pdfPath, b, 0o644); err != nil {
		return nil, err
	}
	rec.Rendered, rec.Pages = true, docx.Pages(b)
	rec.TemplatePages = 1
	if tp, err := docx.Render(ctx, office, template, tmp); err == nil {
		if tb, err := os.ReadFile(tp); err == nil && docx.Pages(tb) > 0 {
			rec.TemplatePages = docx.Pages(tb)
		}
	}
	var problems []string
	if rec.Pages > rec.TemplatePages {
		problems = append(problems, fmt.Sprintf("cover-letter.pdf runs to %d pages, the template has %d: shorten the body", rec.Pages, rec.TemplatePages))
	}
	if layer, ok, err := docx.PDFText(ctx, pdfPath); err != nil {
		problems = append(problems, err.Error())
	} else if ok {
		if strings.TrimSpace(layer) == "" {
			problems = append(problems, "cover-letter.pdf has no text layer")
		} else if miss := docx.Missing(text, layer); len(miss) > 0 {
			problems = append(problems, fmt.Sprintf("cover-letter.pdf lacks %d words of the DOCX, such as %q", len(miss), miss[0]))
		}
	}
	return problems, nil
}

// Summary is the result in one line.
func (r CoverRecord) Summary() string {
	if len(r.Problems) > 0 {
		return strings.Join(r.Problems, "; ")
	}
	if !r.Rendered {
		return "template kept; not rendered, install LibreOffice to check the page count"
	}
	return fmt.Sprintf("template kept, %d of %d pages", r.Pages, r.TemplatePages)
}

// CoverChanges lists what changed since the last build: the slots, the Markdown, the files built,
// or the template itself.
func CoverChanges(dir string) []string {
	p := LoadProvenance(dir)
	if p.Cover == nil {
		return nil
	}
	now := snapshot(dir, []string{SlotsName, "cover-letter.md", "cover-letter.docx", "cover-letter.pdf"})
	var out []string
	for n, h := range p.Cover.Files {
		if now.Files[n] != h {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	if p.Cover.TemplateHash != hashFile(p.Cover.Template) {
		out = append(out, "the template "+filepath.Base(p.Cover.Template))
	}
	return out
}

// ErrNoWordTemplate is returned when a Word build is asked for without a .docx template.
var ErrNoWordTemplate = errors.New("cover_template in config.yaml is not a .docx file with {{slot}} placeholders")
