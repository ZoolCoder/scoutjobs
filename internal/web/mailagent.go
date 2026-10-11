package web

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/runner"
)

// agentSynced is when the inbox agent last finished a sync; the next one starts a day before it.
const agentSynced = "mail_agent_synced"

// agentView is the Inbox's "your AI agent" option and, once chosen, the agent's latest mail run.
type agentView struct {
	Agents []string // installed agents that can read mail
	Agent  string   // the one reading the mail now, "" when another inbox is used
	Run    *runner.Status
	Active bool
}

func (v *server) agentView(src, account string) *agentView {
	m := v.cfg.Runner
	if m == nil {
		return nil
	}
	a := &agentView{}
	for _, ag := range m.Agents() {
		if ag.Name != "custom" {
			a.Agents = append(a.Agents, ag.Name)
		}
	}
	if src == "agent" {
		a.Agent = account
		if a.Agent == "" || a.Agent == "auto" {
			if ag, err := m.MailAgent(""); err == nil {
				a.Agent = ag.Name
			}
		}
	}
	if r := m.Latest(runner.MailID); r != nil {
		snap := r.Snapshot()
		if n := len(snap.Log); n > 60 {
			snap.Log = snap.Log[n-60:]
		}
		a.Run, a.Active = &snap, snap.State == runner.Running
	}
	return a
}

// useAgent makes the chosen agent read the mail from now on.
func (v *server) useAgent(w http.ResponseWriter, r *http.Request) {
	if v.cfg.Runner == nil || sameOrigin(r) != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	ag, err := v.cfg.Runner.MailAgent(r.FormValue("agent"))
	if err == nil {
		err = errors.Join(v.s.SetSetting(runner.MailAgent, ag.Name), v.s.SetSetting(runner.MailVia, "agent"))
	}
	if err != nil {
		http.Redirect(w, r, withParam("/inbox", "err", err.Error())+"#connect", http.StatusSeeOther)
		return
	}
	msg := fmt.Sprintf("%s now reads your mail for ScoutJobs. Press Sync now to bring in the replies.", agentTitle(ag.Name))
	http.Redirect(w, r, withParam("/inbox", "msg", msg), http.StatusSeeOther)
}

// stopAgent goes back to the Google client or IMAP, when set up, or to no inbox.
func (v *server) stopAgent(w http.ResponseWriter, r *http.Request) {
	if sameOrigin(r) != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if err := v.s.SetSetting(runner.MailVia, ""); err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, withParam("/inbox", "msg", "Your AI agent no longer reads your mail for ScoutJobs.")+"#connect", http.StatusSeeOther)
}

// agentCheck reports the mail and calendar connectors an agent can reach, for the Inbox to show.
func (v *server) agentCheck(w http.ResponseWriter, r *http.Request) {
	if v.cfg.Runner == nil {
		writeJSON(w, 404, map[string]string{"error": "no runner"})
		return
	}
	ag, err := v.cfg.Runner.MailAgent(r.URL.Query().Get("agent"))
	if err != nil {
		writeJSON(w, 200, map[string]string{"error": err.Error()})
		return
	}
	conns, err := v.cfg.Runner.Connectors(r.Context(), ag, r.URL.Query().Get("fresh") != "")
	if err != nil {
		writeJSON(w, 200, map[string]string{"error": err.Error()})
		return
	}
	out := map[string]any{"agent": ag.Name, "connectors": conns}
	if mb, _ := runner.ReadyConnectors(conns); len(mb) == 0 {
		out["hint"] = runner.NoMailbox(ag.Name, conns)
	}
	writeJSON(w, 200, out)
}

func (v *server) agentRun(w http.ResponseWriter, r *http.Request) {
	if v.cfg.Runner != nil {
		if run := v.cfg.Runner.Latest(runner.MailID); run != nil {
			writeJSON(w, 200, run.Snapshot())
			return
		}
	}
	writeJSON(w, 404, map[string]string{"error": "no run"})
}

func (v *server) cancelAgent(w http.ResponseWriter, r *http.Request) {
	if v.cfg.Runner != nil && sameOrigin(r) == nil {
		if run := v.cfg.Runner.Latest(runner.MailID); run != nil {
			v.cfg.Runner.Cancel(run.ID)
		}
	}
	http.Redirect(w, r, "/inbox#agent", http.StatusSeeOther)
}

// startAgentSync has the agent read the mail since the last sync, at most two weeks back.
func (v *server) startAgentSync(agent string) error {
	since := time.Now().AddDate(0, 0, -14)
	if t, err := time.Parse(time.RFC3339, v.s.Setting(agentSynced)); err == nil && t.AddDate(0, 0, -1).After(since) {
		since = t.AddDate(0, 0, -1)
	}
	started := time.Now()
	run, err := v.cfg.Runner.StartMail(runner.MailTask{Kind: "sync", Since: since}, agent)
	if err != nil {
		return err
	}
	go func() {
		run.Wait()
		if run.Snapshot().State == runner.Done {
			_ = v.s.SetSetting(agentSynced, started.Format(time.RFC3339))
		}
	}()
	return nil
}

// fileDraft puts a job's e-mail into the mailbox's drafts: at once through the Gmail API, or by the
// AI agent in the background, in which case link is "" and note says where to follow it.
func (v *server) fileDraft(jobID int64, kind string) (link, note string, err error) {
	src, account := v.mailSourceAccount()
	if src == "agent" {
		if v.cfg.Mail.Compose == nil || v.cfg.Runner == nil {
			return "", "", errors.New("the AI agent is not available in this server")
		}
		t, err := v.cfg.Mail.Compose(jobID, kind)
		if err != nil {
			return "", "", err
		}
		if _, err := v.cfg.Runner.StartMail(t, account); err != nil {
			return "", "", err
		}
		return "", fmt.Sprintf("%s is filing the %s in your drafts; follow it on the Inbox. Nothing is sent.", agentTitle(account), t.Label), nil
	}
	if v.cfg.Mail.Draft == nil {
		return "", "", errors.New("no mailbox is connected")
	}
	link, err = v.cfg.Mail.Draft(jobID, kind)
	return link, "", err
}

func (v *server) mailSourceAccount() (string, string) {
	if v.cfg.Mail.Status == nil {
		return "", ""
	}
	return v.cfg.Mail.Status()
}

// canDraft is true when a job's e-mail can go to the drafts of the connected mailbox.
func (v *server) canDraft() bool {
	switch v.mailSource() {
	case "gmail":
		return v.cfg.Mail.Draft != nil
	case "agent":
		return v.cfg.Mail.Compose != nil && v.cfg.Runner != nil
	}
	return false
}

// draftLabel names the drafts button after the mailbox it fills.
func (v *server) draftLabel() string {
	if v.mailSource() == "gmail" {
		return "Draft in Gmail"
	}
	return "Draft in your mailbox"
}

func agentTitle(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
