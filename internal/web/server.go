// Package web serves the local dashboard. Templates, CSS and the logo are embedded, so the
// binary has no runtime assets and never loads anything from the network.
package web

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/apply"
	"github.com/zoolcoder/scoutjobs/internal/gmail"
	"github.com/zoolcoder/scoutjobs/internal/mail"
	"github.com/zoolcoder/scoutjobs/internal/runner"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

//go:embed templates/*.html templates/style.css templates/mark.svg templates/board.js templates/app.js
var assets embed.FS

type server struct {
	s     *store.Store
	tpl   *template.Template
	cfg   Config
	gmail gmailFlows
	gcal  googleCal
}

// Config is the subset of config.yaml the dashboard needs; cli passes it in to avoid an import cycle.
type Config struct {
	FollowUpDays, StaleDays, WeeklyApplyTarget int
	FitApplyFloor                              int               // Prepare all and auto-prepare take jobs at or above it
	APIToken                                   string            // when set, /api/* requires Authorization: Bearer <token>
	Theme                                      string            // auto | dark | light; the theme cookie overrides it
	EmailFrom                                  string            // sender for drafts built through the API; sending stays in the CLI
	SearchQueries, SearchBoards                []string          // defaults for POST /api/search and the search page
	SearchRemote                               bool              // search page default for the remote boards
	IMAP                                       bool              // imap_host is set in config.yaml
	Currency                                   string            // default on the add-job form
	SearchBaseURL                              map[string]string // test override per search source
	Version                                    string            // shown in the footer next to the release link
	MCP                                        http.Handler      // MCP over HTTP, mounted on /mcp when api_token is set
	Mail                                       MailHooks         // inbox sync and Gmail drafts; nil funcs hide the buttons
	Runner                                     *runner.Manager   // Prepare application on the job page; nil hides it
}

// MailHooks reach the mail code in cli without an import cycle.
type MailHooks struct {
	Status func() (source, account string) // source: agent | gmail | imap | ""; account is the agent's name for agent
	Sync   func() (int, error)
	Test   func() error                                               // reads one message from Gmail or IMAP, to prove the mailbox answers
	Draft  func(jobID int64, kind string) (openURL string, err error) // kind "" is the application e-mail
	// Compose builds the e-mail the AI agent files as a draft when it reads the mail
	Compose func(jobID int64, kind string) (runner.MailTask, error)
	Gmail   func() *gmail.Client // the OAuth client from config.yaml or gmail-client.json; the Inbox page connects it
}

func day(s string) string {
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}

var funcs = template.FuncMap{
	"kb": func(n int64) string {
		switch {
		case n >= 1<<20:
			return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
		case n >= 1<<10:
			return fmt.Sprintf("%d KB", n/(1<<10))
		}
		return fmt.Sprintf("%d B", n)
	},
	"tilde": func(p string) string {
		if h, err := os.UserHomeDir(); err == nil && h != "" && strings.HasPrefix(p, h) {
			return "~" + strings.TrimPrefix(p, h)
		}
		return p
	},
	"kindcls":   func(s string) string { k, _, _ := strings.Cut(s, ":"); return k },
	"evrow":     activityRowOf,
	"gatelabel": gateLabel, "steplabel": stepLabel, "plain": plainDetail, "filelabel": fileLabel,
	"mailto": func(m any) string {
		switch m := m.(type) {
		case *mail.Message:
			return mail.Mailto(*m)
		case mail.Message:
			return mail.Mailto(m)
		}
		return ""
	},
	"day": day,
	"host": func(u string) string {
		if p, err := url.Parse(u); err == nil && p.Host != "" {
			return strings.TrimPrefix(p.Host, "www.")
		}
		return u
	},
	"hasSuffix": strings.HasSuffix,
	"isURL":     func(s string) bool { return strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://") },
	"base":      filepath.Base,
	"join":      strings.Join,
	// mon and dom feed the calendar tile: "2026-10-14" -> "Oct", "14".
	"mon": func(s string) string {
		if t, err := time.Parse("2006-01-02", day(s)); err == nil {
			return t.Format("Jan")
		}
		return ""
	},
	"dom": func(s string) string {
		if len(s) >= 10 {
			return s[8:10]
		}
		return s
	},
	"greet": func() string {
		switch h := time.Now().Hour(); {
		case h < 12:
			return "Good morning"
		case h < 18:
			return "Good afternoon"
		}
		return "Good evening"
	},
	"hm": func(s string) string {
		if len(s) >= 16 && s[10] == 'T' {
			return s[11:16]
		}
		return ""
	},
	"ago": ago,
	"when": func(s string) string {
		if len(s) >= 16 && s[10] == 'T' {
			return s[:10] + " " + s[11:16]
		}
		return s
	},
	"fit": func(n int64) string {
		if n < 0 {
			return "–"
		}
		return strconv.FormatInt(n, 10)
	},
	"pct":   func(f float64) string { return fmt.Sprintf("%.0f%%", f*100) },
	"f1":    func(f float64) string { return fmt.Sprintf("%.1f", f) },
	"title": func(s string) string { return strings.ReplaceAll(s, "_", " ") },
	"lower": strings.ToLower,
	"split": func(s string) []string {
		if strings.TrimSpace(s) == "" {
			return nil
		}
		parts := strings.Split(s, ",")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		return parts
	},
	"gaps":      cvGaps,
	"statuses":  func() []string { return store.Statuses },
	"next":      func(st string) []string { return store.NextStatuses[st] },
	"initial":   initial,
	"hue":       hue,
	"today":     func() string { return time.Now().Format("2006-01-02") },
	"add":       func(a, b int) int { return a + b },
	"colcap":    func() int { return colCap },
	"prepItems": func() []store.PrepItem { return store.PrepItems },
	"prepArgs": func(i store.Interview, back string) map[string]any {
		return map[string]any{"I": i, "Back": back}
	},
	"leaving": func(st string) bool { return store.Leaving[st] },
	"dict_status": func(id int64, status, back string) map[string]any {
		return map[string]any{"ID": id, "Status": status, "Back": back}
	},
	// bucket maps 0-100 to a class suffix in steps of 5, so CSS can draw gauges without inline styles.
	"bucket": func(n int64) int {
		if n < 0 {
			return 0
		}
		if n > 100 {
			n = 100
		}
		return int(n/5) * 5
	},
	"max": func(xs []int64) int64 {
		var m int64 = 1
		for _, x := range xs {
			if x > m {
				m = x
			}
		}
		return m
	},
}

// Serve blocks.
func Serve(s *store.Store, addr string, cfg Config) error {
	return ServeContext(context.Background(), s, addr, cfg)
}

// Health is what GET /healthz answers, so scoutjobs doctor, update and serve can tell which
// dashboard owns a port: its version, process, binary and data dir.
type Health struct {
	App     string `json:"app"`
	Version string `json:"version"`
	PID     int    `json:"pid"`
	Exe     string `json:"exe"`
	Home    string `json:"home"`
}

// ServeContext serves until ctx ends, then lets requests in flight finish and returns nil.
func ServeContext(ctx context.Context, s *store.Store, addr string, cfg Config) error {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return ServeListener(ctx, l, s, cfg)
}

// ServeListener is ServeContext on a listener the caller opened, so it can report a busy port first.
func ServeListener(ctx context.Context, l net.Listener, s *store.Store, cfg Config) error {
	srv, h, err := handler(s, cfg)
	if err != nil {
		l.Close()
		return err
	}
	if cfg.Runner != nil {
		go srv.autoPrepareLoop(ctx, 10*time.Minute)
	}
	hs := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- hs.Serve(l) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return hs.Shutdown(sctx)
	}
}

