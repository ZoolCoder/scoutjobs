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
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/apply"
	"github.com/zoolcoder/scoutjobs/internal/gmail"
	"github.com/zoolcoder/scoutjobs/internal/mail"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

//go:embed templates/*.html templates/style.css templates/mark.svg templates/board.js templates/app.js
var assets embed.FS

type server struct {
	s     *store.Store
	tpl   *template.Template
	cfg   Config
	gmail gmailFlows
}

// Config is the subset of config.yaml the dashboard needs; cli passes it in to avoid an import cycle.
type Config struct {
	FollowUpDays, StaleDays, WeeklyApplyTarget int
	APIToken                                   string            // when set, /api/* requires Authorization: Bearer <token>
	Theme                                      string            // auto | dark | light; the theme cookie overrides it
	EmailFrom                                  string            // sender for drafts built through the API; sending stays in the CLI
	SearchQueries, SearchBoards                []string          // defaults for POST /api/search and the search page
	SearchRemote                               bool              // search page default for the remote boards
	Currency                                   string            // default on the add-job form
	SearchBaseURL                              map[string]string // test override per search source
	Version                                    string            // shown in the footer next to the release link
	MCP                                        http.Handler      // MCP over HTTP, mounted on /mcp when api_token is set
	Mail                                       MailHooks         // inbox sync and Gmail drafts; nil funcs hide the buttons
}

