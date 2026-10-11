// Package calsync keeps tracked interviews and the user's Google Calendar in step, both ways:
// interviews scheduled in ScoutJobs are added to Google Calendar (no attendees, so nobody is invited),
// events in Google Calendar that name a tracked company become interviews, and a move or a deletion
// on either side follows on the other. Only events ScoutJobs created are ever written.
package calsync

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/gmail"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

// Calendar is the part of the Google client the sync needs; tests fake it.
type Calendar interface {
	HasCalendar() bool
	CanWriteCalendar() bool
	Events(ctx context.Context, from, to time.Time) ([]gmail.Event, error)
	Event(ctx context.Context, id string) (gmail.Event, error)
	SaveEvent(ctx context.Context, id string, in gmail.EventInput) (string, error)
}

// Report counts what one sync changed.
type Report struct {
	Added   int `json:"added"`   // interviews put in Google Calendar
	Updated int `json:"updated"` // Google events changed after an edit in ScoutJobs
	Moved   int `json:"moved"`   // interviews changed after an edit in Google Calendar
	Matched int `json:"matched"` // Google events that became interviews
	Removed int `json:"removed"` // interviews whose event was deleted in Google Calendar
}

func (r Report) String() string {
	var parts []string
	for _, p := range []struct {
		n    int
		what string
	}{{r.Matched, "found in your calendar"}, {r.Added, "added to Google Calendar"}, {r.Updated, "updated in Google Calendar"},
		{r.Moved, "moved to follow Google Calendar"}, {r.Removed, "deleted in Google Calendar"}} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", p.n, p.what))
		}
	}
	if len(parts) == 0 {
		return "calendar in sync"
	}
	return "interviews: " + strings.Join(parts, ", ")
}

// Window is the stretch of calendar a sync looks at.
const back, ahead = 30 * 24 * time.Hour, 180 * 24 * time.Hour

// Sync runs once; it changes nothing when Calendar is not connected.
func Sync(ctx context.Context, s *store.Store, c Calendar, now time.Time) (Report, error) {
	var r Report
	if c == nil || !c.HasCalendar() {
		return r, nil
	}
	from, to := now.Add(-back), now.Add(ahead)
	evs, err := c.Events(ctx, from, to)
	if err != nil {
		return r, err
	}
	byID := map[string]gmail.Event{}
	for _, e := range evs {
		byID[e.ID] = e
	}
	if err := match(s, evs, now, &r); err != nil {
		return r, err
	}
	ivs, err := s.Interviews(0, false)
	if err != nil {
		return r, err
	}
	var errs []error
	for _, iv := range ivs {
		if err := syncOne(ctx, s, c, iv, byID, from, to, now, &r); err != nil {
			errs = append(errs, fmt.Errorf("interview %d: %w", iv.ID, err))
		}
	}
	return r, errors.Join(errs...)
}

func syncOne(ctx context.Context, s *store.Store, c Calendar, iv store.Interview, byID map[string]gmail.Event, from, to, now time.Time, r *Report) error {
	switch iv.GoogleMode {
	case "gone":
		return nil
	case "":
		t, _, ok := ParseAt(iv.At)
		if !c.CanWriteCalendar() || iv.Outcome != "" || !ok || t.Before(now.Add(-24*time.Hour)) {
			return nil
		}
		id, err := c.SaveEvent(ctx, "", input(iv))
		if err != nil {
			return err
		}
		r.Added++
		_ = s.AddEvent(iv.JobID, "calendar", iv.Stage+" interview added to Google Calendar")
		return s.LinkGoogle(iv.ID, id, "push", localSig(iv)+"\n"+inputSig(input(iv)))
	}
	ev, ok := byID[iv.GoogleID]
	if !ok {
		t, _, parsed := ParseAt(iv.At)
		if !parsed || t.Before(from) || t.After(to) {
			return nil // outside the window: not listed, not gone
		}
		var err error
		if ev, err = c.Event(ctx, iv.GoogleID); errors.Is(err, gmail.ErrEventGone) {
			r.Removed++
			_ = s.AddEvent(iv.JobID, "calendar", iv.Stage+" interview deleted in Google Calendar; still tracked here")
			return s.LinkGoogle(iv.ID, iv.GoogleID, "gone", "")
		} else if err != nil {
			return err
		}
	}
	if iv.GoogleMode == "pull" {
		if eventSig(ev) == iv.GoogleSig {
			return nil
		}
		return follow(s, iv, ev, eventSig(ev), r)
	}
	// push: whichever side changed since the last sync wins; ScoutJobs wins a tie
	oldLocal, oldRemote, _ := strings.Cut(iv.GoogleSig, "\n")
	if localSig(iv) != oldLocal {
		if !c.CanWriteCalendar() {
			return nil
		}
		in := input(iv)
		if _, err := c.SaveEvent(ctx, iv.GoogleID, in); errors.Is(err, gmail.ErrEventGone) {
			r.Removed++
			return s.LinkGoogle(iv.ID, iv.GoogleID, "gone", "")
		} else if err != nil {
			return err
		}
		r.Updated++
		return s.LinkGoogle(iv.ID, iv.GoogleID, "push", localSig(iv)+"\n"+inputSig(in))
	}
	if eventSig(ev) != oldRemote {
		if err := follow(s, iv, ev, "", r); err != nil {
			return err
		}
		ivs, _ := s.Interviews(iv.JobID, false)
		for _, n := range ivs {
			if n.ID == iv.ID {
				return s.LinkGoogle(iv.ID, iv.GoogleID, "push", localSig(n)+"\n"+eventSig(ev))
			}
		}
	}
	return nil
}

