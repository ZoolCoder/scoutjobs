// Package apply owns the per-job application folder: where it is, what is in it,
// which steps of the "get selected" kit are done, and the e-mail draft built from it.
// cli and web both use it so the dashboard, the CLI and the JSON API agree.
package apply

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/mail"
	"github.com/zoolcoder/scoutjobs/internal/pdf"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

// DraftName is the reviewed message, EmlName the same as a file any mail app can open.
const DraftName, EmlName = "email.json", "email.eml"

// Textual lists the file types the dashboard shows and the API reads or writes as text.
var Textual = map[string]bool{".md": true, ".txt": true, ".json": true, ".eml": true}

// Writable is the subset agents may create or replace through the API: drafts, never mail or binaries.
var Writable = map[string]bool{".md": true, ".txt": true, ".json": true}

// MaxFile caps a draft written through the API.
const MaxFile = 1 << 20

// Dir is the application folder of a job: applications/<company>_<title> under the data dir.
func Dir(s *store.Store, j store.Job) string {
	return filepath.Join(s.Home, "applications", store.Slug(j.Company)+"_"+store.Slug(j.Title))
}

// File is one entry of the application folder.
type File struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	Mod  string `json:"modified"`
}

// Files lists the folder, names sorted as the OS returns them, directories skipped.
func Files(dir string) []File {
	var out []File
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, File{Name: e.Name(), Size: info.Size(), Mod: info.ModTime().Format("2006-01-02 15:04")})
	}
	return out
}

// Kit is the per-job "get selected" checklist the job page and the API show.
type Kit struct {
	Resume bool           `json:"resume"`
	Cover  bool           `json:"cover"`
	ATS    *store.CVCheck `json:"ats,omitempty"`
	Draft  *mail.Message  `json:"draft,omitempty"`
	Sent   bool           `json:"sent"`
	Done   int            `json:"done"`
	Of     int            `json:"of"`
}

// Status computes the kit from the folder, the latest CV check for the job and its events.
func Status(s *store.Store, j store.Job, events []store.Event) (Kit, string, []File) {
	dir := Dir(s, j)
	files := Files(dir)
	has := map[string]bool{}
	for _, f := range files {
		has[f.Name] = true
	}
	k := Kit{Resume: has["resume.md"] || has["resume.pdf"], Cover: has["cover-letter.md"], Of: 4}
	if checks, err := s.CVChecks(200); err == nil {
		for i := range checks {
			if checks[i].JobID == j.ID {
				k.ATS = &checks[i]
				break
			}
		}
	}
	for _, e := range events {
		if e.Kind == "email:sent" {
			k.Sent = true
			break
		}
	}
	if m, err := LoadDraft(dir); err == nil {
		k.Draft = &m
	}
	for _, ok := range []bool{k.Resume, k.Cover, k.ATS != nil && k.ATS.ATSScore >= 70, k.Sent} {
		if ok {
			k.Done++
		}
	}
	return k, dir, files
}

// ReadFile returns a textual file of the folder; the name is reduced to its base so paths cannot escape.
func ReadFile(dir, name string) ([]byte, string, error) {
	name = filepath.Base(name)
	if !Textual[strings.ToLower(filepath.Ext(name))] {
		return nil, name, fmt.Errorf("%s is not a text draft", name)
	}
	b, err := os.ReadFile(filepath.Join(dir, name))
	return b, name, err
}

// WriteFile stores a draft the agent produced (resume.md, cover-letter.md, research.md ...).
func WriteFile(dir, name string, body []byte) (string, error) {
	name = filepath.Base(name)
	if !Writable[strings.ToLower(filepath.Ext(name))] {
		return name, fmt.Errorf("%s: only .md, .txt and .json drafts can be written", name)
	}
	if len(body) > MaxFile {
		return name, fmt.Errorf("%s: larger than 1 MB", name)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return name, err
	}
	return name, os.WriteFile(filepath.Join(dir, name), body, 0o600)
}

