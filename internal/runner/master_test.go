package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/apply"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

const masterAgent = `#!/bin/sh
cat > prompt.txt
grep -q "Step [0-9], profile from your cv" prompt.txt && echo "- Kubernetes, from the CV" >> ../profile.md
grep -q "Step [0-9], main cv" prompt.txt && echo "# Jane Doe v2" > resume.md
grep -q "Step [0-9], main cover letter" prompt.txt && echo "Dear [Company] team" > cover-letter.md
true
`

func TestMasterWritesMainDocumentsInCVAndKeepsThePrevious(t *testing.T) {
	rn, _ := setup(t, masterAgent)
	cv := filepath.Join(rn.Store.Home, "cv")
	_ = os.MkdirAll(cv, 0o700)
	_ = os.WriteFile(filepath.Join(cv, "resume.md"), []byte("# Jane Doe v1\n"), 0o600)

	r, ag, err := rn.PlanMaster(Request{})
	if err != nil {
		t.Fatal(err)
	}
	if got := states(r.Snapshot()); got != "sync-profile=skipped main-cv=skipped main-cover=pending" {
		t.Fatalf("plan %s", got)
	}
	rn.ExecMaster(context.Background(), r, ag, nil)
	if got := r.Snapshot(); got.State != Done || states(got) != "sync-profile=skipped main-cv=skipped main-cover=done" {
		t.Fatalf("%s %s %s %v", got.State, got.Error, states(got), got.Log)
	}

	r, ag, _ = rn.PlanMaster(Request{Force: true, Steps: []string{"main-cv"}})
	rn.ExecMaster(context.Background(), r, ag, nil)
	if got := r.Snapshot(); got.State != Done {
		t.Fatalf("%s %s %v", got.State, got.Error, got.Log)
	}
	now, _ := os.ReadFile(filepath.Join(cv, "resume.md"))
	prev, _ := os.ReadFile(filepath.Join(cv, "resume.prev.md"))
	if !strings.Contains(string(now), "v2") || !strings.Contains(string(prev), "v1") {
		t.Errorf("resume %q prev %q", now, prev)
	}
	prompt, _ := os.ReadFile(filepath.Join(cv, "prompt.txt"))
	if !strings.Contains(string(prompt), "===== skill resume-ats-optimizer") || strings.Contains(string(prompt), "cover-letter-generator") {
		t.Errorf("prompt:\n%.300s", prompt)
	}
}

func TestProfileSyncReadsTheUsersPDFCV(t *testing.T) {
	rn, _ := setup(t, masterAgent)
	cv := filepath.Join(rn.Store.Home, "cv")
	profile := filepath.Join(rn.Store.Home, "profile.md")
	_ = os.MkdirAll(cv, 0o700)
	_ = os.WriteFile(profile, []byte("# Jane\n"), 0o600)
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(profile, old, old)
	_ = os.WriteFile(filepath.Join(cv, "Jane CV 2026.pdf"), []byte("%PDF-1.4"), 0o600)
	if got := SourceCV(cv); got != "Jane CV 2026.pdf" {
		t.Fatalf("source CV %q", got)
	}

	r, ag, err := rn.PlanMaster(Request{Steps: []string{"sync-profile", "main-cv"}})
	if err != nil {
		t.Fatal(err)
	}
	rn.ExecMaster(context.Background(), r, ag, nil)
	if got := r.Snapshot(); got.State != Done || states(got) != "sync-profile=done main-cv=done" {
		t.Fatalf("%s %s %s %v", got.State, got.Error, states(got), got.Log)
	}
	now, _ := os.ReadFile(profile)
	prev, _ := os.ReadFile(filepath.Join(rn.Store.Home, "profile.prev.md"))
	if !strings.Contains(string(now), "Kubernetes, from the CV") || string(prev) != "# Jane\n" {
		t.Errorf("profile %q prev %q", now, prev)
	}
	prompt, _ := os.ReadFile(filepath.Join(cv, "prompt.txt"))
	if !strings.Contains(string(prompt), "Your CV: cv/Jane CV 2026.pdf") || !strings.Contains(string(prompt), "sets the style and format") {
		t.Errorf("prompt:\n%.600s", prompt)
	}
	if done := rn.MasterDone(); !done["sync-profile"] {
		t.Error("profile newer than the CV counts as synced")
	}
}

func TestQueuePreparesCandidatesOneAtATime(t *testing.T) {
	rn, acme := setup(t, fakeAgent)
	score := int64(80)
	_ = rn.Store.UpdateJob(acme.ID, store.JobPatch{FitScore: &score})
	low, _, _ := rn.Store.UpsertJob(store.Job{Company: "Low", Title: "Go Engineer", FitScore: 20})
	done, _, _ := rn.Store.UpsertJob(store.Job{Company: "Done", Title: "Go Engineer", FitScore: 90})
	dir := apply.Dir(rn.Store, done)
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "resume.md"), []byte("x"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "cover-letter.md"), []byte("x"), 0o600)

	m := NewManager(rn)
	jobs, err := m.Candidates(60)
	if err != nil || len(jobs) != 1 || jobs[0].ID != acme.ID {
		t.Fatalf("candidates %v %v (low %d)", jobs, err, low.ID)
	}
	req := Request{Steps: []string{"tailor", "cover"}}
	if n := m.Queue(jobs, req); n != 1 {
		t.Fatalf("queued %d", n)
	}
	if n := m.Queue(jobs, req); n != 0 && m.Latest(acme.ID) == nil {
		t.Fatalf("queued twice")
	}
	for i := 0; i < 200 && (m.Latest(acme.ID) == nil || len(m.Waiting()) > 0); i++ {
		waitTick()
	}
	r := m.Latest(acme.ID)
	if r == nil {
		t.Fatal("never started")
	}
	r.Wait()
	if got := r.Snapshot(); got.State != Done {
		t.Fatalf("%s %s %v", got.State, got.Error, got.Log)
	}
}

func waitTick() { time.Sleep(10 * time.Millisecond) }
