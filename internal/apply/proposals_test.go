package apply

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/store"
)

func kinds(ps []Proposal) string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Kind)
	}
	return strings.Join(out, ",")
}

func TestProposalsFollowWhereTheJobStands(t *testing.T) {
	s, j := testJob(t)
	_ = os.WriteFile(filepath.Join(s.Home, "profile.md"), []byte("# Profile\n\n## Identity\n- Name: Jane Doe\n"), 0o600)
	status := func(st string) store.Job {
		_ = s.UpdateJob(j.ID, store.JobPatch{Status: &st})
		j, _ = s.JobByID(j.ID)
		return j
	}
	now := time.Now()

	j = status("applied")
	if got := kinds(Proposals(s, j, 10, now)); got != "" {
		t.Fatalf("fresh application proposes %q", got)
	}
	ps := Proposals(s, j, 10, now.Add(11*24*time.Hour))
	if kinds(ps) != "followup" {
		t.Fatalf("after 11 days %q", kinds(ps))
	}
	m := ps[0].Msg
	if m.To != "jobs@acme.test" || m.Subject != "Re: Application: Go Engineer at Acme Corp" || !strings.HasSuffix(m.Body, "Jane Doe\n") || !strings.Contains(m.Body, "11 days ago") {
		t.Errorf("follow-up %+v", m)
	}
	// a reply from the company: nothing to chase
	_, _ = s.LogMail(store.Mail{MessageID: "<r1>", JobID: j.ID, Dir: "in", From: "Sam <sam@acme.test>", Subject: "Your application", Date: now.Add(24 * time.Hour).Format(time.RFC3339)})
	if got := kinds(Proposals(s, j, 10, now.Add(11*24*time.Hour))); got != "" {
		t.Fatalf("after a reply %q", got)
	}

	j = status("interview")
	_, _ = s.AddInterview(store.Interview{JobID: j.ID, Stage: "system_design", At: now.Add(-2 * time.Hour).Format("2006-01-02T15:04"), Interviewer: "Sam"})
	ps = Proposals(s, j, 10, now)
	if kinds(ps) != "thanks" || ps[0].Msg.To != "sam@acme.test" || !strings.HasPrefix(ps[0].Msg.Body, "Dear Sam,") || !strings.Contains(ps[0].Msg.Body, "system design interview") {
		t.Fatalf("thanks %+v", ps)
	}

	j = status("offer")
	ps = Proposals(s, j, 10, now)
	if kinds(ps) != "offer-accept,offer-negotiate,offer-decline" || ps[0].Msg.Subject != "Re: Your application" {
		t.Fatalf("offer %q %+v", kinds(ps), ps[0].Msg)
	}
	edited := ps[1].Msg
	edited.Body = "I was hoping for 95k."
	if err := SaveProposal(s, j, "offer-negotiate", edited); err != nil {
		t.Fatal(err)
	}
	ps = Proposals(s, j, 10, now)
	if !ps[1].Saved || ps[1].Msg.Body != "I was hoping for 95k." || ps[0].Saved {
		t.Fatalf("saved %+v", ps[1])
	}
	if err := SaveProposal(s, j, "../evil", edited); err == nil {
		t.Error("unknown kind saved")
	}
}
