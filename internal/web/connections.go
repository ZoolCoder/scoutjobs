package web

import (
	"fmt"
	"net/http"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/runner"
	"github.com/zoolcoder/scoutjobs/internal/search"
)

// calendarSynced is when the agent last finished a calendar sync.
const calendarSynced = "calendar_agent_synced"

// remoteSites are the remote job sites Find jobs can add to a search.
var remoteSites = []string{"Remotive", "RemoteOK", "Arbeitnow", "Jobicy", "Himalayas", "HN Who is hiring"}

// connectionsView is the Connections page: every outside tool ScoutJobs works with, in one place.
type connectionsView struct {
	Agents       []string // installed AI assistants, the default first
	Default      string
	Mail         string // gmail | imap | agent | ""
	MailAccount  string
	Google       googleView
	AI           *agentView
	Calendar     string // google | agent | ""
	GoogleCal    string // on | readonly | reconnect | setup | ""
	CalAgent     string
	CalRun       *runner.Status
	CalActive    bool
	CalSynced    string
	FeedURL      string
	FeedNote     string
	Boards       []string
	RemoteSites  []string
	SearchRemote bool
	Msg, Err     string
}

func (v *server) connections(w http.ResponseWriter, r *http.Request) {
	src, account := v.mailSourceAccount()
	c := connectionsView{Agents: v.agentNames(), Mail: src, MailAccount: account, Google: v.googleView(), AI: v.agentView(src, account),
		Boards: search.Boards, RemoteSites: remoteSites, SearchRemote: v.cfg.SearchRemote, CalSynced: v.s.Setting(calendarSynced),
		Msg: r.URL.Query().Get("msg"), Err: r.URL.Query().Get("err")}
	if len(c.Agents) > 0 {
		c.Default = c.Agents[0]
	}
	if g := v.gmailClient(); g != nil {
		switch {
		case g.CanWriteCalendar():
			c.GoogleCal = "on"
		case g.HasCalendar():
			c.GoogleCal = "readonly"
		case g.Connected():
			c.GoogleCal = "reconnect"
		default:
			c.GoogleCal = "setup"
		}
	}
	if v.cfg.Runner != nil {
		c.CalAgent = v.calendarAgent()
	}
	switch {
	case v.s.Setting(runner.CalendarVia) == "agent" && c.CalAgent != "":
		c.Calendar = "agent"
	case c.GoogleCal == "on":
		c.Calendar = "google"
	}
	if m := v.cfg.Runner; m != nil {
		if run := m.Latest(runner.MailID); run != nil {
			if snap := run.Snapshot(); len(snap.Steps) > 0 && snap.Steps[0].Name == "mail-calendar" {
				c.CalRun, c.CalActive = &snap, snap.State == runner.Running
			}
		}
	}
	c.FeedURL, c.FeedNote = v.feedURL(r)
	v.render(w, r, "connections.html", "connections", c)
}

// agentNames lists the installed AI assistants, the one picked on the Connections page first, so a
// form's agent menu starts on it.
func (v *server) agentNames() []string {
	m := v.cfg.Runner
	if m == nil {
		return nil
	}
	var names []string
	for _, a := range m.Agents() {
		names = append(names, a.Name)
	}
	pick := v.s.Setting(runner.DefaultAgent)
	if pick == "" {
		pick = m.Agent
	}
	for i, n := range names {
		if n == pick {
			copy(names[1:i+1], names[:i])
			names[0] = n
		}
	}
	return names
}

// calendarAgent is the agent that syncs the calendar: the one reading the mail, else the default.
func (v *server) calendarAgent() string {
	if src, account := v.mailSourceAccount(); src == "agent" && account != "" && account != "auto" {
		return account
	}
	if ag, err := v.cfg.Runner.MailAgent(v.s.Setting(runner.DefaultAgent)); err == nil {
		return ag.Name
	}
	return ""
}

// setDefaultAgent picks the AI assistant that writes the drafts.
func (v *server) setDefaultAgent(w http.ResponseWriter, r *http.Request) {
	if v.cfg.Runner == nil || sameOrigin(r) != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	name := r.FormValue("agent")
	if _, ok := runner.Pick(v.cfg.Runner.Agents(), name); !ok || name == "" {
		http.Redirect(w, r, withParam("/connections", "err", fmt.Sprintf("%q is not installed on this machine", name)), http.StatusSeeOther)
		return
	}
	if err := v.s.SetSetting(runner.DefaultAgent, name); err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, withParam("/connections", "msg", agentTitle(name)+" now writes your drafts."), http.StatusSeeOther)
}

// setCalendarVia picks how the interviews reach the calendar: through the AI assistant, or not.
func (v *server) setCalendarVia(w http.ResponseWriter, r *http.Request) {
	if sameOrigin(r) != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	via := r.FormValue("via")
	if via != "agent" {
		via = ""
	}
	if err := v.s.SetSetting(runner.CalendarVia, via); err != nil {
		fail(w, err)
		return
	}
	msg := "Your AI assistant no longer syncs your calendar."
	if via == "agent" {
		msg = "Your AI assistant now syncs your interviews with your calendar. Press Sync now."
	}
	http.Redirect(w, r, withParam("/connections", "msg", msg)+"#calendar", http.StatusSeeOther)
}

// syncCalendarAgent has the AI assistant sync the interviews with the calendar, both ways.
func (v *server) syncCalendarAgent(w http.ResponseWriter, r *http.Request) {
	if v.cfg.Runner == nil || sameOrigin(r) != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	dest := back(r, "/connections")
	agent := v.calendarAgent()
	if agent == "" {
		http.Redirect(w, r, withParam(dest, "err", "install Claude Code or Codex to sync the calendar through it"), http.StatusSeeOther)
		return
	}
	started := time.Now()
	run, err := v.cfg.Runner.StartMail(runner.MailTask{Kind: "calendar", Since: started}, agent)
	if err != nil {
		http.Redirect(w, r, withParam(dest, "err", err.Error()), http.StatusSeeOther)
		return
	}
	go func() {
		run.Wait()
		if run.Snapshot().State == runner.Done {
			_ = v.s.SetSetting(calendarSynced, started.Format(time.RFC3339))
		}
	}()
	http.Redirect(w, r, "/connections#calendar", http.StatusSeeOther)
}
