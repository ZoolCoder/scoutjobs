package apply

import (
	"errors"
	"fmt"
	"strings"

	"github.com/zoolcoder/scoutjobs/internal/store"
)

// Acked is the state of a failed gate the user acknowledged: still not a pass, but an informed decision.
const Acked = "ack"

// AckEvent records the acknowledgement on the job: "coverage: gap one; gap two | the user's note".
const AckEvent = "check:acknowledged"

// Acknowledge records the user's informed decision to apply with the failed items of a gate. seen
// must name every one of them. The job moves to drafted when it is earlier in the pipeline; it is
// marked applied only by the user.
func Acknowledge(s *store.Store, j store.Job, gate, notes string, seen []string) (Gate, error) {
	if gate != "coverage" { // a veto, missing documents or an unreviewed draft are never acknowledged away
		return Gate{}, fmt.Errorf("only coverage gaps can be acknowledged; %s has to pass", gate)
	}
	events, _ := s.Events(j.ID, 200)
	rep := Check(s, j, events, CheckOptions{})
	for _, g := range rep.Gates {
		if g.Name == "fit_review" && g.State == Fail {
			return Gate{}, fmt.Errorf("not acknowledged: %s; hard requirements are never bypassed", g.Detail)
		}
	}
	g, found := rep.gate(gate)
	if !found || !evidenceGaps(g) || len(g.Items) == 0 {
		return Gate{}, errors.New("coverage has no must-have gaps to acknowledge")
	}
	ok := map[string]bool{}
	for _, it := range seen {
		ok[strings.TrimSpace(it)] = true
	}
	var missed []string
	for _, it := range g.Items {
		if !ok[it] {
			missed = append(missed, it)
		}
	}
	if len(missed) > 0 {
		return Gate{}, fmt.Errorf("acknowledge each gap; not acknowledged: %s", strings.Join(missed, "; "))
	}
	note := ackNote(g)
	if n := strings.TrimSpace(notes); n != "" {
		note += " | " + n
	}
	if err := s.AddEvent(j.ID, AckEvent, note); err != nil {
		return Gate{}, err
	}
	if j.Status == "new" || j.Status == "ranked" {
		st := "drafted"
		_ = s.UpdateJob(j.ID, store.JobPatch{Status: &st})
	}
	return g, nil
}

func ackNote(g Gate) string { return g.Name + ": " + strings.Join(g.Items, "; ") }

// Acknowledgement is the newest acknowledgement that still covers the gate's current items: a new
// gap, or one that changed, needs a new acknowledgement.
func Acknowledgement(events []store.Event, g Gate) (store.Event, bool) {
	want := ackNote(g)
	for _, e := range events { // newest first
		if e.Kind == AckEvent && (e.Note == want || strings.HasPrefix(e.Note, want+" | ")) {
			return e, true
		}
	}
	return store.Event{}, false
}

// allowAcknowledged replaces an acknowledged coverage gate by what the acknowledgement leaves open:
// the agreed wording of the must-haves that do have evidence still has to be in the drafts.
func (r *Report) allowAcknowledged(events []store.Event, review FitReview, drafts map[string]string) {
	for i, g := range r.Gates {
		if !evidenceGaps(g) {
			continue
		}
		e, ok := Acknowledgement(events, g)
		if !ok {
			continue
		}
		w := gateWording(review, drafts)
		r.Failed--
		switch w.State {
		case Fail:
			r.Failed++
			w.Detail = "gaps acknowledged " + day(e.At) + "; " + w.Detail
		case Warn:
			r.Warned++
			w.Detail = "gaps acknowledged " + day(e.At) + "; " + w.Detail
		default:
			w.State, w.Detail, w.Items = Acked, fmt.Sprintf("%d must-have gap(s) acknowledged %s; no evidence added", len(g.Items), day(e.At)), g.Items
			r.Acked++
		}
		r.Gates[i] = w
	}
}

// evidenceGaps is a coverage failure caused by must-haves without evidence, the only kind the user
// can acknowledge; agreed wording missing from the drafts is fixed in the drafts instead.
func evidenceGaps(g Gate) bool {
	return g.Name == "coverage" && g.State == Fail && strings.HasSuffix(g.Detail, "without evidence")
}

func (r Report) gate(name string) (Gate, bool) {
	for _, g := range r.Gates {
		if g.Name == name {
			return g, true
		}
	}
	return Gate{}, false
}

// PendingGaps lists the coverage gaps the user can still acknowledge; nil when there is nothing to
// acknowledge or a hard requirement fails.
func (r Report) PendingGaps(events []store.Event) []string {
	for _, g := range r.Gates {
		if g.Name == "fit_review" && g.State == Fail {
			return nil
		}
	}
	g, ok := r.gate("coverage")
	if !ok || !evidenceGaps(g) {
		return nil
	}
	if _, acked := Acknowledgement(events, g); acked {
		return nil
	}
	return g.Items
}

func day(at string) string {
	if len(at) >= 10 {
		return at[:10]
	}
	return at
}