// Handler builds the dashboard routes; tests drive it without a listener.
func Handler(s *store.Store, cfg Config) (http.Handler, error) {
	_, h, err := handler(s, cfg)
	return h, err
}

func handler(s *store.Store, cfg Config) (*server, http.Handler, error) {
	tpl, err := template.New("").Funcs(funcs).ParseFS(assets, "templates/*.html")
	if err != nil {
		return nil, nil, err
	}
	if cfg.Theme == "" {
		cfg.Theme = "light"
	}
	srv := &server{s: s, tpl: tpl, cfg: cfg}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", srv.overview)
	mux.HandleFunc("GET /pipeline", srv.pipeline)
	mux.HandleFunc("GET /jobs/new", srv.newJob)
	mux.HandleFunc("POST /jobs", srv.createJob)
	mux.HandleFunc("GET /search", srv.searchPage)
	mux.HandleFunc("POST /search", srv.runSearch)
	mux.HandleFunc("POST /search/saved/{id}/delete", srv.deleteSearch)
	mux.HandleFunc("GET /jobs/{id}", srv.job)
	mux.HandleFunc("GET /jobs/{id}/files/{name}", srv.jobFile)
	mux.HandleFunc("POST /jobs/{id}/files/{name}", srv.saveFile)
	mux.HandleFunc("GET /reviews", srv.reviews)
	mux.HandleFunc("POST /jobs/{id}/status", srv.setStatus)
	mux.HandleFunc("POST /jobs/{id}/note", srv.addNote)
	mux.HandleFunc("POST /jobs/{id}/acknowledge", srv.acknowledgeGaps)
	mux.HandleFunc("POST /jobs/{id}/applied", srv.markApplied)
	mux.HandleFunc("POST /jobs/{id}/export", srv.exportPDF)
	mux.HandleFunc("POST /jobs/{id}/run", srv.startRun)
	mux.HandleFunc("POST /jobs/{id}/run/cancel", srv.cancelRun)
	mux.HandleFunc("GET /jobs/{id}/run", srv.runState)
	mux.HandleFunc("POST /jobs/{id}/gmail-draft", srv.gmailDraft)
	mux.HandleFunc("GET /jobs/{id}/download/{name}", srv.jobDownload)
	mux.HandleFunc("GET /jobs/{id}/view/{name}", srv.jobView)
	mux.HandleFunc("POST /jobs/{id}/emails/{kind}", srv.saveEmail)
	mux.HandleFunc("POST /jobs/prepare-all", srv.prepareAll)
	mux.HandleFunc("POST /gmail/client", srv.gmailSetClient)
	mux.HandleFunc("GET /gmail/detect", srv.gmailDetect)
	mux.HandleFunc("POST /gmail/connect", srv.gmailConnect)
	mux.HandleFunc("GET /gmail/callback", srv.gmailCallback)
	mux.HandleFunc("POST /gmail/disconnect", srv.gmailDisconnect)
	mux.HandleFunc("GET /inbox", srv.inbox)
	mux.HandleFunc("POST /inbox/sync", srv.syncInbox)
	mux.HandleFunc("POST /mail/agent", srv.useAgent)
	mux.HandleFunc("POST /mail/agent/off", srv.stopAgent)
	mux.HandleFunc("GET /mail/agent/check", srv.agentCheck)
	mux.HandleFunc("GET /mail/agent/run", srv.agentRun)
	mux.HandleFunc("POST /mail/agent/cancel", srv.cancelAgent)
	mux.HandleFunc("GET /interviews", srv.interviews)
	mux.HandleFunc("POST /interviews", srv.createInterview)
	mux.HandleFunc("POST /interviews/{id}", srv.setInterview)
	mux.HandleFunc("GET /calendar", srv.calendar)
	mux.HandleFunc("POST /calendar/sync", srv.calendarSync)
	mux.HandleFunc("GET /calendar.ics", srv.calendarFeed)
	mux.HandleFunc("GET /calendar/event/{key}", srv.calendarEvent)
	mux.HandleFunc("GET /learning", srv.learning)
	mux.HandleFunc("POST /learning/{id}", srv.setLearning)
	mux.HandleFunc("GET /preferences", srv.preferences)
	mux.HandleFunc("POST /preferences/review", srv.reviewForm)
	mux.HandleFunc("POST /preferences", srv.setPreferenceForm)
	mux.HandleFunc("POST /preferences/delete", srv.deletePreferenceForm)
	mux.HandleFunc("GET /profile", srv.profile)
	mux.HandleFunc("GET /cv/{name}", srv.cvDownload)
	mux.HandleFunc("GET /cv/view/{name}", srv.cvView)
	mux.HandleFunc("GET /profile/files/{name}", srv.cvFile)
	mux.HandleFunc("POST /profile/files/{name}", srv.saveCVFile)
	mux.HandleFunc("POST /profile/prepare", srv.startMaster)
	mux.HandleFunc("POST /profile/prepare/cancel", srv.cancelMaster)
	mux.HandleFunc("GET /profile/prepare", srv.masterState)
	mux.HandleFunc("POST /settings/auto-prepare", srv.setAutoPrepare)
	mux.HandleFunc("GET /connections", srv.connections)
	mux.HandleFunc("POST /connections/agent", srv.setDefaultAgent)
	mux.HandleFunc("POST /connections/calendar", srv.setCalendarVia)
	mux.HandleFunc("POST /connections/calendar/sync", srv.syncCalendarAgent)
	mux.HandleFunc("POST /connections/mail", srv.setMailVia)
	mux.HandleFunc("POST /connections/sources", srv.setSource)
	mux.HandleFunc("GET /connections/test", srv.testConnection)
	mux.HandleFunc("GET /activity", srv.activity)
	mux.HandleFunc("GET /theme", srv.theme)
	mux.HandleFunc("GET /style.css", static("templates/style.css", "text/css; charset=utf-8", false))
	mux.HandleFunc("GET /board.js", static("templates/board.js", "text/javascript; charset=utf-8", false))
	mux.HandleFunc("GET /app.js", static("templates/app.js", "text/javascript; charset=utf-8", false))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		exe, _ := os.Executable()
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, Health{App: "scoutjobs", Version: cfg.Version, PID: os.Getpid(), Exe: exe, Home: s.Home})
	})
	mux.HandleFunc("GET /version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(cfg.Version))
	})
	mux.HandleFunc("GET /logo.svg", static("templates/mark.svg", "image/svg+xml", true))
	mux.HandleFunc("GET /favicon.svg", static("templates/mark.svg", "image/svg+xml", true))

	api := http.NewServeMux()
	api.HandleFunc("GET /api/jobs", srv.apiJobs)
	api.HandleFunc("POST /api/jobs", srv.apiAddJob)
	api.HandleFunc("GET /api/jobs/{id}", srv.apiJob)
	api.HandleFunc("POST /api/jobs/{id}/status", srv.apiSetStatus)
	api.HandleFunc("POST /api/jobs/{id}/note", srv.apiNote)
	api.HandleFunc("GET /api/stats", srv.apiStats)
	api.HandleFunc("GET /api/brief", srv.apiBrief)
	api.HandleFunc("GET /api/events", srv.apiEvents)
	api.HandleFunc("GET /api/jobs/{id}/check", srv.apiCheck)
	api.HandleFunc("POST /api/jobs/{id}/acknowledge", srv.apiAcknowledge)
	api.HandleFunc("GET /api/jobs/{id}/contacts", srv.apiContacts)
	api.HandleFunc("GET /api/jobs/{id}/files", srv.apiFiles)
	api.HandleFunc("GET /api/jobs/{id}/files/{name}", srv.apiReadFile)
	api.HandleFunc("PUT /api/jobs/{id}/files/{name}", srv.apiWriteFile)
	api.HandleFunc("POST /api/jobs/{id}/email/draft", srv.apiEmailDraft)
	api.HandleFunc("POST /api/jobs/{id}/export", srv.apiExport)
	api.HandleFunc("POST /api/jobs/{id}/run", srv.apiRun)
	api.HandleFunc("GET /api/runs/{id}", srv.apiRunGet)
	api.HandleFunc("POST /api/runs/{id}/cancel", srv.apiRunCancel)
	api.HandleFunc("GET /api/agents", srv.apiAgents)
	api.HandleFunc("GET /api/profile", srv.apiProfile)
	api.HandleFunc("POST /api/cv/checks", srv.apiCVCheck)
	api.HandleFunc("GET /api/cv/checks", srv.apiCVChecks)
	api.HandleFunc("POST /api/jobs/{id}/skip", srv.apiSkip)
	api.HandleFunc("GET /api/interviews", srv.apiInterviews)
	api.HandleFunc("POST /api/interviews", srv.apiAddInterview)
	api.HandleFunc("PATCH /api/interviews/{id}", srv.apiPatchInterview)
	api.HandleFunc("GET /api/sessions", srv.apiSessions)
	api.HandleFunc("POST /api/sessions", srv.apiAddSession)
	api.HandleFunc("GET /api/learning", srv.apiLearning)
	api.HandleFunc("POST /api/learning", srv.apiAddLearning)
	api.HandleFunc("PATCH /api/learning/{id}", srv.apiPatchLearning)
	api.HandleFunc("GET /api/preferences", srv.apiPreferences)
	api.HandleFunc("PUT /api/preferences/{key}", srv.apiSetPreference)
	api.HandleFunc("DELETE /api/preferences/{key}", srv.apiDeletePreference)
	api.HandleFunc("GET /api/preferences/proposals", srv.apiProposals)
	api.HandleFunc("PUT /api/preferences/proposals/{key}", srv.apiPropose)
	api.HandleFunc("POST /api/preferences/proposals/{key}", srv.apiReviewProposal)
	api.HandleFunc("GET /api/decisions", srv.apiDecisions)
	api.HandleFunc("POST /api/decisions", srv.apiAddDecision)
	api.HandleFunc("POST /api/search", srv.apiSearch)
	api.HandleFunc("GET /api/mail", srv.apiMail)
	api.HandleFunc("POST /api/jobs/{id}/mail", srv.apiLogMail)
	mux.Handle("/api/", srv.auth(api))
	if cfg.MCP != nil {
		mux.Handle("/mcp", srv.mcpAuth(cfg.MCP))
	}
	return srv, noStore(mux), nil
}

