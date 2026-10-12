package web

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/runner"
	"github.com/zoolcoder/scoutjobs/internal/search"
)

// calendarSynced is when the agent last finished a calendar sync.
const calendarSynced = "calendar_agent_synced"

// calendarDays is how many days ahead the agent's calendar sync looks: fewer is quicker.
const calendarDays = "calendar_agent_days"

var calendarPeriods = []int{7, 14, 30}

func (v *server) calendarWindow() int {
	n, _ := strconv.Atoi(v.s.Setting(calendarDays))
	if !slices.Contains(calendarPeriods, n) {
		return 14
	}
	return n
}

// sourceTitles names the job sources in plain words.
var sourceTitles = map[string]string{
	"remotive": "Remotive", "remoteok": "RemoteOK", "arbeitnow": "Arbeitnow", "jobicy": "Jobicy", "himalayas": "Himalayas", "hn": "HN Who is hiring",
	"greenhouse": "Greenhouse", "lever": "Lever", "ashby": "Ashby", "workable": "Workable", "smartrecruiters": "SmartRecruiters",
	"personio": "Personio", "recruitee": "Recruitee", "bamboohr": "BambooHR", "rss": "RSS feed",
}

// option is one choice on a Connections card.
type option struct {
	Value, Title, Note string
	On                 bool // in use now
	Off                bool // cannot be picked yet; Note says why
}

// source is one job source row: a remote board with its switch, or a careers-page kind.
type source struct {
	Name, Title string
	On          bool
}

// connectionsView is the Connections page: every outside tool ScoutJobs works with, in one place.
type connectionsView struct {
	AI, Mail, Cal  []option
	AIOn, MailOn   bool
	CalOn          string // agent | google | apple | ""
	MailOff        string // where Disconnect posts, "" when the mailbox cannot be disconnected here
	CalSync        string // where Sync now posts, "" when nothing syncs
	CalSynced      string
	CalDays        int
	CalPeriods     []int
	FeedURL        string
	FeedNote       string
	Sync           *runner.Status // the calendar sync the page waits on
	Ended          *runner.Status // the calendar sync that just ended, shown once
	Summary        string
	Tab            string // remote | careers
	Remote, Career []source
	SearchRemote   bool
	Msg, Err       string
}

func (v *server) connections(w http.ResponseWriter, r *http.Request) {
	c := connectionsView{Tab: r.URL.Query().Get("tab"), SearchRemote: v.cfg.SearchRemote,
		Msg: r.URL.Query().Get("msg"), Err: r.URL.Query().Get("err")}
	if c.Tab != "careers" {
		c.Tab = "remote"
	}
	c.AI = v.aiOptions()
	c.AIOn = slices.ContainsFunc(c.AI, func(o option) bool { return o.On })
	c.Mail, c.MailOff = v.mailOptions()
	c.MailOn = slices.ContainsFunc(c.Mail, func(o option) bool { return o.On })
	c.FeedURL, c.FeedNote = v.feedURL(r)
	c.Cal, c.CalOn = v.calendarOptions()
	switch c.CalOn {
	case "agent":
		c.CalSync, c.CalSynced = "/connections/calendar/sync", v.s.Setting(calendarSynced)
		c.CalDays, c.CalPeriods = v.calendarWindow(), calendarPeriods
	case "google":
		c.CalSync = "/calendar/sync"
	}
	if run := v.calendarRun(); run != nil {
		if run.State == runner.Running {
			c.Sync = run
		} else if r.URL.Query().Has("synced") {
			c.Ended, c.Summary = run, runSummary(*run)
		}
	}
	off := splitList(v.s.Setting(search.OffSetting))
	for _, b := range search.RemoteBoards {
		c.Remote = append(c.Remote, source{b, sourceTitles[b], !slices.Contains(off, b)})
	}
	for _, b := range search.Boards {
		c.Career = append(c.Career, source{Name: b, Title: sourceTitles[b]})
	}
	v.render(w, r, "connections.html", "connections", c)
}

