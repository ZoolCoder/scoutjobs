package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zoolcoder/scoutjobs/internal/country"
	"github.com/zoolcoder/scoutjobs/internal/search"
)

func TestCountrySet(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SCOUTJOBS_HOME", home)
	run(t, "init")
	run(t, "pref", "set", "timezone", "UTC+0 to UTC+2", "--source", "stated")
	setConfigValue(filepath.Join(home, "config.yaml"), "search_boards", "greenhouse:stripe")

	out := run(t, "country", "show", "denmark")
	if !strings.Contains(out, "would set currency: DKK") || strings.Contains(LoadConfig(home).Currency, "DKK") {
		t.Fatalf("show must not write: %s", out)
	}
	out = run(t, "country", "set", "Danmark")
	cfg := LoadConfig(home)
	if cfg.Country != "DK" || cfg.Currency != "DKK" || strings.Join(cfg.SearchBoards, ",") != "greenhouse:stripe,jobindex:dk,jobbank:dk,jobdanmark:dk" {
		t.Fatalf("config: %+v\n%s", cfg, out)
	}
	b, _ := os.ReadFile(filepath.Join(home, "config.yaml"))
	if !strings.Contains(string(b), "currency: DKK           # salaries") {
		t.Errorf("the comment must stay:\n%s", b)
	}
	for _, want := range []string{"proposed locations = Denmark", "kept: you stated it timezone", "salary floor, in DKK", "EU/EEA citizens need no permit"} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in\n%s", want, out)
		}
	}
	review := run(t, "pref", "review")
	if !strings.Contains(review, "cv_norms") || strings.Contains(review, "salary") {
		t.Errorf("proposals: %s", review)
	}
	run(t, "country", "set", "DK") // twice keeps one copy of each board
	if n := len(LoadConfig(home).SearchBoards); n != 4 {
		t.Errorf("boards duplicated: %d", n)
	}

	var e bytes.Buffer
	if err := Run([]string{"country", "set", "Atlantis"}, &e); err == nil {
		t.Error("an unknown country must fail")
	}
	out = run(t, "country", "set", "Kenya") // a region the location filter knows, without a table row
	if cfg := LoadConfig(home); cfg.Country != "Kenya" || cfg.Currency != "DKK" || !strings.Contains(out, "no local boards") {
		t.Errorf("fallback: %+v\n%s", cfg, out)
	}
}

// every country in the table must be one the location filter understands
func TestCountryNamesMatchRegions(t *testing.T) {
	for _, c := range country.All() {
		if _, ok := search.Regions[strings.ToLower(c.Name)]; !ok {
			t.Errorf("%s: %q missing from search.Regions", c.Code, c.Name)
		}
	}
}