// MailHooks reach the mail code in cli without an import cycle.
type MailHooks struct {
	Status func() (source, account string) // source: gmail | imap | ""
	Sync   func() (int, error)
	Draft  func(jobID int64) (openURL string, err error)
	Gmail  func() *gmail.Client // the OAuth client from config.yaml or gmail-client.json; the Inbox page connects it
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
	"kindcls": func(s string) string { k, _, _ := strings.Cut(s, ":"); return k },
	"mailto":  func(m *mail.Message) string { return mail.Mailto(*m) },
	"day":     day,
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
	"hm": func(s string) string {
		if len(s) >= 16 && s[10] == 'T' {
			return s[11:16]
		}
		return ""
	},
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
	h, err := Handler(s, cfg)
	if err != nil {
		l.Close()
		return err
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
	tpl, err := template.New("").Funcs(funcs).ParseFS(assets, "templates/*.html")
	if err != nil {
		return nil, err
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
	mux.HandleFunc("POST /jobs/{id}/export", srv.exportPDF)
	mux.HandleFunc("POST /jobs/{id}/gmail-draft", srv.gmailDraft)
	mux.HandleFunc("POST /gmail/client", srv.gmailSetClient)
	mux.HandleFunc("POST /gmail/connect", srv.gmailConnect)
	mux.HandleFunc("GET /gmail/callback", srv.gmailCallback)
	mux.HandleFunc("POST /gmail/disconnect", srv.gmailDisconnect)
	mux.HandleFunc("GET /inbox", srv.inbox)
	mux.HandleFunc("POST /inbox/sync", srv.syncInbox)
	mux.HandleFunc("GET /interviews", srv.interviews)
	mux.HandleFunc("POST /interviews", srv.createInterview)
	mux.HandleFunc("POST /interviews/{id}", srv.setInterview)
	mux.HandleFunc("GET /calendar", srv.calendar)
	mux.HandleFunc("GET /calendar.ics", srv.calendarFeed)
	mux.HandleFunc("GET /calendar/event/{key}", srv.calendarEvent)
	mux.HandleFunc("GET /learning", srv.learning)
	mux.HandleFunc("POST /learning/{id}", srv.setLearning)
	mux.HandleFunc("GET /preferences", srv.preferences)
	mux.HandleFunc("POST /preferences/review", srv.reviewForm)
	mux.HandleFunc("GET /profile", srv.profile)
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
	api.HandleFunc("GET /api/jobs/{id}/contacts", srv.apiContacts)
	api.HandleFunc("GET /api/jobs/{id}/files", srv.apiFiles)
	api.HandleFunc("GET /api/jobs/{id}/files/{name}", srv.apiReadFile)
	api.HandleFunc("PUT /api/jobs/{id}/files/{name}", srv.apiWriteFile)
	api.HandleFunc("POST /api/jobs/{id}/email/draft", srv.apiEmailDraft)
	api.HandleFunc("POST /api/jobs/{id}/export", srv.apiExport)
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
	return noStore(mux), nil
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
	Data   any
	Cfg    Config
	Now    string
	Theme  string // dark | light | auto, resolved from cookie then config
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
	"overview":    "Your job search, at a glance.",
	"pipeline":    "Every opportunity, from discovery to outcome.",
	"interviews":  "Prepare for what is coming, record how it went.",
	"calendar":    "Interviews, deadlines and follow-ups. Subscribe from Google Calendar or add one event at a time.",
	"learning":    "Close the gaps your target roles keep asking for.",
	"preferences": "What ScoutJobs learned from your decisions, and why.",
	"profile":     "The source of truth behind every CV claim.",
	"activity":    "Everything ScoutJobs recorded, newest first.",
	"reviews":     "The weekly look back: numbers, funnel, commitments.",
	"search":      "Public boards and feeds, deduplicated, nothing followed.",
	"add_job":     "Track a posting you found elsewhere.",
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
	p := page{Title: title, Bare: bare, Active: active, Data: data, Cfg: v.cfg, Now: time.Now().Format("Mon 02 Jan 15:04"), Theme: theme, Path: r.URL.RequestURI(), Sub: subtitles[active]}
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
	Stats     store.Stats
	Funnel    []funnelRow
	Weekly    []weekBar
	Upcoming  []store.Interview
	Recent    []store.Event
	Prefs     []store.Preference
	Learning  []store.Learning
	Sessions  []store.Session
	TodoJobs  []store.Job
	Decisions []store.Decision
	Checks    []store.CVCheck
	Target    int
	Today     []todo
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
	d.Recent, _ = v.s.Events(0, 12)
	d.Prefs, _ = v.s.Preferences()
	if len(d.Prefs) > 6 {
		d.Prefs = d.Prefs[:6]
	}
	d.Learning, _ = v.s.LearningItems()
	d.Sessions, _ = v.s.Sessions(5)
	d.TodoJobs, _ = v.s.ListJobs(store.JobFilter{Status: "drafted", Limit: 5})
	d.Decisions, _ = v.s.Decisions(5)
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
	v.render(w, r, "job.html", "pipeline", map[string]any{"Job": j, "Tab": tab, "Self": self, "P": p, "Mails": v.mailRows(id, 200), "Events": events, "Interviews": ivs, "Dir": dir, "Files": files, "HasResumeMD": hasMD,
		"Resume": kit.Resume, "Cover": kit.Cover, "Check": kit.ATS, "Sent": kit.Sent, "Draft": kit.Draft, "KitDone": kit.Done,
		"Gates": gates, "Review": review, "HasReview": reviewErr == nil, "Contacts": contacts, "ContactCount": v.s.ContactCount()})
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
	name := filepath.Base(r.PathValue("name"))
	if !apply.Textual[filepath.Ext(name)] {
		http.NotFound(w, r)
		return
	}
	dir := apply.Dir(v.s, j)
	body, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	v.render(w, r, "file.html", "pipeline", map[string]any{"Job": j, "Name": name, "Body": string(body), "Size": int64(len(body)), "Path": filepath.Join(dir, name),
		"Editable": apply.Writable[filepath.Ext(name)], "Saved": r.URL.Query().Get("saved") != ""})
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
	Name string
	Mod  string
}

// reviews lists reviews/*.md (written by the weekly-review skill), shows one, and draws the funnel next to it.
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
			list = append(list, review{e.Name(), mod})
		}
	}
	sort.Slice(list, func(a, b int) bool { return list[a].Name > list[b].Name })
	pick := filepath.Base(r.URL.Query().Get("f"))
	var body, current string
	for _, rv := range list {
		if pick == rv.Name || (pick == "." && current == "") {
			if b, err := os.ReadFile(filepath.Join(dir, rv.Name)); err == nil {
				body, current = string(b), rv.Name
			}
		}
	}
	st, _ := v.s.Stats(v.cfg.FollowUpDays, v.cfg.StaleDays)
	v.render(w, r, "reviews.html", "reviews", map[string]any{"Reviews": list, "Current": current, "Body": body, "Dir": dir,
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
		Name string
		Size int64
		Mod  string
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
			files = append(files, file{e.Name(), info.Size(), info.ModTime().Format("2006-01-02")})
		}
	}
	checks, _ := v.s.CVChecks(20)
	prefs, _ := v.s.Preferences()
	v.render(w, r, "profile.html", "profile", map[string]any{
		"Profile": string(prof), "Filled": filled, "ProfilePath": filepath.Join(v.s.Home, "profile.md"),
		"CVDir": cvDir, "Files": files, "Checks": checks, "Prefs": prefs,
	})
}

func (v *server) activity(w http.ResponseWriter, r *http.Request) {
	all, _ := v.s.Events(0, 200)
	kind := r.URL.Query().Get("kind")
	kinds := map[string]bool{}
	var events []store.Event
	for _, e := range all {
		k, _, _ := strings.Cut(e.Kind, ":")
		kinds[k] = true
		if kind == "" || k == kind {
			events = append(events, e)
		}
	}
	var names []string
	for k := range kinds {
		names = append(names, k)
	}
	sort.Strings(names)
	v.render(w, r, "activity.html", "activity", map[string]any{"Days": groupByDay(events, time.Now()), "Kinds": names, "Kind": kind, "Count": len(events)})
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
	writeJSON(w, 200, map[string]any{"job": j, "events": events, "interviews": ivs})
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