// aiOptions lists the installed AI assistants and the custom command, the default one on.
func (v *server) aiOptions() []option {
	names := v.agentNames()
	notes := map[string]string{"claude": "Uses your Claude subscription on this computer.", "codex": "Uses your ChatGPT or Codex subscription on this computer."}
	var out []option
	for i, n := range names {
		if n == "custom" {
			continue
		}
		title := agentTitle(n)
		if n == "claude" {
			title += " (recommended)"
		}
		out = append(out, option{Value: n, Title: title, Note: notes[n], On: i == 0})
	}
	custom := option{Value: "custom", Title: "Custom command", Note: "Another AI app on this computer: set agent_command in config.yaml.", Off: true}
	if i := slices.Index(names, "custom"); i >= 0 {
		custom.Note, custom.Off, custom.On = "Runs the agent_command from config.yaml.", false, i == 0
	}
	return append(out, custom)
}

// mailOptions lists the ways to read the mail and where Disconnect posts for the one in use.
func (v *server) mailOptions() ([]option, string) {
	src, account := v.mailSourceAccount()
	g := v.googleView()
	gmail := option{Value: "gmail", Title: "Google account (Gmail)", Note: "Your own Google client: no AI reads your mail. About 3 minutes to set up.", On: src == "gmail"}
	if g.Connected {
		gmail.Note = g.Account
	}
	imap := option{Value: "imap", Title: "Other mailbox (IMAP)", Note: "Works with iCloud, Outlook, Yahoo and others. Reads only.", On: src == "imap"}
	if src == "imap" {
		imap.Note = account
	}
	agent := option{Value: "agent", Title: "Use my AI assistant's mail connector", Note: "Claude or Codex reads your mail through its Gmail or Microsoft 365 connector.", On: src == "agent", Off: v.cfg.Runner == nil}
	if src == "agent" {
		agent.Note = agentTitle(account) + " reads your mail through its mail connector."
	}
	out := []option{imap, agent}
	if g.Offered {
		out = append([]option{gmail}, out...)
	}
	switch src {
	case "gmail":
		return out, "/gmail/disconnect"
	case "agent":
		return out, "/mail/agent/off"
	}
	return out, ""
}

// calendarOptions lists the ways to sync the interviews with a calendar and the one in use.
func (v *server) calendarOptions() ([]option, string) {
	gcal := ""
	if g := v.gmailClient(); g != nil {
		switch {
		case g.CanWriteCalendar():
			gcal = "on"
		case g.Connected():
			gcal = "reconnect"
		default:
			gcal = "setup"
		}
	}
	via := v.s.Setting(runner.CalendarVia)
	agent := ""
	if v.cfg.Runner != nil {
		agent = v.calendarAgent()
	}
	on := ""
	switch {
	case via == "agent" && agent != "":
		on = "agent"
	case via == "apple":
		on = "apple"
	case gcal == "on":
		on = "google"
	}
	google := option{Value: "google", Title: "Google Calendar", On: on == "google"}
	switch gcal {
	case "on":
		google.Note = "Syncs in the background with " + v.googleView().Account + "."
	case "reconnect":
		google.Note = "Reconnect Google and allow Calendar."
	default:
		google.Note = "Connect your Google account once for mail and calendar."
	}
	ai := option{Value: "agent", Title: "Use my AI assistant's calendar connector", Note: "Claude or Codex syncs your interviews both ways when you press Sync now.", On: on == "agent", Off: agent == ""}
	if agent != "" {
		ai.Note = agentTitle(agent) + " syncs your interviews both ways when you press Sync now."
	}
	apple := option{Value: "apple", Title: "Apple Calendar (subscribe link)", Note: "Add a read-only calendar to Apple Calendar or Outlook on this computer.", On: on == "apple"}
	out := []option{ai, apple}
	if gcal != "" {
		out = append([]option{google}, out...)
	}
	return out, on
}

// calendarRun is the latest agent calendar sync, if the latest mail-slot run is one.
func (v *server) calendarRun() *runner.Status {
	if v.cfg.Runner == nil {
		return nil
	}
	run := v.cfg.Runner.Latest(runner.MailID)
	if run == nil {
		return nil
	}
	snap := run.Snapshot()
	if len(snap.Steps) == 0 || snap.Steps[0].Name != "mail-calendar" {
		return nil
	}
	return &snap
}

// runSummary is the agent's last line: the counts the calendar prompt asks it to end with.
func runSummary(s runner.Status) string {
	for i := len(s.Log) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(s.Log[i]); strings.HasPrefix(s.Log[i], "  ") && line != "" {
			return line
		}
	}
	return ""
}

// agentNames lists the installed AI assistants, the one picked on the Connections page first.
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

