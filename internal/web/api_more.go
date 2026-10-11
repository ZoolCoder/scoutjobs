package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/search"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

// Parity with the CLI for agents that only have the JSON API: interviews, sessions, learning,
// preferences, decisions, skip and search. Sending e-mail stays in the CLI on purpose.

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	return json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(v)
}

func queryInt(r *http.Request, key string, def int) int {
	if n, err := strconv.Atoi(r.URL.Query().Get(key)); err == nil && n > 0 {
		return n
	}
	return def
}

// ---- interviews ----

func (v *server) apiInterviews(w http.ResponseWriter, r *http.Request) {
	var jobID int64
	if s := r.URL.Query().Get("job"); s != "" {
		jobID, _ = strconv.ParseInt(s, 10, 64)
	}
	ivs, err := v.s.Interviews(jobID, r.URL.Query().Get("all") == "")
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	if ivs == nil {
		ivs = []store.Interview{}
	}
	writeJSON(w, 200, ivs)
}

func (v *server) apiAddInterview(w http.ResponseWriter, r *http.Request) {
	var i store.Interview
	if err := decodeJSON(w, r, &i); err != nil {
		apiErr(w, 400, err)
		return
	}
	if i.JobID == 0 || i.Stage == "" {
		apiErr(w, 400, fmt.Errorf("job_id and stage are required"))
		return
	}
	j, err := v.s.JobByID(i.JobID)
	if err != nil {
		apiErr(w, 404, fmt.Errorf("job %d not found", i.JobID))
		return
	}
	id, err := v.s.AddInterview(i)
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	// same rule as `interview add`: scheduling an interview moves an earlier-stage job to interview
	switch j.Status {
	case "new", "ranked", "drafted", "applied":
		st := "interview"
		_ = v.s.UpdateJob(j.ID, store.JobPatch{Status: &st})
	}
	i.ID, i.Company, i.Title = id, j.Company, j.Title
	writeJSON(w, 201, i)
}

func (v *server) apiPatchInterview(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		apiErr(w, 400, err)
		return
	}
	var p struct {
		At, Format, Interviewer, PrepStatus, Outcome, Feedback, Stage, Location, Prep *string
		Duration                                                                      *int64
	}
	if err := decodeJSON(w, r, &p); err != nil {
		apiErr(w, 400, err)
		return
	}
	patch := store.InterviewPatch{At: p.At, Format: p.Format, Interviewer: p.Interviewer, PrepStatus: p.PrepStatus, Outcome: p.Outcome, Feedback: p.Feedback, Stage: p.Stage,
		Location: p.Location, Duration: p.Duration, Prep: p.Prep}
	if err := v.s.UpdateInterview(id, patch); err != nil {
		apiErr(w, 400, err)
		return
	}
	ivs, _ := v.s.Interviews(0, false)
	for _, i := range ivs {
		if i.ID == id {
			writeJSON(w, 200, i)
			return
		}
	}
	apiErr(w, 404, fmt.Errorf("interview %d not found", id))
}

// apiContacts is the referral finder: people in the imported LinkedIn network at the job's company.
func (v *server) apiContacts(w http.ResponseWriter, r *http.Request) {
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
	list, err := v.s.ContactsAt(j.Company)
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	if list == nil {
		list = []store.Contact{}
	}
	writeJSON(w, 200, map[string]any{"company": j.Company, "contacts": list})
}

// ---- sessions ----

func (v *server) apiSessions(w http.ResponseWriter, r *http.Request) {
	ks, err := v.s.Sessions(queryInt(r, "limit", 30))
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	if ks == nil {
		ks = []store.Session{}
	}
	writeJSON(w, 200, ks)
}

func (v *server) apiAddSession(w http.ResponseWriter, r *http.Request) {
	var k store.Session
	if err := decodeJSON(w, r, &k); err != nil {
		apiErr(w, 400, err)
		return
	}
	if k.Kind == "" {
		apiErr(w, 400, fmt.Errorf("kind is required"))
		return
	}
	if err := v.s.AddSession(k); err != nil {
		apiErr(w, 500, err)
		return
	}
	writeJSON(w, 201, map[string]any{"ok": true})
}

// ---- learning ----