// follow copies an event's time and place onto its interview; sig, when set, is stored as the pull state.
func follow(s *store.Store, iv store.Interview, ev gmail.Event, sig string, r *Report) error {
	at, dur := when(ev)
	loc := ev.Location
	if loc == "" {
		loc = iv.Location
	}
	if err := s.UpdateInterview(iv.ID, store.InterviewPatch{At: &at, Duration: &dur, Location: &loc}); err != nil {
		return err
	}
	if at != iv.At {
		r.Moved++
		_ = s.AddEvent(iv.JobID, "calendar", iv.Stage+" interview moved to "+strings.Replace(at, "T", " ", 1)+" in Google Calendar")
	}
	if sig == "" {
		return nil
	}
	return s.LinkGoogle(iv.ID, iv.GoogleID, "pull", sig)
}

// match turns events in the user's calendar that are about a tracked job into interviews. An interview
// already tracked at the same time adopts the event instead, so nothing is counted twice.
func match(s *store.Store, evs []gmail.Event, now time.Time, r *Report) error {
	open, err := s.ListJobs(store.JobFilter{Open: true})
	if err != nil {
		return err
	}
	ivs, err := s.Interviews(0, false)
	if err != nil {
		return err
	}
	linked := map[string]bool{}
	for _, iv := range ivs {
		linked[iv.GoogleID] = true
	}
	for _, ev := range evs {
		if ev.Interview != "" || linked[ev.ID] || strings.Contains(ev.Text, "scoutjobs:") || ev.Start.Before(now.Add(-7*24*time.Hour)) || !Interviewish(ev.Title) {
			continue
		}
		j, ok := MatchJob(ev, open)
		if !ok {
			continue
		}
		at, dur := when(ev)
		adopted := false
		for _, iv := range ivs {
			if iv.JobID == j.ID && iv.GoogleID == "" && strings.HasPrefix(iv.At, at) {
				if err := s.LinkGoogle(iv.ID, ev.ID, "pull", eventSig(ev)); err != nil {
					return err
				}
				adopted = true
				break
			}
		}
		if adopted {
			continue
		}
		id, err := s.AddInterview(store.Interview{JobID: j.ID, Stage: Stage(ev.Title), At: at, Duration: dur, Location: ev.Location,
			Format: format(ev.Location)})
		if err != nil {
			return err
		}
		r.Matched++
		_ = s.AddEvent(j.ID, "calendar", "found in Google Calendar: "+ev.Title)
		if err := s.LinkGoogle(id, ev.ID, "pull", eventSig(ev)); err != nil {
			return err
		}
	}
	return nil
}

// MatchJob finds the open job an event is about: the company named in it, or an attendee at its domain.
func MatchJob(g gmail.Event, open []store.Job) (store.Job, bool) {
	text := strings.ToLower(g.Title + " " + g.Location + " " + g.Text)
	for _, j := range open {
		name := strings.ToLower(strings.TrimSpace(j.Company))
		if len(name) < 3 {
			continue
		}
		if strings.Contains(text, name) {
			return j, true
		}
		domain := strings.ReplaceAll(name, " ", "")
		for _, a := range g.Attendees {
			if _, host, ok := strings.Cut(strings.ToLower(a), "@"); ok && strings.HasPrefix(host, domain+".") {
				return j, true
			}
		}
	}
	return store.Job{}, false
}

