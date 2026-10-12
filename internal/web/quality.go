package web

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/zoolcoder/scoutjobs/internal/apply"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

// preApply are the statuses a job leaves when it is marked applied for the first time.
var preApply = map[string]bool{"new": true, "ranked": true, "drafted": true}

// checksFailed stops a job with drafts from being marked applied while a sending check fails.
type checksFailed struct{ failed int }

func (e checksFailed) Error() string {
	return fmt.Sprintf("%d sending check(s) failed. Fix them, or give a reason to apply anyway.", e.failed)
}

// qualityNote runs the sending checks when a job with drafts is about to be marked applied. It
// returns the note of the quality event to log once the status changed, or checksFailed when a check
// fails and no override reason was given. Jobs without drafts were applied to by hand: not checked.
func (v *server) qualityNote(j store.Job, to, override string) (string, error) {
	if to != "applied" || !preApply[j.Status] {
		return "", nil
	}
	if !apply.HasDrafts(v.s, j) {
		return "no drafts", nil
	}
	events, _ := v.s.Events(j.ID, 200)
	rep := apply.Check(v.s, j, events, apply.CheckOptions{AllowAcknowledged: true})
	if rep.Failed == 0 {
		return fmt.Sprintf("pass score %d", rep.Score()), nil
	}
	override = strings.TrimSpace(override)
	if override == "" {
		return "", checksFailed{rep.Failed}
	}
	return fmt.Sprintf("override score %d: %s", rep.Score(), override), nil
}

// logQuality records what the checks said when the job was marked applied; Reviews compares the
// answer rates of the groups.
func (v *server) logQuality(id int64, note string) {
	if note == "" {
		return
	}
	if reason, ok := strings.CutPrefix(note, "override "); ok {
		_ = v.s.AddEvent(id, "check:override", reason)
	}
	_ = v.s.AddEvent(id, "quality", note)
}

// gatesURL sends the user to the failing checks with the override form.
func gatesURL(id int64, err error) string {
	return fmt.Sprintf("/jobs/%d?err=%s&override=1#gates", id, url.QueryEscape(err.Error()))
}
