package linkedin

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

var export = fstest.MapFS{
	"Basic_LinkedInDataExport/Profile.csv":   {Data: []byte("\xef\xbb\xbfFirst Name,Last Name,Headline,Summary,Industry,Geo Location\nAda,Lovelace,Go engineer,Builds things,Software,Berlin\n")},
	"Basic_LinkedInDataExport/Positions.csv": {Data: []byte("Company Name,Title,Description,Location,Started On,Finished On\nAcme,Engineer,\"Built the API, then the queue\",Berlin,Jan 2020,\n")},
	"Basic_LinkedInDataExport/Skills.csv":    {Data: []byte("Name\nGo\nPostgreSQL\n")},
	"Basic_LinkedInDataExport/Connections.csv": {Data: []byte("Notes:\n\"When exporting your connection data, you may notice that some of the email addresses are missing.\"\n\n" +
		"First Name,Last Name,URL,Email Address,Company,Position,Connected On\nBo,Chen,https://www.linkedin.com/in/bo,,Stripe,Staff Engineer,15 Mar 2023\n,,,,,,\n")},
}

func TestParse(t *testing.T) {
	ex, err := parse(export)
	if err != nil {
		t.Fatal(err)
	}
	if ex.Profile.FirstName != "Ada" || ex.Profile.Location != "Berlin" {
		t.Errorf("profile %+v", ex.Profile)
	}
	if len(ex.Positions) != 1 || ex.Positions[0].Description != "Built the API, then the queue" {
		t.Errorf("positions %+v", ex.Positions)
	}
	if strings.Join(ex.Skills, ",") != "Go,PostgreSQL" {
		t.Errorf("skills %v", ex.Skills)
	}
	if len(ex.Connections) != 1 || ex.Connections[0].ConnectedOn != "2023-03-15" || ex.Connections[0].Company != "Stripe" {
		t.Errorf("connections %+v", ex.Connections)
	}
	md := ex.Markdown()
	for _, w := range []string{"- Name: Ada Lovelace", "### Engineer, Acme (Jan 2020 to present)"} {
		if !strings.Contains(md, w) {
			t.Errorf("markdown missing %q:\n%s", w, md)
		}
	}
	if _, err := parse(fstest.MapFS{"x.csv": {Data: []byte("a,b\n1,2\n")}}); err == nil {
		t.Error("unrelated folder accepted")
	}
}

func TestReadZip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "export.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, file := range export {
		w, _ := zw.Create(name)
		_, _ = w.Write(file.Data)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	ex, err := Read(path)
	if err != nil || len(ex.Connections) != 1 {
		t.Fatalf("zip: %v %+v", err, ex)
	}
	if _, err := Read(filepath.Join(t.TempDir(), "missing.zip")); err == nil {
		t.Error("missing file accepted")
	}
}