// static serves an embedded file. The stylesheet is never cached so an upgraded binary shows its new look at once.
func static(name, ctype string, cache bool) http.HandlerFunc {
	b, _ := assets.ReadFile(name)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", ctype)
		if cache {
			w.Header().Set("Cache-Control", "public, max-age=86400")
		}
		w.Write(b)
	}
}

func noStore(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if w.Header().Get("Cache-Control") == "" {
			w.Header().Set("Cache-Control", "no-store")
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; img-src 'self' data:")
		h.ServeHTTP(w, r)
	})
}

type page struct {
	Bare   bool // page renders its own header
	Title  string
	Active string
	Nav    navCounts
	Data   any
	Cfg    Config
	Now    string
	Theme  string // dark | light | auto, resolved from cookie then config
	Mini   bool   // the sidebar is folded to icons, from the side cookie app.js sets
	Path   string
	Sub    string
}

// initial and hue draw a company avatar: first letter, one of eight colors picked by the name.
func initial(s string) string {
	for _, r := range strings.TrimSpace(s) {
		return strings.ToUpper(string(r))
	}
	return "?"
}

func hue(s string) int {
	h := 0
	for _, r := range strings.ToLower(s) {
		h = (h*31 + int(r)) % 9973
	}
	return h % 8
}

var subtitles = map[string]string{
	"inbox":       "Replies from companies, matched to your applications.",
	"connections": "Connect the tools ScoutJobs uses: your AI assistant, mail, calendar and job sources.",
	"overview":    "Your job search, at a glance. Here's what to do next.",
	"pipeline":    "Every opportunity, from discovery to outcome.",
	"interviews":  "Prepare for what is coming, record how it went.",
	"calendar":    "Interviews, deadlines and follow-ups. Subscribe from Google Calendar or add one event at a time.",
	"learning":    "Close the gaps your target roles keep asking for.",
	"preferences": "What ScoutJobs learned from your decisions, and why.",
	"profile":     "The source of truth behind every CV claim.",
	"activity":    "Everything ScoutJobs recorded, newest first.",
	"reviews":     "The weekly look back: numbers, funnel, commitments.",
	"search":      "Find open jobs that fit you and add the good ones to your pipeline.",
	"add_job":     "Track a posting you found elsewhere.",
}