func (v *server) apiLearning(w http.ResponseWriter, r *http.Request) {
	ls, err := v.s.LearningItems()
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	if ls == nil {
		ls = []store.Learning{}
	}
	writeJSON(w, 200, ls)
}

func (v *server) apiAddLearning(w http.ResponseWriter, r *http.Request) {
	var l store.Learning
	if err := decodeJSON(w, r, &l); err != nil {
		apiErr(w, 400, err)
		return
	}
	if strings.TrimSpace(l.Skill) == "" {
		apiErr(w, 400, fmt.Errorf("skill is required"))
		return
	}
	if err := v.s.UpsertLearning(l); err != nil {
		apiErr(w, 500, err)
		return
	}
	writeJSON(w, 201, map[string]any{"ok": true})
}

func (v *server) apiPatchLearning(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		apiErr(w, 400, err)
		return
	}
	var p struct {
		Status, Resource, Notes *string
		Hours                   *float64 // hours to ADD, like `learn set --hours`
		Priority, Demand        *int64
	}
	if err := decodeJSON(w, r, &p); err != nil {
		apiErr(w, 400, err)
		return
	}
	if err := v.s.UpdateLearning(id, store.LearningPatch{Status: p.Status, Resource: p.Resource, Notes: p.Notes, Hours: p.Hours, Priority: p.Priority, Demand: p.Demand}); err != nil {
		apiErr(w, 400, err)
		return
	}
	ls, _ := v.s.LearningItems()
	for _, l := range ls {
		if l.ID == id {
			writeJSON(w, 200, l)
			return
		}
	}
	apiErr(w, 404, fmt.Errorf("learning item %d not found", id))
}

// ---- preferences ----

func (v *server) apiPreferences(w http.ResponseWriter, r *http.Request) {
	ps, err := v.s.Preferences()
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	if ps == nil {
		ps = []store.Preference{}
	}
	writeJSON(w, 200, ps)
}

func (v *server) apiSetPreference(w http.ResponseWriter, r *http.Request) {
	var p store.Preference
	if err := decodeJSON(w, r, &p); err != nil {
		apiErr(w, 400, err)
		return
	}
	p.Key = r.PathValue("key")
	if p.Key == "" || p.Value == "" {
		apiErr(w, 400, fmt.Errorf("key and value are required"))
		return
	}
	if err := v.s.SetPreference(p); err != nil {
		apiErr(w, prefErrCode(err), err)
		return
	}
	writeJSON(w, 200, p)
}

func prefErrCode(err error) int {
	switch {
	case errors.Is(err, store.ErrStated):
		return 409
	case errors.Is(err, store.ErrNoProposal):
		return 404
	}
	return 400
}

func (v *server) apiProposals(w http.ResponseWriter, r *http.Request) {
	ps, err := v.s.Proposals(r.URL.Query().Get("all") == "true")
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	if ps == nil {
		ps = []store.Proposal{}
	}
	writeJSON(w, 200, ps)
}

func (v *server) apiPropose(w http.ResponseWriter, r *http.Request) {
	var p store.Proposal
	if err := decodeJSON(w, r, &p); err != nil {
		apiErr(w, 400, err)
		return
	}
	p.Key = r.PathValue("key")
	if err := v.s.ProposePreference(p); err != nil {
		apiErr(w, prefErrCode(err), err)
		return
	}
	writeJSON(w, 201, p)
}

// apiReviewProposal takes {"action":"confirm|reject","value":"edited wording","reason":"..."}.
func (v *server) apiReviewProposal(w http.ResponseWriter, r *http.Request) {
	var in struct{ Action, Value, Reason string }
	if err := decodeJSON(w, r, &in); err != nil {
		apiErr(w, 400, err)
		return
	}
	pref, err := v.reviewProposal(r.PathValue("key"), in.Action, in.Value, in.Reason)
	if err != nil {
		apiErr(w, prefErrCode(err), err)
		return
	}
	if pref == nil {
		w.WriteHeader(204)
		return
	}
	writeJSON(w, 200, pref)
}

// reviewProposal is shared by the Preferences page and the API.
func (v *server) reviewProposal(key, action, value, reason string) (*store.Preference, error) {
	switch action {
	case "confirm":
		p, err := v.s.ConfirmProposal(key, value)
		if err != nil {
			return nil, err
		}
		return &p, nil
	case "reject":
		return nil, v.s.RejectProposal(key, reason)
	}
	return nil, fmt.Errorf("action must be confirm or reject, not %q", action)
}

