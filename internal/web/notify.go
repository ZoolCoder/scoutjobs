package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/zoolcoder/scoutjobs/internal/autopilot"
)

// notice is one browser notification: something the chores or the AI assistant finished.
type notice struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Href  string `json:"href"`
}

type notices struct {
	Last  int64    `json:"last"` // the newest event id, the next call's after
	Items []notice `json:"items"`
}

// notifyJSON lists what happened after the event id the browser saw last; without one it only returns
// the newest id, so turning notifications on does not replay the past.
func (v *server) notifyJSON(w http.ResponseWriter, r *http.Request) {
	events, err := v.s.Events(0, 100)
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	var out notices
	out.Items = []notice{}
	if len(events) > 0 {
		out.Last = events[0].ID
	}
	after, err := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	if err != nil || after <= 0 {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, out)
		return
	}
	digest := 0
	for _, e := range events {
		if e.ID <= after {
			continue
		}
		out.Last = max(out.Last, e.ID)
		job := fmt.Sprintf("/jobs/%d", e.JobID)
		switch e.Kind {
		case autopilot.DigestEvent:
			digest++
		case "email:proposal":
			kind, _, _ := strings.Cut(e.Note, " ")
			out.Items = append(out.Items, notice{"Review the " + proposalLabel(kind) + " to " + e.Company, "Drafted for you, not sent.", job + "?tab=mail#emails"})
		case "run:done":
			out.Items = append(out.Items, notice{"Your assistant finished " + e.Company, e.Title, job})
		case "run:failed":
			out.Items = append(out.Items, notice{"Preparing " + e.Company + " stopped", e.Note, job})
		}
	}
	if digest > 0 {
		out.Items = append(out.Items, notice{fmt.Sprintf("%d new %s from your daily searches", digest, plural(digest, "job", "jobs")), "Best fit first.", "/pipeline?sort=fit"})
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, out)
}

func proposalLabel(kind string) string {
	switch kind {
	case "followup":
		return "follow-up"
	case "thanks":
		return "thank-you"
	}
	return "e-mail"
}
