package apply

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zoolcoder/scoutjobs/internal/store"
)

func testJob(t *testing.T) (*store.Store, store.Job) {
	t.Helper()
	t.Setenv("SCOUTJOBS_HOME", t.TempDir())
	s, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	j, _, err := s.UpsertJob(store.Job{Company: "Acme Corp", Title: "Go Engineer", Contact: "jobs@acme.test"})
	if err != nil {
		t.Fatal(err)
	}
	return s, j
}

func TestDirAndFiles(t *testing.T) {
	s, j := testJob(t)
	dir := Dir(s, j)
	if filepath.Base(dir) != "acme-corp_go-engineer" || filepath.Dir(dir) != filepath.Join(s.Home, "applications") {
		t.Errorf("dir: %s", dir)
	}
	if Files(dir) != nil {
		t.Error("missing folder should list nothing")
	}
	if _, err := WriteFile(dir, "../../escape.md", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "escape.md")); err != nil {
		t.Error("WriteFile must reduce the name to its base")
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := Files(dir)
	if len(files) != 1 || files[0].Name != "escape.md" || files[0].Size != 1 {
		t.Errorf("files: %+v", files)
	}
}

func TestReadWriteFileRules(t *testing.T) {
	s, j := testJob(t)
	dir := Dir(s, j)
	if _, err := WriteFile(dir, "resume.pdf", []byte("%PDF")); err == nil {
		t.Error("binary write must be refused")
	}
	if _, err := WriteFile(dir, "big.md", make([]byte, MaxFile+1)); err == nil {
		t.Error("oversized write must be refused")
	}
	if _, err := WriteFile(dir, "resume.md", []byte("# Me")); err != nil {
		t.Fatal(err)
	}
	b, name, err := ReadFile(dir, "resume.md")
	if err != nil || name != "resume.md" || string(b) != "# Me" {
		t.Errorf("read: %q %s %v", b, name, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "resume.pdf"), []byte("%PDF"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadFile(dir, "resume.pdf"); err == nil {
		t.Error("pdf must not be read as text")
	}
	if _, _, err := ReadFile(dir, "missing.md"); err == nil {
		t.Error("missing file must error")
	}
}

func TestDraftAndStatus(t *testing.T) {
	s, j := testJob(t)
	dir := Dir(s, j)
	if _, _, err := Draft(s, j, "me@test", DraftOptions{}); err == nil || !strings.Contains(err.Error(), "cover letter first") {
		t.Errorf("draft without a letter: %v", err)
	}
	if _, err := WriteFile(dir, "cover-letter.md", []byte("Dear team,\n\nhire me.")); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteFile(dir, "resume.md", []byte("# Me\n\n- Go")); err != nil {
		t.Fatal(err)
	}
	k, _, _ := Status(s, j, nil)
	if !k.Resume || !k.Cover || k.Sent || k.Draft != nil || k.Done != 2 || k.Of != 4 {
		t.Errorf("kit before draft: %+v", k)
	}

	m, _, err := Draft(s, j, "", DraftOptions{To: "hr@acme.test", Attach: []string{"resume.md", filepath.Join(dir, "cover-letter.md")}})
	if err != nil {
		t.Fatal(err)
	}
	if m.Subject != "Application: Go Engineer at Acme Corp" || m.To != "hr@acme.test" || len(m.Attachments) != 3 {
		t.Errorf("draft: %+v", m)
	}
	if _, err := os.Stat(filepath.Join(dir, EmlName)); err == nil {
		t.Error("no .eml without a sender")
	}
	if got, _ := s.JobByID(j.ID); got.Contact != "hr@acme.test" {
		t.Errorf("contact not recorded: %+v", got)
	}

	m2, _, err := Draft(s, j, "me@test", DraftOptions{Subject: "Hello", Body: "cover-letter.md"})
	if err != nil || m2.From != "me@test" || m2.Subject != "Hello" {
		t.Fatalf("second draft: %+v %v", m2, err)
	}
	if _, err := os.Stat(filepath.Join(dir, EmlName)); err != nil {
		t.Error("eml expected once a sender is known")
	}
	loaded, err := LoadDraft(dir)
	if err != nil || loaded.Subject != "Hello" {
		t.Errorf("load: %+v %v", loaded, err)
	}

	if _, err := s.AddCVCheck(store.CVCheck{File: "resume.md", Score: 80, ATSScore: 90, JobID: j.ID}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddEvent(j.ID, "email:sent", "to hr"); err != nil {
		t.Fatal(err)
	}
	events, _ := s.Events(j.ID, 0)
	k, _, files := Status(s, j, events)
	if !k.Sent || k.Draft == nil || k.ATS == nil || k.ATS.ATSScore != 90 || k.Done != 4 || len(files) != 4 {
		t.Errorf("kit after send: %+v files=%d", k, len(files))
	}

	if _, _, err := Draft(s, j, "me@test", DraftOptions{Attach: []string{"nope.pdf"}}); err == nil {
		t.Error("missing attachment must fail validation")
	}
}

func TestDefaultAttachmentsAndExport(t *testing.T) {
	s, j := testJob(t)
	dir := Dir(s, j)
	if DefaultAttachments(dir) != nil {
		t.Error("empty folder has no attachment")
	}
	if _, err := WriteFile(dir, "resume.md", []byte("# Jane Doe\n\nGo since 2015.")); err != nil {
		t.Fatal(err)
	}
	if got := DefaultAttachments(dir); len(got) != 1 || filepath.Base(got[0]) != "resume.md" {
		t.Errorf("markdown fallback: %v", got)
	}
	dst, n, err := ExportPDF(dir, "resume.md", "")
	if err != nil || n == 0 || filepath.Base(dst) != "resume.pdf" {
		t.Fatalf("export: %s %d %v", dst, n, err)
	}
	if got := DefaultAttachments(dir); filepath.Base(got[0]) != "resume.pdf" {
		t.Errorf("pdf should win: %v", got)
	}
	if _, _, err := ExportPDF(dir, "missing", ""); err == nil {
		t.Error("missing source must fail")
	}
	if pdfTitle("intro\n# Title here\n", "x") != "Title here" || pdfTitle("no heading", "fallback") != "fallback" {
		t.Error("pdfTitle")
	}
}
