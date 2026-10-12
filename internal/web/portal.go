package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/zoolcoder/scoutjobs/internal/apply"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

// portalPanel is the proceed-with-gaps flow of the job page: apply on the employer's portal with
// must-have gaps the user acknowledged. It never turns a gap into evidence and never submits.
type portalPanel struct {
	Gaps    []string // coverage gaps still to acknowledge
	Acked   string   // date of the acknowledgement that covers the current gaps
	Note    string
	Blocked []string // other failed gates; the flow waits for them
	URL     string
	Applied bool
}

func (v *server) portal(j store.Job, gates apply.Report, events []store.Event) *portalPanel {
	p := &portalPanel{Gaps: gates.PendingGaps(events), URL: safeURL(j.URL), Applied: j.Status != "new" && j.Status != "ranked" && j.Status != "drafted"}
	for _, g := range gates.Gates {
		if g.Name != "coverage" || g.State != apply.Fail || len(p.Gaps) > 0 {
			continue
		}
		if e, ok := apply.Acknowledgement(events, g); ok {
			p.Acked = e.At
			if _, n, ok := strings.Cut(e.Note, " | "); ok {
				p.Note = n
			}
		}
	}
	if len(p.Gaps) == 0 && p.Acked == "" {
		return nil
	}
	for _, g := range apply.Check(v.s, j, events, apply.CheckOptions{AllowAcknowledged: true}).Gates {
		if g.State == apply.Fail {
			p.Blocked = append(p.Blocked, g.Name+": "+g.Detail)
		}
	}
	return p
}

// safeURL keeps only http(s) links, so a posting URL cannot become a script link on the page.
func safeURL(u string) string {
	if p, err := url.Parse(u); err == nil && (p.Scheme == "https" || p.Scheme == "http") && p.Host != "" {
		return u
	}
	return ""
}

func (v *server) acknowledgeGaps(w http.ResponseWriter, r *http.Request) {
	j, ok := v.formJob(w, r)
	if !ok {
		return
	}
	if sameOrigin(r) != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	_ = r.ParseForm()
	u := fmt.Sprintf("/jobs/%d", j.ID)
	if _, err := apply.Acknowledge(v.s, j, "coverage", r.FormValue("notes"), r.Form["gap"]); err != nil {
		u += "?err=" + url.QueryEscape(err.Error())
	} else {
		u += "?msg=" + url.QueryEscape("Gaps acknowledged. Apply on the official posting, then come back and mark it applied.")
	}
	http.Redirect(w, r, u+"#portal", http.StatusSeeOther)
}

// markApplied is "Yes, mark applied" after a portal submission: the status change is the dated record.
func (v *server) markApplied(w http.ResponseWriter, r *http.Request) {
	j, ok := v.formJob(w, r)
	if !ok {
		return
	}
	if sameOrigin(r) != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if err := v.changeStatus(j.ID, "applied", "", r.FormValue("override")); err != nil {
		var cf checksFailed
		if errors.As(err, &cf) {
			http.Redirect(w, r, gatesURL(j.ID, err), http.StatusSeeOther)
			return
		}
		fail(w, err)
		return
	}
	note := "applied on the official posting"
	if n := strings.TrimSpace(r.FormValue("note")); n != "" {
		note += ": " + n
	}
	_ = v.s.AddEvent(j.ID, "note", note)
	http.Redirect(w, r, fmt.Sprintf("/jobs/%d?msg=%s", j.ID, url.QueryEscape("Marked applied.")), http.StatusSeeOther)
}

func (v *server) apiAcknowledge(w http.ResponseWriter, r *http.Request) {
	j, ok := v.apiJobFor(w, r)
	if !ok {
		return
	}
	var in struct {
		Gate  string   `json:"gate"`
		Notes string   `json:"notes"`
		Gaps  []string `json:"gaps"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
		apiErr(w, 400, err)
		return
	}
	if in.Gate == "" {
		in.Gate = "coverage"
	}
	g, err := apply.Acknowledge(v.s, j, in.Gate, in.Notes, in.Gaps)
	if err != nil {
		apiErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"job": j.ID, "acknowledged": g, "url": safeURL(j.URL)})
}
