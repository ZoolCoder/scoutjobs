package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLinkedInImportAndContacts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SCOUTJOBS_HOME", home)
	run(t, "init")
	run(t, "job", "add", "--company", "Stripe, Inc.", "--title", "Go Engineer")
	run(t, "contacts", "1") // nothing imported yet: prints a hint, no error

	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("Profile.csv", "First Name,Last Name,Headline\nAda,Lovelace,Go engineer\n")
	write("Connections.csv", "Notes:\n\"Some e-mails are missing.\"\n\nFirst Name,Last Name,URL,Email Address,Company,Position,Connected On\nBo,Chen,https://www.linkedin.com/in/bo,,Stripe,Staff Engineer,15 Mar 2023\n")

	out := run(t, "linkedin", "import", dir)
	if !strings.Contains(out, "1 connections") {
		t.Errorf("import: %s", out)
	}
	md, err := os.ReadFile(filepath.Join(home, "linkedin", "profile.md"))
	if err != nil || !strings.Contains(string(md), "Ada Lovelace") {
		t.Fatalf("profile.md: %v %s", err, md)
	}
	if out := run(t, "contacts", "1", "--json"); !strings.Contains(out, `"name": "Bo Chen"`) && !strings.Contains(out, `"name":"Bo Chen"`) {
		t.Errorf("contacts: %s", out)
	}
	if out := run(t, "contacts", "--company", "Acme"); !strings.Contains(out, "nobody") {
		t.Errorf("contacts --company: %s", out)
	}
	if err := Run([]string{"linkedin", "import", t.TempDir()}, &strings.Builder{}); err == nil {
		t.Error("empty folder accepted")
	}
}