var interviewWords = []string{"interview", "screen", "call", "chat", "onsite", "on-site", "technical", "coding", "recruiter",
	"hiring", "meet", "conversation", "intro", "assessment", "panel", "system design", "behavioral", "final round", "loop"}

// Interviewish keeps a lunch with a friend at the company from becoming an interview.
func Interviewish(title string) bool {
	t := strings.ToLower(title)
	for _, w := range interviewWords {
		if strings.Contains(t, w) {
			return true
		}
	}
	return false
}

// Stage guesses the interview stage from an event title.
func Stage(title string) string {
	t := strings.ToLower(title)
	for _, m := range []struct{ word, stage string }{
		{"system design", "system_design"}, {"behavio", "behavioral"}, {"onsite", "onsite"}, {"on-site", "onsite"},
		{"final", "final"}, {"technical", "technical"}, {"coding", "technical"}, {"screen", "screen"}, {"recruiter", "screen"},
		{"intro", "screen"}, {"case", "case"},
	} {
		if strings.Contains(t, m.word) {
			return m.stage
		}
	}
	return "other"
}

func format(location string) string {
	l := strings.ToLower(location)
	switch {
	case strings.Contains(l, "meet.google") || strings.Contains(l, "zoom") || strings.Contains(l, "teams") || strings.Contains(l, "http"):
		return "video"
	case location != "":
		return "onsite"
	}
	return ""
}

// ParseAt reads an interview time the way the dashboard writes it; a date alone means all day.
func ParseAt(s string) (t time.Time, timed, ok bool) {
	s = strings.TrimSpace(s)
	for _, l := range []string{"2006-01-02T15:04", "2006-01-02T15:04:05", "2006-01-02 15:04"} {
		if t, err := time.ParseInLocation(l, s, time.Local); err == nil {
			return t, true, true
		}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Local(), true, true
	}
	if len(s) >= 10 {
		if t, err := time.ParseInLocation("2006-01-02", s[:10], time.Local); err == nil {
			return t, false, true
		}
	}
	return time.Time{}, false, false
}

func when(ev gmail.Event) (string, int64) {
	if ev.AllDay {
		return ev.Start.Local().Format("2006-01-02"), 60
	}
	dur := int64(ev.End.Sub(ev.Start).Minutes())
	if dur <= 0 {
		dur = 60
	}
	return ev.Start.Local().Format("2006-01-02T15:04"), dur
}

func input(iv store.Interview) gmail.EventInput {
	t, timed, _ := ParseAt(iv.At)
	dur := time.Duration(max(iv.Duration, 15)) * time.Minute
	in := gmail.EventInput{
		Title:     "Interview: " + iv.Company + ", " + strings.ReplaceAll(iv.Stage, "_", " "),
		Location:  iv.Location,
		Start:     t,
		End:       t.Add(dur),
		Interview: strconv.FormatInt(iv.ID, 10),
		Text: "Tracked by ScoutJobs: " + iv.Title + " at " + iv.Company + "." +
			strings.TrimSpace("\n"+withPrefix("With ", iv.Interviewer)+"\n"+withPrefix("Format: ", iv.Format)) +
			"\nscoutjobs:interview:" + strconv.FormatInt(iv.ID, 10),
	}
	if !timed {
		in.AllDay, in.End = true, t.AddDate(0, 0, 1)
	}
	return in
}

func withPrefix(p, v string) string {
	if v == "" {
		return ""
	}
	return p + v
}

func localSig(iv store.Interview) string {
	return strings.Join([]string{iv.At, strconv.FormatInt(iv.Duration, 10), iv.Location, iv.Stage, iv.Interviewer, iv.Format}, "|")
}

func inputSig(in gmail.EventInput) string {
	return eventSig(gmail.Event{Start: in.Start, End: in.End, AllDay: in.AllDay})
}

// eventSig is the time of an event; a moved event changes it, a renamed one does not.
func eventSig(ev gmail.Event) string {
	at, dur := when(ev)
	return at + "|" + strconv.FormatInt(dur, 10)
}
