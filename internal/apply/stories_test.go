package apply

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const kafkaStory = `---
id: kafka-migration
title: "Moved billing to Kafka"
company_context: Acme (B2B SaaS)
role: Senior Engineer
timeline: 2022-01 to 2022-09
themes: [migration, leadership, kafka]
metrics:
  - cut invoice lag from 6h to 4m
  - "zero lost events"
last_modified: 2026-01-01
---

## Core Facts
Led five engineers; Go, Postgres, event streaming.

## Variants
### Variant: lead-migration
`

func TestStoriesParseAndMatch(t *testing.T) {
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "stories"), 0o755)
	_ = os.WriteFile(filepath.Join(home, "stories", "kafka-migration.md"), []byte(kafkaStory), 0o644)
	_ = os.WriteFile(filepath.Join(home, "stories", "onboarding.md"), []byte("## Core Facts\nMentored two juniors through onboarding.\n"), 0o644)
	st := Stories(home)
	if len(st) != 2 {
		t.Fatalf("stories = %d", len(st))
	}
	k := st[0]
	if k.Title != "Moved billing to Kafka" || k.Context != "Acme (B2B SaaS)" || strings.Join(k.Themes, ",") != "migration,leadership,kafka" ||
		strings.Join(k.Metrics, "|") != "cut invoice lag from 6h to 4m|zero lost events" || !strings.HasPrefix(k.Facts, "Led five engineers") {
		t.Errorf("story = %+v", k)
	}
	if st[1].ID != "onboarding" || st[1].Title != "onboarding" {
		t.Errorf("no front matter: %+v", st[1])
	}
	review := FitReview{Requirements: []Requirement{
		{Requirement: "Experience with Kafka and event streaming", Kind: "must"},
		{Requirement: "Mentoring junior engineers", Kind: "must"},
		{Requirement: "Rust", Kind: "must"},
		{Requirement: "Kafka", Kind: "nice"},
	}}
	var got []string
	for _, m := range MatchStories(st, review) {
		id := "-"
		if m.Story != nil {
			id = m.Story.ID
		}
		got = append(got, id)
	}
	if strings.Join(got, ",") != "kafka-migration,-,-" {
		t.Errorf("matches = %v", got)
	}
}