func (v *server) apiDeletePreference(w http.ResponseWriter, r *http.Request) {
	if err := v.s.DeletePreference(r.PathValue("key")); err != nil {
		apiErr(w, 500, err)
		return
	}
	w.WriteHeader(204)
}

// ---- decisions ----

func (v *server) apiDecisions(w http.ResponseWriter, r *http.Request) {
	ds, err := v.s.Decisions(queryInt(r, "limit", 50))
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	if ds == nil {
		ds = []store.Decision{}
	}
	writeJSON(w, 200, ds)
}

func (v *server) apiAddDecision(w http.ResponseWriter, r *http.Request) {
	var d struct {
		JobID   int64           `json:"job_id"`
		Kind    string          `json:"kind"`
		Reason  string          `json:"reason"`
		Signals json.RawMessage `json:"signals"`
	}
	if err := decodeJSON(w, r, &d); err != nil {
		apiErr(w, 400, err)
		return
	}
	if d.Kind == "" || strings.TrimSpace(d.Reason) == "" {
		apiErr(w, 400, fmt.Errorf("kind and reason are required"))
		return
	}
	if err := v.s.AddDecision(d.JobID, d.Kind, d.Reason, string(d.Signals)); err != nil {
		apiErr(w, 500, err)
		return
	}
	writeJSON(w, 201, map[string]any{"ok": true})
}

// apiSkip is `job skip`: status skipped plus the decision the learner reads.
func (v *server) apiSkip(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		apiErr(w, 400, err)
		return
	}
	var in struct {
		Reason  string          `json:"reason"`
		Signals json.RawMessage `json:"signals"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		apiErr(w, 400, err)
		return
	}
	if strings.TrimSpace(in.Reason) == "" {
		apiErr(w, 400, fmt.Errorf("reason is required"))
		return
	}
	if _, err := v.s.JobByID(id); err != nil {
		apiErr(w, 404, fmt.Errorf("job %d not found", id))
		return
	}
	st := "skipped"
	if err := v.s.UpdateJob(id, store.JobPatch{Status: &st}); err != nil {
		apiErr(w, 400, err)
		return
	}
	if err := v.s.AddDecision(id, "skip", in.Reason, string(in.Signals)); err != nil {
		apiErr(w, 500, err)
		return
	}
	j, _ := v.s.JobByID(id)
	writeJSON(w, 200, j)
}

// ---- search ----

// apiSearch runs the built-in board search; with add:true matches land in the tracker as status new.
func (v *server) apiSearch(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Queries []string `json:"queries"`
		Boards  []string `json:"boards"`
		Remote  bool     `json:"remote"`
		Limit   int      `json:"limit"`
		Add     bool     `json:"add"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		apiErr(w, 400, err)
		return
	}
	if len(in.Queries) == 0 {
		in.Queries = v.cfg.SearchQueries
	}
	if len(in.Boards) == 0 {
		in.Boards = v.cfg.SearchBoards
	}
	if len(in.Boards) == 0 && !in.Remote {
		apiErr(w, 400, fmt.Errorf("nothing to search: give boards or remote:true"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	res := search.Run(ctx, search.Options{Queries: in.Queries, Boards: in.Boards, Remote: in.Remote, Limit: in.Limit, BaseURL: v.cfg.SearchBaseURL})
	type row struct {
		search.Posting
		ID      int64 `json:"id,omitempty"`
		Created bool  `json:"created,omitempty"`
	}
	rows := make([]row, 0, len(res.Postings))
	added := 0
	for _, p := range res.Postings {
		rw := row{Posting: p}
		if in.Add {
			j, created, err := v.s.UpsertJob(p.Job())
			if err != nil {
				apiErr(w, 500, err)
				return
			}
			rw.ID, rw.Created = j.ID, created
			if created {
				added++
			}
		}
		rows = append(rows, rw)
	}
	if res.Errors == nil {
		res.Errors = []string{}
	}
	writeJSON(w, 200, map[string]any{"postings": rows, "added": added, "errors": res.Errors})
}
