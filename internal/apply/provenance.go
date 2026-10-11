package apply

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// ProvenanceName records, per application folder, which bytes each result was made from, so a
// verdict or a score is only trusted while the files it covered are unchanged.
const ProvenanceName = "provenance.json"

// Record is one result and the SHA-256 of every file it covered, by filename.
type Record struct {
	At    string            `json:"at"`
	Files map[string]string `json:"files"`
}

// Provenance binds the draft review to the final documents, each export to its Markdown source and
// output, and the ATS score to the file scored.
type Provenance struct {
	Review  *Record           `json:"review,omitempty"`
	Exports map[string]Record `json:"exports,omitempty"` // by output name: the source .md and the .pdf made
	ATS     *Record           `json:"ats,omitempty"`
	Cover   *CoverRecord      `json:"cover,omitempty"` // the cover letter built from a Word template
}

// finalDocs are the files an application can send; the review covers every one present, except a
// PDF ScoutJobs rendered from a reviewed Markdown file.
var finalDocs = []string{"resume.md", "cover-letter.md", "resume.pdf", "resume.docx", "cover-letter.pdf", "cover-letter.docx"}

func LoadProvenance(dir string) Provenance {
	var p Provenance
	if b, err := os.ReadFile(filepath.Join(dir, ProvenanceName)); err == nil {
		_ = json.Unmarshal(b, &p)
	}
	return p
}

func (p Provenance) save(dir string) error {
	if !exists(dir) { // no application folder yet: no file to vouch for
		return nil
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, ProvenanceName), append(b, '\n'), 0o600)
}

func hashFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func snapshot(dir string, names []string) Record {
	r := Record{At: time.Now().UTC().Format(time.RFC3339), Files: map[string]string{}}
	for _, n := range names {
		if h := hashFile(filepath.Join(dir, n)); h != "" {
			r.Files[n] = h
		}
	}
	return r
}

// changed lists the files of the current set that the record did not cover with these bytes.
func (r Record) changed(now Record) []string {
	var out []string
	for n, h := range now.Files {
		if r.Files[n] != h {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// exported is true when the file is a PDF ScoutJobs rendered, byte for byte.
func (p Provenance) exported(dir, name string) bool {
	e, ok := p.Exports[name]
	return ok && e.Files[name] != "" && e.Files[name] == hashFile(filepath.Join(dir, name))
}

// ReviewedFiles are the final documents a draft review must cover now.
func ReviewedFiles(dir string) []string {
	p := LoadProvenance(dir)
	var out []string
	for _, n := range finalDocs {
		if exists(filepath.Join(dir, n)) && !p.exported(dir, n) {
			out = append(out, n)
		}
	}
	return out
}

// StampReview binds a review verdict just recorded to the bytes of the final documents.
func StampReview(dir string) error {
	p := LoadProvenance(dir)
	r := snapshot(dir, ReviewedFiles(dir))
	p.Review = &r
	return p.save(dir)
}

// ReviewChanges lists the final documents added or edited since the recorded review. known is
// false when the review predates provenance, so callers fall back to modification times.
func ReviewChanges(dir string) (changed []string, known bool) {
	p := LoadProvenance(dir)
	if p.Review == nil {
		return nil, false
	}
	return p.Review.changed(snapshot(dir, ReviewedFiles(dir))), true
}

func stampExport(dir, src, dst string) error {
	p := LoadProvenance(dir)
	if p.Exports == nil {
		p.Exports = map[string]Record{}
	}
	p.Exports[filepath.Base(dst)] = snapshot(dir, []string{filepath.Base(src), filepath.Base(dst)})
	return p.save(dir)
}

// Export states of a PDF rendered from Markdown.
const (
	ExportCurrent  = "current"  // rendered from the Markdown as it is now
	ExportOutdated = "outdated" // rendered from an older version of the Markdown
	ExportOwn      = "own"      // replaced by another file after the export: the user's final document
)

// ExportState says where <name>.pdf came from; "" when there is no PDF or no record of an export.
func ExportState(dir, name string) string {
	pdfName := name + ".pdf"
	e, ok := LoadProvenance(dir).Exports[pdfName]
	if !ok || !exists(filepath.Join(dir, pdfName)) {
		return ""
	}
	switch {
	case e.Files[pdfName] != hashFile(filepath.Join(dir, pdfName)):
		return ExportOwn
	case e.Files[name+".md"] != hashFile(filepath.Join(dir, name+".md")):
		return ExportOutdated
	}
	return ExportCurrent
}

// ScoredFile is the resume an ATS check scores: the PDF when there is one.
func ScoredFile(dir string) string {
	if exists(filepath.Join(dir, "resume.pdf")) {
		return "resume.pdf"
	}
	return "resume.md"
}

// StampATS binds an ATS score just logged to the bytes of the file scored.
func StampATS(dir, file string) error {
	if file == "" || !exists(filepath.Join(dir, file)) {
		file = ScoredFile(dir)
	}
	p := LoadProvenance(dir)
	r := snapshot(dir, []string{file})
	p.ATS = &r
	return p.save(dir)
}

// ATSChanges names the resume to score when it is not the file the recorded score covered.
func ATSChanges(dir string) (changed []string, known bool) {
	p := LoadProvenance(dir)
	if p.ATS == nil {
		return nil, false
	}
	return p.ATS.changed(snapshot(dir, []string{ScoredFile(dir)})), true
}

// ProvenanceStatus is the recorded provenance with what changed since each result, for the API.
type ProvenanceStatus struct {
	Provenance
	ReviewChanged []string `json:"review_changed,omitempty"` // final documents edited or added since the review
	ATSChanged    []string `json:"ats_changed,omitempty"`    // the resume to score, when the score covered other bytes
	ResumePDF     string   `json:"resume_pdf,omitempty"`     // current | outdated | own
	CoverChanged  []string `json:"cover_changed,omitempty"`  // edited since the cover letter was built from the template
}

func ProvenanceOf(dir string) ProvenanceStatus {
	st := ProvenanceStatus{Provenance: LoadProvenance(dir), ResumePDF: ExportState(dir, "resume")}
	st.ReviewChanged, _ = ReviewChanges(dir)
	st.ATSChanged, _ = ATSChanges(dir)
	st.CoverChanged = CoverChanges(dir)
	return st
}

// Short is the first 12 hex digits of a hash, enough to tell versions apart on screen.
func Short(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}