func sideMini(r *http.Request) bool {
	c, err := r.Cookie("side")
	return err == nil && c.Value == "mini"
}

func (v *server) render(w http.ResponseWriter, r *http.Request, name, active string, data any) {
	theme := v.cfg.Theme
	if c, err := r.Cookie("theme"); err == nil && (c.Value == "dark" || c.Value == "light" || c.Value == "auto") {
		theme = c.Value
	}
	title := active
	bare := name == "job.html" || name == "file.html" // these pages draw their own header
	if bare {
		if m, ok := data.(map[string]any); ok {
			if j, ok := m["Job"].(store.Job); ok {
				title = j.Company
			}
		}
	}
	p := page{Title: title, Bare: bare, Active: active, Data: data, Cfg: v.cfg, Now: time.Now().Format("Mon 02 Jan 15:04"), Theme: theme, Mini: sideMini(r), Path: r.URL.RequestURI(), Sub: subtitles[active], Nav: v.navCounts()}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := v.tpl.ExecuteTemplate(w, name, p); err != nil {
		http.Error(w, err.Error(), 500)
	}
}

// theme stores the choice in a cookie and goes back where the user was.
func (v *server) theme(w http.ResponseWriter, r *http.Request) {
	t := r.URL.Query().Get("set")
	if t != "dark" && t != "light" && t != "auto" {
		t = "light"
	}
	http.SetCookie(w, &http.Cookie{Name: "theme", Value: t, Path: "/", MaxAge: 365 * 24 * 3600, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, back(r, "/"), http.StatusSeeOther)
}

func fail(w http.ResponseWriter, err error) {
	http.Error(w, err.Error(), http.StatusBadRequest)
}

func pathID(r *http.Request) (int64, error) {
	return strconv.ParseInt(r.PathValue("id"), 10, 64)
}

// ---- overview ----

type overviewData struct {
	Stats    store.Stats
	Funnel   []funnelRow
	Weekly   []weekBar
	Upcoming []store.Interview
	Recent   []store.Event
	Learning []store.Learning
	TodoJobs []store.Job
	Checks   []store.CVCheck
	Target   int
	Today    []todo
}

type funnelRow struct {
	Status string
	Count  int64
	Max    int64
}

func funnelOf(st store.Stats) []funnelRow {
	var max int64 = 1
	for _, k := range store.Statuses {
		if st.ByStatus[k] > max {
			max = st.ByStatus[k]
		}
	}
	var funnel []funnelRow
	for _, k := range []string{"new", "ranked", "drafted", "applied", "interview", "offer", "hired"} {
		funnel = append(funnel, funnelRow{k, st.ByStatus[k], max})
	}
	return funnel
}

type weekBar struct {
	Count int64
	Pct   int // 0-100 in steps of 5, rendered as a CSS height class
}

func (v *server) overview(w http.ResponseWriter, r *http.Request) {
	st, err := v.s.Stats(v.cfg.FollowUpDays, v.cfg.StaleDays)
	if err != nil {
		fail(w, err)
		return
	}
	funnel := funnelOf(st)
	counts := v.s.WeeklyCounts("status", "%applied%", 8)
	wmax := int64(v.cfg.WeeklyApplyTarget)
	for _, c := range counts {
		if c > wmax {
			wmax = c
		}
	}
	if wmax < 1 {
		wmax = 1
	}
	var weekly []weekBar
	for _, c := range counts {
		weekly = append(weekly, weekBar{c, int(c*100/wmax) / 5 * 5})
	}
	d := overviewData{Stats: st, Funnel: funnel, Weekly: weekly, Target: v.cfg.WeeklyApplyTarget}
	d.Upcoming, _ = v.s.Interviews(0, true)
	d.Recent, _ = v.s.Events(0, 8)
	d.Learning, _ = v.s.LearningItems()
	d.TodoJobs, _ = v.s.ListJobs(store.JobFilter{Status: "drafted", Limit: 5})
	d.Checks, _ = v.s.CVChecks(1)
	d.Today = v.today(st, d.Upcoming)
	v.render(w, r, "overview.html", "overview", d)
}

// dueJobs keeps what needs a hand this week: a deadline within 7 days (or passed) on an open job, or a follow-up due.
func (v *server) dueJobs(jobs []store.Job) []store.Job {
	horizon := time.Now().AddDate(0, 0, 7).Format("2006-01-02")
	follow := map[int64]bool{}
	if st, err := v.s.Stats(v.cfg.FollowUpDays, v.cfg.StaleDays); err == nil {
		for _, j := range st.DueFollowUps {
			follow[j.ID] = true
		}
	}
	var out []store.Job
	for _, j := range jobs {
		if store.FinalStatuses[j.Status] {
			continue
		}
		if follow[j.ID] || (j.Deadline != "" && j.Deadline <= horizon) {
			out = append(out, j)
		}
	}
	return out
}

func (v *server) job(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	j, err := v.s.JobByID(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	v.reconcileIdle(j)
	events, _ := v.s.Events(id, 100)
	ivs, _ := v.s.Interviews(id, false)
	kit, dir, entries := apply.Status(v.s, j, events)
	gates := apply.Check(v.s, j, events, apply.CheckOptions{})
	review, reviewErr := apply.LoadReview(dir)
	files := make([]string, 0, len(entries))
	hasMD := false
	for _, f := range entries {
		files = append(files, f.Name)
		hasMD = hasMD || f.Name == "resume.md"
	}
	contacts, _ := v.s.ContactsAt(j.Company)
	tab := r.URL.Query().Get("tab")
	switch tab {
	case "mail", "interviews", "files", "activity":
	default:
		tab = "overview"
	}
	self := fmt.Sprintf("/jobs/%d", id)
	if tab != "overview" {
		self += "?tab=" + tab
	}
	p := v.panel(j, self, "/pipeline")
	v.render(w, r, "job.html", "pipeline", map[string]any{"Job": j, "Tab": tab, "Self": self, "P": p, "Mails": v.mailRows(id, 200), "Events": events, "Interviews": ivs, "Dir": dir, "Files": files, "FileGroups": fileGroups(dir, files), "HasResumeMD": hasMD,
		"Resume": kit.Resume, "Cover": kit.Cover, "Check": kit.ATS, "Sent": kit.Sent, "Draft": kit.Draft, "KitDone": kit.Done,
		"Gates": gates, "Portal": v.portal(j, gates, events), "Review": review, "HasReview": reviewErr == nil, "Contacts": contacts, "ContactCount": v.s.ContactCount(),
		"Checklist": jobChecklist(j, kit, gates, events, dir), "RunP": v.runPanel(j), "RunErr": r.URL.Query().Get("runerr"),
		"Cards": docCards(docs(dir, "resume.md", "cover-letter.md", "resume.pdf", "resume.docx", "cover-letter.docx", "cover-letter.pdf")), "Points": fitPoints(review, 6), "CoverBuild": apply.ProvenanceOf(dir), "Emails": apply.Proposals(v.s, j, v.cfg.FollowUpDays, time.Now()),
		"CanGmail": v.canDraft(), "DraftLabel": v.draftLabel(),
		"Msg": r.URL.Query().Get("msg"), "Err": r.URL.Query().Get("err"), "Drafted": gmailLink(r.URL.Query().Get("drafted"))})
}

// gmailLink keeps the "open your draft" link pointing at Gmail, whatever the query string says.
func gmailLink(u string) string {
	if strings.HasPrefix(u, "https://mail.google.com/") {
		return u
	}
	return ""
}

func (v *server) jobFile(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	j, err := v.s.JobByID(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	p, ok := loadPage(apply.Dir(v.s, j), r.PathValue("name"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	p.Back, p.BackLabel = fmt.Sprintf("/jobs/%d", id), j.Company+" · "+j.Title
	p.Editable = apply.Writable[filepath.Ext(p.Name)]
	// Markdown opens on the preview; plain text and JSON open in the editor, as before
	p.Edit = p.Editable && (p.Kind != "md" || r.URL.Query().Get("edit") != "")
	p.Saved = r.URL.Query().Get("saved") != ""
	base := fmt.Sprintf("/jobs/%d/", id)
	p.SaveURL, p.ViewURL, p.Download = base+"files/"+p.Name, base+"view/"+p.Name, base+"download/"+p.Name
	if !downloadable[filepath.Ext(p.Name)] {
		p.Download = ""
	}
	v.render(w, r, "file.html", "pipeline", map[string]any{"F": p})
}

// saveFile is the textarea on the file page: drafts (.md, .txt, .json) are written back through apply.WriteFile.
func (v *server) saveFile(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	j, err := v.s.JobByID(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, apply.MaxFile+4096)
	body := strings.ReplaceAll(r.FormValue("body"), "\r\n", "\n")
	name, err := apply.WriteFile(apply.Dir(v.s, j), r.PathValue("name"), []byte(body))
	if err != nil {
		fail(w, err)
		return
	}
	_ = v.s.AddEvent(id, "draft:edited", name)
	http.Redirect(w, r, fmt.Sprintf("/jobs/%d/files/%s?saved=1", id, name), http.StatusSeeOther)
}

// ---- reviews ----

type review struct {
	Name  string
	Mod   string
	Label string
}

// weekLabel turns a review file name like 2026-41.md into "Week 41 · 5–11 Oct 2026".
func weekLabel(name string) string {
	var y, wk int
	if _, err := fmt.Sscanf(name, "%d-%d.md", &y, &wk); err != nil || wk < 1 || wk > 53 {
		return strings.TrimSuffix(name, ".md")
	}
	jan4 := time.Date(y, 1, 4, 0, 0, 0, 0, time.UTC)
	mon := jan4.AddDate(0, 0, -((int(jan4.Weekday())+6)%7)+(wk-1)*7)
	sun := mon.AddDate(0, 0, 6)
	span := fmt.Sprintf("%d–%d %s %d", mon.Day(), sun.Day(), sun.Format("Jan"), sun.Year())
	if mon.Month() != sun.Month() {
		span = mon.Format("2 Jan") + " – " + sun.Format("2 Jan 2006")
	}
	return fmt.Sprintf("Week %d · %s", wk, span)
}

func (v *server) reviews(w http.ResponseWriter, r *http.Request) {
	dir := filepath.Join(v.s.Home, "reviews")
	var list []review
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".md" {
				continue
			}
			mod := ""
			if info, err := e.Info(); err == nil {
				mod = info.ModTime().Format("2006-01-02")
			}
			list = append(list, review{e.Name(), mod, weekLabel(e.Name())})
		}
	}
	sort.Slice(list, func(a, b int) bool { return list[a].Name > list[b].Name })
	pick := filepath.Base(r.URL.Query().Get("f"))
	var body, current, label, older, newer string
	for n, rv := range list {
		if pick == rv.Name || (pick == "." && current == "") {
			if b, err := os.ReadFile(filepath.Join(dir, rv.Name)); err == nil {
				body, current, label = string(b), rv.Name, rv.Label
				if n+1 < len(list) {
					older = list[n+1].Name
				}
				if n > 0 {
					newer = list[n-1].Name
				}
			}
		}
	}
	// the page shows the week as its title, so drop the file's own heading
	if first, rest, ok := strings.Cut(body, "\n"); ok && strings.HasPrefix(first, "# ") {
		body = rest
	}
	st, _ := v.s.Stats(v.cfg.FollowUpDays, v.cfg.StaleDays)
	up, _ := v.s.Interviews(0, true)
	if len(up) > 3 {
		up = up[:3]
	}
	v.render(w, r, "reviews.html", "reviews", map[string]any{"Reviews": list, "Current": current, "Label": label, "HTML": mdHTML(body),
		"Older": older, "Newer": newer, "Upcoming": up,
		"Funnel": funnelOf(st), "Stats": st, "Target": v.cfg.WeeklyApplyTarget, "ThisWeek": v.s.WeeklyCounts("status", "%applied%", 1)})
}

func (v *server) setStatus(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	if err := v.changeStatus(id, r.FormValue("status"), r.FormValue("reason")); err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, back(r, "/pipeline"), http.StatusSeeOther)
}

// changeStatus is shared by the form and the API; terminal choices are logged as decisions so learn-preferences sees them.
func (v *server) changeStatus(id int64, st, reason string) error {
	if !store.ValidStatus(st) {
		return fmt.Errorf("bad status %q", st)
	}
	if err := v.s.UpdateJob(id, store.JobPatch{Status: &st}); err != nil {
		return err
	}
	if kind, ok := map[string]string{"skipped": "skip", "withdrawn": "withdraw", "offer_declined": "decline_offer"}[st]; ok {
		_ = v.s.AddDecision(id, kind, reason, "{}")
	}
	return nil
}

func (v *server) addNote(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	if n := strings.TrimSpace(r.FormValue("note")); n != "" {
		_ = v.s.AddEvent(id, "note", n)
	}
	http.Redirect(w, r, back(r, fmt.Sprintf("/jobs/%d", id)), http.StatusSeeOther)
}

// exportPDF is the "Export PDF" button on the job page: resume.md in the folder becomes resume.pdf.
func (v *server) exportPDF(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	j, err := v.s.JobByID(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	name := filepath.Base(r.FormValue("file"))
	if name == "." || name == "" {
		name = "resume.md"
	}
	if dst, _, err := apply.ExportPDF(apply.Dir(v.s, j), name, ""); err == nil {
		_ = v.s.AddEvent(id, "cv:export", filepath.Base(dst))
	}
	http.Redirect(w, r, fmt.Sprintf("/jobs/%d", id), http.StatusSeeOther)
}

func back(r *http.Request, def string) string {
	if b := r.FormValue("back"); strings.HasPrefix(b, "/") && !strings.HasPrefix(b, "//") {
		return b
	}
	return def
}

// ---- interviews ----

func (v *server) interviews(w http.ResponseWriter, r *http.Request) {
	all, _ := v.s.Interviews(0, false)
	now := time.Now().UTC().Format("2006-01-02T15:04")
	var up, open, past []store.Interview
	for _, i := range all {
		switch {
		case i.At >= now[:len(i.At)] || i.Outcome == "" && i.At >= now[:10]:
			up = append(up, i)
		case i.Outcome == "":
			open = append(open, i)
		default:
			past = append(past, i)
		}
	}
	v.render(w, r, "interviews.html", "interviews", map[string]any{"Upcoming": up, "NeedsOutcome": open, "Past": past, "Outcomes": []string{"passed", "failed", "pending", "cancelled"}})
}

func (v *server) setInterview(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	var p store.InterviewPatch
	if r.FormValue("prep_form") != "" { // the checklist: unticked boxes are absent from the form
		done := strings.Join(r.Form["prep"], ",")
		p.Prep = &done
	}
	if s := r.FormValue("prep_status"); s != "" {
		p.PrepStatus = &s
	}
	if s := r.FormValue("outcome"); s != "" {
		p.Outcome = &s
	}
	if s := r.FormValue("feedback"); s != "" {
		p.Feedback = &s
	}
	if r.FormValue("date") != "" {
		at, err := formAt(r)
		if err != nil {
			fail(w, err)
			return
		}
		p.At = &at
	}
	if _, ok := r.Form["location"]; ok {
		loc := strings.TrimSpace(r.FormValue("location"))
		p.Location = &loc
	}
	if d, err := strconv.ParseInt(r.FormValue("duration"), 10, 64); err == nil && d > 0 {
		p.Duration = &d
	}
	if err := v.s.UpdateInterview(id, p); err != nil {
		fail(w, err)
		return
	}
	v.syncSoon()
	http.Redirect(w, r, back(r, "/interviews"), http.StatusSeeOther)
}

// ---- learning ----

func (v *server) learning(w http.ResponseWriter, r *http.Request) {
	items, _ := v.s.LearningItems()
	sessions, _ := v.s.Sessions(30)
	var hours float64
	byKind := map[string]int{}
	for _, l := range items {
		hours += l.Hours
	}
	for _, k := range sessions {
		byKind[k.Kind]++
	}
	v.render(w, r, "learning.html", "learning", map[string]any{"Items": items, "Sessions": sessions, "Hours": hours, "ByKind": byKind})
}

func (v *server) setLearning(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	var p store.LearningPatch
	if s := r.FormValue("status"); s != "" {
		p.Status = &s
	}
	if h, err := strconv.ParseFloat(r.FormValue("hours"), 64); err == nil && h > 0 {
		p.Hours = &h
	}
	if err := v.s.UpdateLearning(id, p); err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, "/learning", http.StatusSeeOther)
}

// ---- preferences / profile / activity ----

func (v *server) preferences(w http.ResponseWriter, r *http.Request) {
	prefs, _ := v.s.Preferences()
	ds, _ := v.s.DecisionSummary(200)
	all, _ := v.s.Proposals(true)
	var pending, rejected []store.Proposal
	for _, p := range all {
		if p.Status == "pending" {
			pending = append(pending, p)
		} else {
			rejected = append(rejected, p)
		}
	}
	v.render(w, r, "preferences.html", "preferences", map[string]any{"Prefs": prefs, "Summary": ds, "Pending": pending, "Rejected": rejected})
}

// reviewForm confirms, edits (confirm with the user's wording) or rejects one proposal.
func (v *server) reviewForm(w http.ResponseWriter, r *http.Request) {
	action := r.FormValue("action")
	value := ""
	if action == "edit" {
		action, value = "confirm", r.FormValue("value")
		if strings.TrimSpace(value) == "" {
			fail(w, errors.New("write the value in your own words, or confirm the proposal as it is"))
			return
		}
	}
	if _, err := v.reviewProposal(r.FormValue("key"), action, value, r.FormValue("reason")); err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, "/preferences", http.StatusSeeOther)
}

// setPreferenceForm stores a preference in the user's own words, so learning never overwrites it.
func (v *server) setPreferenceForm(w http.ResponseWriter, r *http.Request) {
	key := prefKey(r.FormValue("key"))
	value := strings.TrimSpace(r.FormValue("value"))
	if key == "" || value == "" {
		fail(w, errors.New("name the preference and give it a value"))
		return
	}
	if err := v.s.SetPreference(store.Preference{Key: key, Value: value, Confidence: 1, Source: "stated"}); err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, "/preferences", http.StatusSeeOther)
}

