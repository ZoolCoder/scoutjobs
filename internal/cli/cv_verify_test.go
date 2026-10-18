package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCVVerify(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SCOUTJOBS_HOME", home)
	run(t, "init")
	body := "# Ada Lovelace\nada@example.test\n\n## Experience\n### Senior Go Developer, Acme\n2019 – 2023\n" +
		strings.Repeat("- Built payment services in Go and PostgreSQL that handled a million requests a day.\n", 8)
	os.MkdirAll(filepath.Join(home, "cv"), 0o700)
	os.WriteFile(filepath.Join(home, "cv", "resume.md"), []byte(body), 0o600)
	run(t, "cv", "export")

	var b bytes.Buffer
	err := Run([]string{"cv", "verify", "--contains", "Senior Go Developer", "--contains", "Kubernetes", "--json"}, &b)
	out := b.String()
	if !strings.Contains(out, `"name":"pages","ok":true`) {
		t.Errorf("page check: %s", out)
	}
	if _, look := exec.LookPath("pdftotext"); look != nil {
		t.Skip("pdftotext not installed: text checks are skipped")
	}
	for _, want := range []string{`"name":"text layer","ok":true`, `"name":"e-mail","ok":true`, `"name":"contains Senior Go Developer","ok":true`,
		`"name":"contains Kubernetes","ok":false`, `"name":"date ranges","ok":false`} {
		if !strings.Contains(out, want) {
			t.Errorf("want %s in %s", want, out)
		}
	}
	if err == nil || !strings.Contains(err.Error(), "2 of 6 checks failed") {
		t.Errorf("failed checks must fail the command: %v", err)
	}
	if err := Run([]string{"cv", "verify", "nope.pdf"}, &b); err == nil || !strings.Contains(err.Error(), "cv export") {
		t.Errorf("missing PDF: %v", err)
	}
}
