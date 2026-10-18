package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfileExpand(t *testing.T) {
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users/ada/repos" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`[{"name":"queue","language":"Rust","topics":["kafka"],"description":"a Kafka consumer"},
			{"name":"forked","language":"Haskell","fork":true}]`))
	}))
	defer gh.Close()
	old := githubAPI
	githubAPI = gh.URL
	defer func() { githubAPI = old }()

	home := t.TempDir()
	t.Setenv("SCOUTJOBS_HOME", home)
	run(t, "init")
	profile := "# Profile\n- Links: https://github.com/ada\n\n## Skills\n- Strong: Go\n- Working: Python\n"
	os.WriteFile(filepath.Join(home, "profile.md"), []byte(profile), 0o600)
	os.MkdirAll(filepath.Join(home, "linkedin"), 0o700)
	os.WriteFile(filepath.Join(home, "cv", "resume.md"), []byte("Built Go services on Kubernetes and Kafka."), 0o600)
	os.WriteFile(filepath.Join(home, "linkedin", "Skills.csv"), []byte("Name\nPython\nKubernetes\n"), 0o600)

	out := run(t, "profile", "expand", "--json")
	for _, want := range []string{`{"skill":"kafka","sources":["cv/resume.md","github/queue"]}`, `"skill":"kubernetes","sources":["cv/resume.md","linkedin/Skills.csv"]`, `"skill":"rust"`} {
		if !strings.Contains(out, want) {
			t.Errorf("want %s in %s", want, out)
		}
	}
	for _, not := range []string{`"skill":"go"`, `"skill":"python"`, "haskell"} {
		if strings.Contains(out, not) {
			t.Errorf("%s is listed or forked, not new: %s", not, out)
		}
	}
	run(t, "profile", "expand", "--add", "kafka,haskell")
	b, _ := os.ReadFile(filepath.Join(home, "profile.md"))
	if !strings.Contains(string(b), "- Working: Python, kafka\n") {
		t.Errorf("profile after --add:\n%s", b)
	}
	if out := run(t, "profile", "expand", "--json"); strings.Contains(out, `"skill":"kafka"`) {
		t.Errorf("kafka is listed now: %s", out)
	}
}

func TestAppendWorking(t *testing.T) {
	if got := appendWorking("# P\n## Skills\n- Strong: Go\n", []string{"rust"}); got != "# P\n## Skills\n- Working: rust\n- Strong: Go\n" {
		t.Errorf("under heading: %q", got)
	}
	if got := appendWorking("# P", []string{"rust"}); got != "# P\n\n## Skills\n- Working: rust\n" {
		t.Errorf("no section: %q", got)
	}
}