func (v *server) deletePreferenceForm(w http.ResponseWriter, r *http.Request) {
	if err := v.s.DeletePreference(r.FormValue("key")); err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, "/preferences", http.StatusSeeOther)
}

// prefKey turns "Company size" into the stored key "company_size".
func prefKey(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), "_")
}

// profile shows profile.md, the cv/ folder and the CV check history. Files are read, never written, from here.
func (v *server) profile(w http.ResponseWriter, r *http.Request) {
	prof, _ := os.ReadFile(filepath.Join(v.s.Home, "profile.md"))
	filled := false
	for _, line := range strings.Split(string(prof), "\n") {
		if k, val, ok := strings.Cut(line, ":"); ok && strings.HasPrefix(k, "- Name") && strings.TrimSpace(val) != "" {
			filled = true
		}
	}
	cvDir := filepath.Join(v.s.Home, "cv")
	type file struct {
		Name    string
		Size    int64
		Mod     string
		Preview bool
	}
	var files []file
	if entries, err := os.ReadDir(cvDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || strings.HasPrefix(e.Name(), ".") || e.Name() == "README.md" {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			files = append(files, file{e.Name(), info.Size(), info.ModTime().Format("2006-01-02"), previewKind(e.Name()) != "other"})
		}
	}
	checks, _ := v.s.CVChecks(20)
	v.render(w, r, "profile.html", "profile", map[string]any{
		"Profile": string(prof), "Filled": filled, "ProfilePath": filepath.Join(v.s.Home, "profile.md"),
		"CVDir": cvDir, "Files": files, "Checks": checks, "Main": v.masterPanel(),
		"Msg": r.URL.Query().Get("msg"), "Err": r.URL.Query().Get("err"),
	})
}

