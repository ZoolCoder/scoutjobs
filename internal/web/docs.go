package web

import (
	"context"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/apply"
	"github.com/zoolcoder/scoutjobs/internal/mail"
	"github.com/zoolcoder/scoutjobs/internal/runner"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

// doc is one row of a Documents panel.
type doc struct {
	Name, Label string
	Size        int64
	Mod         string
	Exists      bool
	Editable    bool
	Preview     bool
}

func docs(dir string, names ...string) []doc {
	labels := map[string]string{"resume.md": "Resume", "cover-letter.md": "Cover letter", "resume.pdf": "Resume PDF", "resume.docx": "Resume DOCX",
		"cover-letter.docx": "Cover letter DOCX", "cover-letter.pdf": "Cover letter PDF"}
	optional := map[string]bool{"resume.docx": true, "cover-letter.docx": true, "cover-letter.pdf": true}
	var out []doc
	for _, n := range names {
		d := doc{Name: n, Label: labels[n], Editable: apply.Writable[filepath.Ext(n)], Preview: filepath.Ext(n) != ".docx"}
		if fi, err := os.Stat(filepath.Join(dir, n)); err == nil {
			d.Exists, d.Size, d.Mod = true, fi.Size(), fi.ModTime().Format("2006-01-02 15:04")
		}
		if d.Exists || !optional[n] {
			out = append(out, d)
		}
	}
	return out
}

// downloadable lists what the download routes hand out: documents, never the e-mail drafts or logs.
var downloadable = map[string]bool{".md": true, ".pdf": true, ".docx": true, ".txt": true}

func download(w http.ResponseWriter, r *http.Request, dir, name string) {
	name = filepath.Base(name)
	if !downloadable[filepath.Ext(name)] {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(filepath.Join(dir, name))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if ct := mime.TypeByExtension(filepath.Ext(name)); ct != "" && !strings.HasPrefix(ct, "text/html") {
		w.Header().Set("Content-Type", ct)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	http.ServeContent(w, r, name, fi.ModTime(), f)
}

func (v *server) jobDownload(w http.ResponseWriter, r *http.Request) {
	j, ok := v.formJob(w, r)
	if ok {
		download(w, r, apply.Dir(v.s, j), r.PathValue("name"))
	}
}

func (v *server) cvDownload(w http.ResponseWriter, r *http.Request) {
	download(w, r, filepath.Join(v.s.Home, "cv"), r.PathValue("name"))
}

// saveEmail keeps an edited proposal, and with action=gmail also puts it in the mailbox's drafts. Nothing is sent.
func (v *server) saveEmail(w http.ResponseWriter, r *http.Request) {
	j, ok := v.formJob(w, r)
	if !ok {
		return
	}
	if err := sameOrigin(r); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	kind := r.PathValue("kind")
	dest := fmt.Sprintf("/jobs/%d#emails", j.ID)
	m := mail.Message{To: strings.TrimSpace(r.FormValue("to")), Subject: strings.TrimSpace(r.FormValue("subject")), Body: strings.ReplaceAll(r.FormValue("body"), "\r\n", "\n")}
	if err := apply.SaveProposal(v.s, j, kind, m); err != nil {
		http.Redirect(w, r, withParam(dest, "err", err.Error()), http.StatusSeeOther)
		return
	}
	if r.FormValue("action") != "gmail" {
		http.Redirect(w, r, withParam(dest, "msg", "E-mail saved. It is not sent."), http.StatusSeeOther)
		return
	}
	link, note, err := v.fileDraft(j.ID, kind)
	switch {
	case err != nil:
		http.Redirect(w, r, withParam(dest, "err", err.Error()), http.StatusSeeOther)
	case note != "":
		http.Redirect(w, r, withParam(dest, "msg", note), http.StatusSeeOther)
	default:
		http.Redirect(w, r, withParam(dest, "drafted", link), http.StatusSeeOther)
	}
}

// ---- main CV and cover letter ----

// masterPanel is the Main CV panel of the Profile page.
type masterPanel struct {
	Docs     []doc
	Steps    []runner.Step
	Agents   []string
	Run      *runner.Status
	Active   bool
	Auto     bool
	Floor    int
	Ready    int     // jobs Prepare all would queue
	Waiting  []int64 // jobs queued now
	Disabled bool    // no runner in this server
}

func (v *server) masterPanel() masterPanel {
	p := masterPanel{Docs: docs(filepath.Join(v.s.Home, "cv"), "resume.md", "cover-letter.md"), Steps: runner.MasterSteps, Auto: v.s.Setting(autoPrepare) == "on", Floor: v.cfg.FitApplyFloor}
	m := v.cfg.Runner
	if m == nil {
		p.Disabled = true
		return p
	}
	for _, a := range m.Agents() {
		p.Agents = append(p.Agents, a.Name)
	}
	if r := m.Latest(runner.MasterID); r != nil {
		snap := r.Snapshot()
		if n := len(snap.Log); n > 60 {
			snap.Log = snap.Log[n-60:]
		}
		p.Run, p.Active = &snap, snap.State == runner.Running
	}
	if jobs, err := m.Candidates(v.cfg.FitApplyFloor); err == nil {
		p.Ready = len(jobs)
	}
	p.Waiting = m.Waiting()
	return p
}

func (v *server) startMaster(w http.ResponseWriter, r *http.Request) {
	if v.cfg.Runner == nil || sameOrigin(r) != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	_ = r.ParseForm()
	req := runner.Request{Steps: r.Form["step"], Agent: r.FormValue("agent"), Force: r.FormValue("force") != ""}
	if len(req.Steps) == 0 {
		http.Redirect(w, r, withParam("/profile", "err", "pick the CV, the cover letter or both")+"#main", http.StatusSeeOther)
		return
	}
	if _, err := v.cfg.Runner.StartMaster(req); err != nil {
		http.Redirect(w, r, withParam("/profile", "err", err.Error())+"#main", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/profile#main", http.StatusSeeOther)
}

func (v *server) cancelMaster(w http.ResponseWriter, r *http.Request) {
	if v.cfg.Runner != nil && sameOrigin(r) == nil {
		if run := v.cfg.Runner.Latest(runner.MasterID); run != nil {
			v.cfg.Runner.Cancel(run.ID)
		}
	}
	http.Redirect(w, r, "/profile#main", http.StatusSeeOther)
}

func (v *server) masterState(w http.ResponseWriter, r *http.Request) {
	p := v.masterPanel()
	if p.Run == nil {
		writeJSON(w, 404, map[string]string{"error": "no run"})
		return
	}
	writeJSON(w, 200, p.Run)
}

// prepareAll queues every open job at or above the fit floor that has no tailored drafts yet.
func (v *server) prepareAll(w http.ResponseWriter, r *http.Request) {
	if v.cfg.Runner == nil || sameOrigin(r) != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	dest := back(r, "/profile") + "#main"
	jobs, err := v.cfg.Runner.Candidates(v.cfg.FitApplyFloor)
	if err != nil {
		http.Redirect(w, r, withParam(dest, "err", err.Error()), http.StatusSeeOther)
		return
	}
	steps, _ := runner.Parse("all")
	n := v.cfg.Runner.Queue(jobs, runner.Request{Steps: steps, Agent: r.FormValue("agent")})
	http.Redirect(w, r, withParam(dest, "msg", fmt.Sprintf("%d %s queued; your agent prepares them one at a time. Nothing is sent.", n, plural(n, "job", "jobs"))), http.StatusSeeOther)
}

const autoPrepare = "auto_prepare"

func (v *server) setAutoPrepare(w http.ResponseWriter, r *http.Request) {
	if sameOrigin(r) != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	val, msg := "off", "Auto-prepare is off."
	if r.FormValue("on") != "" {
		val, msg = "on", fmt.Sprintf("Auto-prepare is on: new jobs scoring %d or more get a resume, cover letter and e-mail draft. Nothing is sent.", v.cfg.FitApplyFloor)
	}
	if err := v.s.SetSetting(autoPrepare, val); err != nil {
		http.Redirect(w, r, withParam("/profile", "err", err.Error())+"#main", http.StatusSeeOther)
		return
	}
	if val == "on" {
		v.autoPrepareOnce()
	}
	http.Redirect(w, r, withParam("/profile", "msg", msg)+"#main", http.StatusSeeOther)
}

// autoPrepareOnce queues the jobs that qualify and were never run before, so a failed or cancelled
// preparation is not retried behind the user's back.
func (v *server) autoPrepareOnce() int {
	m := v.cfg.Runner
	if m == nil || v.s.Setting(autoPrepare) != "on" {
		return 0
	}
	jobs, err := m.Candidates(v.cfg.FitApplyFloor)
	if err != nil {
		return 0
	}
	var fresh []store.Job
	for _, j := range jobs {
		if !ranBefore(v.s, j.ID) {
			fresh = append(fresh, j)
		}
	}
	steps, _ := runner.Parse("all")
	return m.Queue(fresh, runner.Request{Steps: steps})
}

func ranBefore(s *store.Store, jobID int64) bool {
	events, _ := s.Events(jobID, 200)
	for _, e := range events {
		if strings.HasPrefix(e.Kind, "run:") {
			return true
		}
	}
	return false
}

// autoPrepareLoop checks for new qualifying jobs every few minutes while the dashboard runs.
func (v *server) autoPrepareLoop(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			v.autoPrepareOnce()
		}
	}
}
