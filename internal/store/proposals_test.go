package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProposalLifecycle(t *testing.T) {
	s := open(t)
	if err := s.SetPreference(Preference{Key: "remote", Value: "remote only", Source: "stated", Confidence: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.ProposePreference(Proposal{Key: "remote", Value: "hybrid ok", Support: 9}); !errors.Is(err, ErrStated) {
		t.Errorf("proposal over a stated key: %v", err)
	}
	if err := s.SetPreference(Preference{Key: "remote", Value: "hybrid", Source: "learned"}); !errors.Is(err, ErrStated) {
		t.Errorf("learned set over a stated key: %v", err)
	}

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.SetPreference(Preference{Key: "stack_avoid", Value: "php", Confidence: 0.5}))
	must(s.ProposePreference(Proposal{Key: "stack_avoid", Value: "java", Support: 5, Confidence: 0.99, Evidence: "5 skips mention Java"}))
	must(s.ProposePreference(Proposal{Key: "company_size", Value: "50-500", Support: 3}))
	ps, _ := s.Proposals(false)
	if len(ps) != 2 || ps[0].Key != "stack_avoid" || ps[0].Current != "php" || ps[0].Confidence != 0.95 {
		t.Fatalf("pending: %+v", ps)
	}
	if prefs, _ := s.Preferences(); len(prefs) != 2 {
		t.Errorf("a proposal leaked into preferences: %+v", prefs)
	}

	p, err := s.ConfirmProposal("stack_avoid", "")
	if err != nil || p.Value != "java" || p.Source != "learned" || p.Confidence != 0.95 {
		t.Fatalf("confirm: %+v %v", p, err)
	}
	md, _ := os.ReadFile(filepath.Join(s.Home, "preferences.md"))
	if !strings.Contains(string(md), "**stack_avoid**: java") {
		t.Errorf("preferences.md not re-rendered:\n%s", md)
	}
	if _, err := s.ConfirmProposal("stack_avoid", ""); !errors.Is(err, ErrNoProposal) {
		t.Errorf("confirmed twice: %v", err)
	}

	must(s.RejectProposal("company_size", "size does not matter"))
	if err := s.ProposePreference(Proposal{Key: "company_size", Value: "50-500", Support: 3}); err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Errorf("re-proposed a rejected value without new evidence: %v", err)
	}
	must(s.ProposePreference(Proposal{Key: "company_size", Value: "50-500", Support: 6}))
	p, err = s.ConfirmProposal("company_size", "under 300")
	if err != nil || p.Value != "under 300" || p.Source != "stated" || p.Confidence != 1 {
		t.Fatalf("edit: %+v %v", p, err)
	}

	evs, _ := s.Events(0, 20)
	var kinds []string
	for _, e := range evs {
		kinds = append(kinds, e.Kind)
	}
	for _, k := range []string{"pref:proposed", "pref:confirmed", "pref:rejected", "pref:edited"} {
		if !strings.Contains(strings.Join(kinds, " "), k) {
			t.Errorf("no %s event in %v", k, kinds)
		}
	}
}