func (v *server) activity(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	group := q.Get("kind")
	limit := 60
	if n, err := strconv.Atoi(q.Get("n")); err == nil && n > 0 {
		limit = min(n, 500)
	}
	all, _ := v.s.Events(0, 500)
	weekAgo := time.Now().UTC().AddDate(0, 0, -7).Format("2006-01-02")
	present, week := map[string]bool{}, map[string]int{}
	var events []store.Event
	for _, e := range all {
		g := eventGroup(e.Kind)
		present[g] = true
		if day(e.At) >= weekAgo {
			week[g]++
		}
		if group == "" || g == group {
			events = append(events, e)
		}
	}
	total := len(events)
	if len(events) > limit {
		events = events[:limit]
	}
	type tally struct {
		Group      string
		Count, Max int
	}
	var groups []string
	var tallies []tally
	most := 0
	for _, g := range activityGroups {
		if present[g] {
			groups = append(groups, g)
		}
		if week[g] > 0 {
			tallies = append(tallies, tally{Group: g, Count: week[g]})
			most = max(most, week[g])
		}
	}
	for i := range tallies {
		tallies[i].Max = most
	}
	v.render(w, r, "activity.html", "activity", map[string]any{"Days": groupByDay(events, time.Now()), "Groups": groups, "Kind": group,
		"Older": total - len(events), "More": limit + 60, "Week": tallies})
}

