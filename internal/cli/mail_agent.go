package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/apply"
	"github.com/zoolcoder/scoutjobs/internal/mail"
	"github.com/zoolcoder/scoutjobs/internal/runner"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

// mailAgentName is the agent that reads mail: the one chosen on the Inbox page, else config.yaml's agent.
func mailAgentName(s *store.Store, cfg Config) string {
	if a := s.Setting(runner.MailAgent); a != "" {
		return a
	}
	return cfg.Agent
}

// agentSync has the AI agent read the mail through its connector, here in the terminal.
func agentSync(s *store.Store, cfg Config, since time.Time, out io.Writer) error {
	if _, viaMCP := out.(*mcpOutput); viaMCP || os.Getenv("SCOUTJOBS_RUNNER") == "1" { // an agent asked: it reads the mail with its own tools
		return errors.New("you read the mail through your connector: log each reply with email_log")
	}
	m := runner.NewManager(newRunner(s, cfg))
	ag, err := m.MailAgent(mailAgentName(s, cfg))
	if err != nil {
		return err
	}
	ctx := context.Background()
	conns, err := m.Connectors(ctx, ag, true)
	if err != nil {
		return err
	}
	t := runner.MailTask{Kind: "sync", Since: since}
	run, err := runner.PlanMail(t, ag, conns)
	if err != nil {
		return err
	}
	m.ExecMail(ctx, run, ag, conns, t, out)
	if st := run.Snapshot(); st.State != runner.Done {
		return fmt.Errorf("inbox agent %s: %s", st.State, st.Error)
	}
	return nil
}

// draftTask is the e-mail the agent files as a draft: the application e-mail, or a proposed follow-up
// or thank-you that answers the conversation so far.
func draftTask(s *store.Store, cfg Config, j store.Job, kind string) (runner.MailTask, error) {
	m, _, err := loadDraft(s, j)
	label := "application to " + j.Company
	if kind != "" && kind != "application" {
		if !apply.ValidKind(kind) {
			return runner.MailTask{}, fmt.Errorf("unknown e-mail kind %q, want one of %s", kind, strings.Join(apply.ProposalKinds, ", "))
		}
		m, err = proposal(s, cfg, j, kind)
		label = kind + " to " + j.Company
		_, m.InReplyTo = s.JobThread(j.ID)
	}
	if err != nil {
		return runner.MailTask{}, err
	}
	if m.To == "" {
		return runner.MailTask{}, fmt.Errorf("the %s has no recipient: set the job's contact first", label)
	}
	if !strings.HasPrefix(m.InReplyTo, "<") { // a connector's own message id is no Message-ID header
		m.InReplyTo = ""
	}
	return runner.MailTask{Kind: "draft", JobID: j.ID, Draft: mail.Message{To: m.To, Subject: m.Subject, Body: m.Body, Attachments: m.Attachments, InReplyTo: m.InReplyTo}, Label: label}, nil
}