// connectionsDone sends the browser back to the card it came from.
func connectionsDone(w http.ResponseWriter, r *http.Request, card, key, text string) {
	http.Redirect(w, r, withParam("/connections", key, text)+"#"+card, http.StatusSeeOther)
}

// setDefaultAgent picks the AI assistant every run uses.
func (v *server) setDefaultAgent(w http.ResponseWriter, r *http.Request) {
	if v.cfg.Runner == nil || sameOrigin(r) != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	name := r.FormValue("via")
	if _, ok := runner.Pick(v.cfg.Runner.Agents(), name); !ok || name == "" {
		connectionsDone(w, r, "ai", "err", fmt.Sprintf("%q is not installed on this machine", name))
		return
	}
	if err := v.s.SetSetting(runner.DefaultAgent, name); err != nil {
		fail(w, err)
		return
	}
	connectionsDone(w, r, "ai", "msg", agentTitle(name)+" now writes your resumes, cover letters and e-mails.")
}

// setMailVia picks where ScoutJobs reads the mail; a mailbox not set up yet opens its setup.
func (v *server) setMailVia(w http.ResponseWriter, r *http.Request) {
	if sameOrigin(r) != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	src, _ := v.mailSourceAccount()
	via := r.FormValue("via")
	switch {
	case via == "gmail" && !v.googleView().Connected:
		http.Redirect(w, r, "/inbox?connect=1#google", http.StatusSeeOther)
		return
	case via == "imap" && src != "imap" && !v.cfg.IMAP:
		http.Redirect(w, r, "/inbox?connect=1#imap", http.StatusSeeOther)
		return
	case via == "agent":
		if v.cfg.Runner == nil {
			connectionsDone(w, r, "mail", "err", "install Claude Code or Codex first")
			return
		}
		ag, err := v.cfg.Runner.MailAgent(v.s.Setting(runner.DefaultAgent))
		if err == nil {
			err = v.s.SetSetting(runner.MailAgent, ag.Name)
		}
		if err != nil {
			connectionsDone(w, r, "mail", "err", err.Error())
			return
		}
	case via != "gmail" && via != "imap":
		connectionsDone(w, r, "mail", "err", "pick a mailbox")
		return
	}
	if err := v.s.SetSetting(runner.MailVia, via); err != nil {
		fail(w, err)
		return
	}
	connectionsDone(w, r, "mail", "msg", "ScoutJobs now reads your mail this way.")
}

// setCalendarVia picks how the interviews reach the calendar.
func (v *server) setCalendarVia(w http.ResponseWriter, r *http.Request) {
	if sameOrigin(r) != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	via := r.FormValue("via")
	msg := map[string]string{
		"agent":  "Your AI assistant now syncs your interviews with your calendar. Press Sync now.",
		"apple":  "Copy the subscribe link below into Apple Calendar or Outlook.",
		"google": "Google Calendar now syncs your interviews in the background.",
		"off":    "Your interviews no longer sync with a calendar.",
	}[via]
	if msg == "" {
		connectionsDone(w, r, "calendar", "err", "pick a calendar")
		return
	}
	if via == "google" {
		if g := v.gmailClient(); g == nil || !g.CanWriteCalendar() {
			http.Redirect(w, r, "/inbox?connect=1#google", http.StatusSeeOther)
			return
		}
		via = ""
	}
	if via == "off" {
		via = ""
	}
	if err := v.s.SetSetting(runner.CalendarVia, via); err != nil {
		fail(w, err)
		return
	}
	connectionsDone(w, r, "calendar", "msg", msg)
}