// ---- json api (n8n, Make, scripts) ----

func (v *server) auth(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v.cfg.APIToken != "" && r.Header.Get("Authorization") != "Bearer "+v.cfg.APIToken {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeJSON(w, 401, map[string]string{"error": "set Authorization: Bearer <api_token from config.yaml>"})
			return
		}
		h.ServeHTTP(w, r)
	})
}

// mcpAuth guards /mcp harder than /api: a tunnel delivers remote callers from loopback, so the endpoint
// exists only with api_token set. Connectors that cannot send headers pass the token as ?token=.
// Browsers are kept out by the Origin check, which stops a web page from driving the tools.
func (v *server) mcpAuth(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v.cfg.APIToken == "" {
			writeJSON(w, 403, map[string]string{"error": "set api_token in config.yaml to enable /mcp"})
			return
		}
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if tok == "" || tok == r.Header.Get("Authorization") {
			tok = r.URL.Query().Get("token")
		}
		if subtle.ConstantTimeCompare([]byte(tok), []byte(v.cfg.APIToken)) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeJSON(w, 401, map[string]string{"error": "set Authorization: Bearer <api_token> or ?token=<api_token>"})
			return
		}
		if o := r.Header.Get("Origin"); o != "" && o != "http://"+r.Host && o != "https://"+r.Host {
			writeJSON(w, 403, map[string]string{"error": "cross-origin requests are refused"})
			return
		}
		h.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func apiErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func (v *server) apiJobs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	jobs, err := v.s.ListJobs(store.JobFilter{Status: q.Get("status"), Query: q.Get("q"), Limit: limit, Open: q.Get("open") == "1"})
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	if jobs == nil {
		jobs = []store.Job{}
	}
	writeJSON(w, 200, jobs)
}

