package apply

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKeywordGaps(t *testing.T) {
	dir := t.TempDir()
	if KeywordGaps(dir, "k8s") != nil {
		t.Error("gaps before resume.md exists")
	}
	_ = os.WriteFile(filepath.Join(dir, "resume.md"), []byte("Built **Go services** on AWS.\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, ReviewName), []byte(`{"score":70,"requirements":[
		{"requirement":"Go","kind":"must","strength":"direct","evidence":"profile: Acme","wording":"Go services"},
		{"requirement":"Kafka","kind":"must","strength":"adjacent","evidence":"profile: Initech queues","wording":"event streaming with Kafka"},
		{"requirement":"Rust","kind":"must","strength":"gap"},
		{"requirement":"Scala","kind":"nice","strength":"gap"}]}`), 0o644)
	gaps := KeywordGaps(dir, "quantify impact; add Terraform")
	var got []string
	for _, g := range gaps {
		got = append(got, g.Requirement+"/"+g.Source+"/"+map[bool]string{true: "add", false: "no"}[g.Addable()])
	}
	want := "Kafka/fit review/add,Rust/fit review/no,quantify impact/CV check/no,add Terraform/CV check/no"
	if strings.Join(got, ",") != want {
		t.Errorf("gaps = %s", strings.Join(got, ","))
	}
	task := KeywordTask(gaps)
	if !strings.Contains(task, `"event streaming with Kafka"`) || strings.Contains(task, "Rust") || strings.Contains(task, "Terraform") {
		t.Errorf("task = %s", task)
	}
	if KeywordTask(gaps[1:]) != "" {
		t.Error("task without addable gaps")
	}
}