// syncCalendarAgent has the AI assistant sync the interviews with the calendar, both ways.
func (v *server) syncCalendarAgent(w http.ResponseWriter, r *http.Request) {
	if v.cfg.Runner == nil || sameOrigin(r) != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	agent := v.calendarAgent()
	if agent == "" {
		connectionsDone(w, r, "calendar", "err", "install Claude Code or Codex to sync the calendar through it")
		return
	}
	started := time.Now()
	run, err := v.cfg.Runner.StartMail(runner.MailTask{Kind: "calendar", Since: started, Days: v.calendarWindow()}, agent)
	if err != nil {
		connectionsDone(w, r, "calendar", "err", err.Error())
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

// setCalendarDays sets how far ahead the agent's calendar sync looks.
func (v *server) setCalendarDays(w http.ResponseWriter, r *http.Request) {
	if sameOrigin(r) != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	n, err := strconv.Atoi(r.FormValue("days"))
	if err != nil || !slices.Contains(calendarPeriods, n) {
		connectionsDone(w, r, "calendar", "err", "pick 7, 14 or 30 days")
		return
	}
	if err := v.s.SetSetting(calendarDays, strconv.Itoa(n)); err != nil {
		connectionsDone(w, r, "calendar", "err", err.Error())
		return
	}
	http.Redirect(w, r, "/connections#calendar", http.StatusSeeOther)
}

// setSource turns a remote job board on or off for every search.
func (v *server) setSource(w http.ResponseWriter, r *http.Request) {
	if sameOrigin(r) != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	name := r.FormValue("source")
	if !slices.Contains(search.RemoteBoards, name) {
		http.Error(w, "unknown source", http.StatusBadRequest)
		return
	}
	off := slices.DeleteFunc(splitList(v.s.Setting(search.OffSetting)), func(s string) bool { return s == name })
	if r.FormValue("on") == "" {
		off = append(off, name)
	}
	if err := v.s.SetSetting(search.OffSetting, strings.Join(off, ",")); err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, "/connections#sources", http.StatusSeeOther)
}

// testConnection checks the tool a card has in use, for its Test connection button.
func (v *server) testConnection(w http.ResponseWriter, r *http.Request) {
	ok, text := v.test(r.Context(), r.URL.Query().Get("card"))
	writeJSON(w, 200, map[string]any{"ok": ok, "text": text})
}

func (v *server) test(ctx context.Context, card string) (bool, string) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	switch card {
	case "ai":
		return v.testAgent(ctx, v.s.Setting(runner.DefaultAgent), "")
	case "mail":
		src, account := v.mailSourceAccount()
		switch src {
		case "":
			return false, "No mailbox is connected yet."
		case "agent":
			return v.testAgent(ctx, account, "mail")
		}
		if v.cfg.Mail.Test == nil {
			return false, "This mailbox cannot be tested here."
		}
		if err := v.cfg.Mail.Test(); err != nil {
			return false, "Could not read " + account + ": " + err.Error()
		}
		return true, "Connected: ScoutJobs can read " + account + "."
	case "calendar":
		_, on := v.calendarOptions()
		switch on {
		case "agent":
			return v.testAgent(ctx, v.calendarAgent(), "calendar")
		case "google":
			if _, err := v.gmailClient().Events(ctx, time.Now(), time.Now().Add(24*time.Hour)); err != nil {
				return false, "Could not read Google Calendar: " + err.Error()
			}
			return true, "Connected: ScoutJobs can read and write Google Calendar."
		case "apple":
			return true, "The subscribe link works while ScoutJobs runs on this computer."
		}
		return false, "No calendar is connected yet."
	}
	return false, "unknown card"
}

// testAgent proves the agent starts, and has a ready mail or calendar connector when need says so.
func (v *server) testAgent(ctx context.Context, name, need string) (bool, string) {
	if v.cfg.Runner == nil {
		return false, "No AI assistant is installed: install Claude Code or Codex."
	}
	ag, err := v.cfg.Runner.MailAgent(name)
	if err != nil {
		return false, err.Error()
	}
	conns, err := v.cfg.Runner.Connectors(ctx, ag, true)
	if err != nil {
		return false, agentTitle(ag.Name) + " did not answer: " + err.Error()
	}
	mb, cal := runner.ReadyConnectors(conns)
	switch {
	case need == "mail" && len(mb) == 0:
		return false, runner.NoMailbox(ag.Name, conns)
	case need == "calendar" && len(cal) == 0:
		return false, runner.NoCalendar(ag.Name, conns)
	}
	var ready []string
	for _, c := range conns {
		if c.Ready {
			ready = append(ready, c.Name)
		}
	}
	if len(ready) == 0 {
		return true, agentTitle(ag.Name) + " works. It has no connectors yet."
	}
	return true, agentTitle(ag.Name) + " works. Connectors ready: " + strings.Join(ready, ", ") + "."
}

// ago says how long ago an RFC 3339 time was, in words.
func ago(s string) string {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return s
	}
	switch d := time.Since(t); {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		return fmt.Sprintf("%d %s ago", m, plural(m, "minute", "minutes"))
	case d < 24*time.Hour:
		h := int(d.Hours())
		return fmt.Sprintf("%d %s ago", h, plural(h, "hour", "hours"))
	}
	return "on " + t.Local().Format("2 Jan at 15:04")
}