// apiAddJob accepts the same fields as `scoutjobs job add`; an existing company+title is updated, not duplicated.
func (v *server) apiAddJob(w http.ResponseWriter, r *http.Request) {
	var j store.Job
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&j); err != nil {
		apiErr(w, 400, err)
		return
	}
	if j.Company == "" || j.Title == "" {
		apiErr(w, 400, fmt.Errorf("company and title are required"))
		return
	}
	job, created, err := v.s.UpsertJob(j)
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	code := 200
	if created {
		code = 201
	}
	writeJSON(w, code, map[string]any{"job": job, "created": created})
}

// apiCheck runs the quality gates; 200 with ok=false when something still blocks the send.
func (v *server) apiCheck(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		apiErr(w, 400, err)
		return
	}
	j, err := v.s.JobByID(id)
	if err != nil {
		apiErr(w, 404, fmt.Errorf("job %d not found", id))
		return
	}
	events, _ := v.s.Events(id, 200)
	rep := apply.Check(v.s, j, events, apply.CheckOptions{})
	writeJSON(w, 200, map[string]any{"job": j.ID, "ok": rep.OK(), "report": rep})
}

func (v *server) apiJob(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		apiErr(w, 400, err)
		return
	}
	j, err := v.s.JobByID(id)
	if err != nil {
		apiErr(w, 404, fmt.Errorf("job %d not found", id))
		return
	}
	events, _ := v.s.Events(id, 50)
	ivs, _ := v.s.Interviews(id, false)
	writeJSON(w, 200, map[string]any{"job": j, "events": events, "interviews": ivs, "provenance": apply.ProvenanceOf(apply.Dir(v.s, j))})
}

func (v *server) apiSetStatus(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		apiErr(w, 400, err)
		return
	}
	var in struct{ Status, Reason string }
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
			apiErr(w, 400, err)
			return
		}
	} else {
		in.Status, in.Reason = r.FormValue("status"), r.FormValue("reason")
	}
	if err := v.changeStatus(id, in.Status, in.Reason); err != nil {
		apiErr(w, 400, err)
		return
	}
	j, _ := v.s.JobByID(id)
	writeJSON(w, 200, j)
}

func (v *server) apiNote(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		apiErr(w, 400, err)
		return
	}
	var in struct{ Note string }
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in)
	} else {
		in.Note = r.FormValue("note")
	}
	if strings.TrimSpace(in.Note) == "" {
		apiErr(w, 400, fmt.Errorf("note is empty"))
		return
	}
	if err := v.s.AddEvent(id, "note", strings.TrimSpace(in.Note)); err != nil {
		apiErr(w, 400, err)
		return
	}
	writeJSON(w, 201, map[string]string{"ok": "noted"})
}

func (v *server) apiStats(w http.ResponseWriter, r *http.Request) {
	st, err := v.s.Stats(v.cfg.FollowUpDays, v.cfg.StaleDays)
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	writeJSON(w, 200, st)
}

// apiBrief is the daily-briefing payload: what needs attention today, small enough for an automation step.
func (v *server) apiBrief(w http.ResponseWriter, r *http.Request) {
	st, err := v.s.Stats(v.cfg.FollowUpDays, v.cfg.StaleDays)
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	up, _ := v.s.Interviews(0, true)
	fresh, _ := v.s.ListJobs(store.JobFilter{Status: "new", Limit: 10})
	drafted, _ := v.s.ListJobs(store.JobFilter{Status: "drafted", Limit: 10})
	writeJSON(w, 200, map[string]any{
		"date": time.Now().Format("2006-01-02"), "target_week": v.cfg.WeeklyApplyTarget, "applied_week": st.AppliedWeek, "open": st.Open,
		"response_rate": st.ResponseRate, "streak": st.Streak, "follow_ups_due": st.DueFollowUps, "stale": st.Stale,
		"upcoming_interviews": up, "new_jobs": fresh, "drafted_jobs": drafted,
	})
}

func (v *server) apiEvents(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	events, err := v.s.Events(0, limit)
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	if events == nil {
		events = []store.Event{}
	}
	writeJSON(w, 200, events)
}

type dayGroup struct {
	Label  string
	Events []store.Event
}

// groupByDay splits events (newest first) into one group per calendar day with a human label.
func groupByDay(events []store.Event, now time.Time) []dayGroup {
	var out []dayGroup
	today := now.Format("2006-01-02")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
	for _, e := range events {
		d := day(e.At)
		label := d
		switch d {
		case today:
			label = "Today"
		case yesterday:
			label = "Yesterday"
		default:
			if t, err := time.Parse("2006-01-02", d); err == nil {
				label = t.Format("Mon 2 Jan")
			}
		}
		if n := len(out); n > 0 && out[n-1].Label == label {
			out[n-1].Events = append(out[n-1].Events, e)
			continue
		}
		out = append(out, dayGroup{Label: label, Events: []store.Event{e}})
	}
	return out
}

// navCounts are the live numbers beside the navigation links.
type navCounts struct {
	Preparing  int // jobs an agent is preparing or that wait in the queue
	Interviews int // upcoming interviews
}

func (v *server) navCounts() navCounts {
	var n navCounts
	if v.cfg.Runner != nil {
		n.Preparing = v.cfg.Runner.Busy()
	}
	if ivs, err := v.s.Interviews(0, true); err == nil {
		n.Interviews = len(ivs)
	}
	return n
}

// gapStart matches the start of a CV-check finding, such as "Tailor:" or "Profile gap (".
var gapStart = regexp.MustCompile(`^[A-Z][A-Za-z ]{0,30}[:(]`)

// cvGaps splits a CV check's comma-joined findings, keeping commas inside a finding.
func cvGaps(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		switch {
		case p == "":
		case len(out) == 0 || gapStart.MatchString(p):
			out = append(out, p)
		default:
			out[len(out)-1] += ", " + p
		}
	}
	return out
}
