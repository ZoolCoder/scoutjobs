package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/zoolcoder/scoutjobs/internal/runner"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

// runPanel is the Prepare application panel of the job page.
type runPanel struct {
	Agents   []string
	Steps    []runner.Step
	Todo     []runner.Step // steps without output yet, offered by default
	Done     []runner.Step // finished steps, offered only to redo
	Finished map[string]string
	Run      *runner.Status
	Active   bool
	Queued   int // position in the Prepare all queue, 0 when not waiting
	// Recovered: the last run stopped at a step that is done now (fixed by hand or by a later run),
	// so its error is history, not the current state.
	Recovered string
	// Stale: steps whose result covers files that changed since, with what changed
	Stale map[string]string
	// Now: how each step of the last run stands today, when that differs: recovered or stale
	Now map[string]string
}

func (v *server) runPanel(j store.Job) *runPanel {
	m := v.cfg.Runner
	if m == nil {
		return nil
	}
	p := &runPanel{Steps: runner.Steps, Finished: m.Finished(j), Stale: m.Stale(j), Now: map[string]string{}}
	for _, st := range runner.Steps {
		if _, ok := p.Finished[st.Name]; ok {
			p.Done = append(p.Done, st)
		} else {
			p.Todo = append(p.Todo, st)
		}
	}
	p.Agents = v.agentNames()
	if r := m.Latest(j.ID); r != nil {
		snap := r.Snapshot()
		if n := len(snap.Log); n > 60 {
			snap.Log = snap.Log[n-60:]
		}
		p.Run, p.Active = &snap, snap.State == runner.Running
		for _, st := range snap.Steps {
			_, done := p.Finished[st.Name]
			switch {
			case done && st.State == runner.Failed:
				p.Recovered = st.Label
				p.Now[st.Name] = "recovered"
			case p.Stale[st.Name] != "" && (st.State == runner.Done || st.State == runner.Skipped):
				p.Now[st.Name] = "stale"
			}
		}
	}
	for i, id := range m.Waiting() {
		if id == j.ID {
			p.Queued = i + 1
		}
	}
	return p
}

// reconcileIdle records agent results left in the job folder and merges older folders when no run
// is working on the job, so a stopped run's steps show as they stand now. It never sends anything
// and never changes the job's stage beyond what the recorded results say.
func (v *server) reconcileIdle(j store.Job) {
	m := v.cfg.Runner
	if m == nil {
		return
	}
	if r := m.Latest(j.ID); r != nil && r.Snapshot().State == runner.Running {
		return
	}
	m.Reconcile(j)
}

func (v *server) startRun(w http.ResponseWriter, r *http.Request) {
	j, ok := v.formJob(w, r)
	if !ok {
		return
	}
	if v.cfg.Runner == nil || sameOrigin(r) != nil { // another site must not start an agent
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	_ = r.ParseForm()
	// a finished step is only run again when picked under "Redo", which forces it
	redo := r.Form["redo"]
	req := runner.Request{Steps: append(r.Form["step"], redo...), Agent: r.FormValue("agent"), Force: r.FormValue("force") != "" || len(redo) > 0, KeepGoing: r.FormValue("keep") != ""}
	msg := ""
	if len(req.Steps) == 0 {
		msg = "pick at least one step"
	} else if _, err := v.cfg.Runner.Start(j, req); err != nil {
		msg = err.Error()
	}
	u := fmt.Sprintf("/jobs/%d", j.ID)
	if msg != "" {
		u += "?runerr=" + url.QueryEscape(msg)
	}
	http.Redirect(w, r, u+"#run", http.StatusSeeOther)
}

func (v *server) cancelRun(w http.ResponseWriter, r *http.Request) {
	j, ok := v.formJob(w, r)
	if !ok {
		return
	}
	if v.cfg.Runner != nil && sameOrigin(r) == nil {
		if run := v.cfg.Runner.Latest(j.ID); run != nil {
			v.cfg.Runner.Cancel(run.ID)
		}
	}
	http.Redirect(w, r, fmt.Sprintf("/jobs/%d#run", j.ID), http.StatusSeeOther)
}

// runState feeds the live progress on the job page (app.js polls it while a run is active).
func (v *server) runState(w http.ResponseWriter, r *http.Request) {
	j, ok := v.formJob(w, r)
	if !ok {
		return
	}
	p := v.runPanel(j)
	if p != nil && p.Queued > 0 && !p.Active {
		writeJSON(w, 200, map[string]any{"state": "queued", "position": p.Queued})
		return
	}
	if p == nil || p.Run == nil {
		writeJSON(w, 404, map[string]string{"error": "no run"})
		return
	}
	writeJSON(w, 200, p.Run)
}

func (v *server) formJob(w http.ResponseWriter, r *http.Request) (store.Job, bool) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return store.Job{}, false
	}
	j, err := v.s.JobByID(id)
	if err != nil {
		http.NotFound(w, r)
		return store.Job{}, false
	}
	return j, true
}

// ---- API ----

func (v *server) apiRun(w http.ResponseWriter, r *http.Request) {
	j, ok := v.apiJobFor(w, r)
	if !ok {
		return
	}
	if v.cfg.Runner == nil {
		apiErr(w, 503, errors.New("the job runner is not available in this server"))
		return
	}
	var in struct {
		Steps     any    `json:"steps"` // "tailor,cover", ["tailor","cover"] or "safe"
		Agent     string `json:"agent"`
		Force     bool   `json:"force"`
		KeepGoing bool   `json:"keep_going"`
		DryRun    bool   `json:"dry_run"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&in); err != nil {
			apiErr(w, 400, err)
			return
		}
	}
	list := ""
	switch s := in.Steps.(type) {
	case string:
		list = s
	case []any:
		for i, x := range s {
			if i > 0 {
				list += ","
			}
			list += fmt.Sprint(x)
		}
	}
	steps, err := runner.Parse(list)
	if err != nil {
		apiErr(w, 400, err)
		return
	}
	run, err := v.cfg.Runner.Start(j, runner.Request{Steps: steps, Agent: in.Agent, Force: in.Force, KeepGoing: in.KeepGoing, DryRun: in.DryRun})
	if err != nil {
		code := 400
		if run != nil {
			code = 409
		}
		apiErr(w, code, err)
		return
	}
	writeJSON(w, 202, map[string]any{"id": run.ID, "status": "/api/runs/" + run.ID, "run": run.Snapshot()})
}

func (v *server) apiRunGet(w http.ResponseWriter, r *http.Request) {
	run, ok := v.apiRunFor(w, r)
	if ok {
		writeJSON(w, 200, run.Snapshot())
	}
}

func (v *server) apiRunCancel(w http.ResponseWriter, r *http.Request) {
	run, ok := v.apiRunFor(w, r)
	if ok {
		v.cfg.Runner.Cancel(run.ID)
		writeJSON(w, 202, map[string]string{"id": run.ID, "state": "cancelling"})
	}
}

func (v *server) apiRunFor(w http.ResponseWriter, r *http.Request) (*runner.Run, bool) {
	if v.cfg.Runner == nil {
		apiErr(w, 404, errors.New("no such run"))
		return nil, false
	}
	run, ok := v.cfg.Runner.Get(r.PathValue("id"))
	if !ok {
		apiErr(w, 404, errors.New("no such run"))
	}
	return run, ok
}

func (v *server) apiAgents(w http.ResponseWriter, r *http.Request) {
	out := []runner.Agent{}
	if v.cfg.Runner != nil {
		out = v.cfg.Runner.Agents()
	}
	writeJSON(w, 200, map[string]any{"agents": out, "steps": runner.Steps})
}