// DraftOptions are the knobs of `scoutjobs email draft` and POST /api/jobs/{id}/email/draft.
type DraftOptions struct {
	To      string   `json:"to"`
	Subject string   `json:"subject"`
	Body    string   `json:"body"`   // file in the folder used as body; default cover-letter.md
	Attach  []string `json:"attach"` // extra attachments, relative to the folder or absolute
}

// Draft builds the application e-mail from the folder, stores email.json (and email.eml when a sender is known),
// records the contact on the job and logs the event. Nothing is sent.
func Draft(s *store.Store, j store.Job, from string, o DraftOptions) (mail.Message, string, error) {
	dir := Dir(s, j)
	if o.To == "" {
		o.To = j.Contact
	} else if j.Contact != o.To {
		_ = s.UpdateJob(j.ID, store.JobPatch{Contact: &o.To})
	}
	if o.Subject == "" {
		o.Subject = fmt.Sprintf("Application: %s at %s", j.Title, j.Company)
	}
	if o.Body == "" {
		o.Body = "cover-letter.md"
	}
	body, err := os.ReadFile(filepath.Join(dir, filepath.Base(o.Body)))
	if err != nil {
		return mail.Message{}, dir, fmt.Errorf("no %s in %s: write the cover letter first (/scoutjobs:cover %d)", o.Body, dir, j.ID)
	}
	files := DefaultAttachments(dir)
	for _, a := range o.Attach {
		if !filepath.IsAbs(a) {
			a = filepath.Join(dir, a)
		}
		files = append(files, a)
	}
	m := mail.Message{From: from, To: o.To, Subject: o.Subject, Body: string(body), Attachments: files}
	if err := m.Validate(); err != nil && !strings.Contains(err.Error(), "from address") {
		return m, dir, err
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return m, dir, err
	}
	if err := os.WriteFile(filepath.Join(dir, DraftName), raw, 0o600); err != nil {
		return m, dir, err
	}
	if m.From != "" {
		if eml, err := mail.Build(m, time.Now()); err == nil {
			_ = os.WriteFile(filepath.Join(dir, EmlName), eml, 0o600)
		}
	}
	_ = s.AddEvent(j.ID, "email:draft", "to "+m.To+": "+m.Subject)
	return m, dir, nil
}

// LoadDraft reads email.json back.
func LoadDraft(dir string) (mail.Message, error) {
	raw, err := os.ReadFile(filepath.Join(dir, DraftName))
	if err != nil {
		return mail.Message{}, err
	}
	var m mail.Message
	err = json.Unmarshal(raw, &m)
	return m, err
}

// DefaultAttachments picks the tailored resume if one exists, PDF before Markdown.
func DefaultAttachments(dir string) []string {
	for _, name := range []string{"resume.pdf", "resume.docx", "resume.md"} {
		if p := filepath.Join(dir, name); exists(p) {
			return []string{p}
		}
	}
	return nil
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// ExportPDF renders <dir>/<name>.md as <dir>/<name>.pdf: the ATS-safe copy the e-mail attaches first.
func ExportPDF(dir, name, title string) (string, int, error) {
	name = strings.TrimSuffix(filepath.Base(name), ".md")
	src := filepath.Join(dir, name+".md")
	md, err := os.ReadFile(src)
	if err != nil {
		return "", 0, fmt.Errorf("no %s.md in %s", name, dir)
	}
	if title == "" {
		title = pdfTitle(string(md), name)
	}
	out := pdf.Render(string(md), title, time.Now())
	dst := filepath.Join(dir, name+".pdf")
	if err := os.WriteFile(dst, out, 0o600); err != nil {
		return dst, 0, err
	}
	return dst, len(out), stampExport(dir, src, dst)
}

func pdfTitle(md, fallback string) string {
	for _, l := range strings.Split(md, "\n") {
		if t, ok := strings.CutPrefix(l, "# "); ok {
			return strings.TrimSpace(t)
		}
	}
	return fallback
}
