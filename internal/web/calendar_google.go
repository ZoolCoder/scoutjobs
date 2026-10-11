package web

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/calsync"
	"github.com/zoolcoder/scoutjobs/internal/gmail"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

// googleCal caches the user's Google Calendar around the viewed date, so paging through weeks
// does not call Google on every click.
type googleCal struct {
	mu      sync.Mutex
	from    time.Time
	fetched time.Time
	events  []calEvent
	err     string

	syncMu  sync.Mutex // one two-way sync at a time
	synced  time.Time
	syncErr string
}

const googleTTL = 5 * time.Minute

// googleEvents returns read-only Google Calendar events around d: six weeks back, ten ahead.
// It returns nothing when Calendar is not connected; a failure is reported, never fatal.
func (v *server) googleEvents(ctx context.Context, d time.Time) ([]calEvent, string) {
	c := v.gmailClient()
	if c == nil || !c.HasCalendar() {
		return nil, ""
	}
	from := midnight(d).AddDate(0, 0, -42)
	v.gcal.mu.Lock()
	defer v.gcal.mu.Unlock()
	if v.gcal.from.Equal(from) && time.Since(v.gcal.fetched) < googleTTL {
		return v.gcal.events, v.gcal.err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	evs, err := c.Events(ctx, from, from.AddDate(0, 0, 112))
	v.gcal.from, v.gcal.fetched, v.gcal.events, v.gcal.err = from, time.Now(), nil, ""
	if err != nil {
		v.gcal.err = err.Error()
		return nil, v.gcal.err
	}
	open, _ := v.s.ListJobs(store.JobFilter{Open: true})
	ivs, _ := v.s.Interviews(0, false)
	for _, g := range evs {
		if e, ok := googleEvent(g, open, ivs); ok {
			v.gcal.events = append(v.gcal.events, e)
		}
	}
	return v.gcal.events, ""
}

// syncCalendar runs the two-way sync with Google Calendar, at most every googleTTL unless forced.
// The error is the last sync's, so a page shows it until a sync succeeds.
func (v *server) syncCalendar(ctx context.Context, force bool) (calsync.Report, string) {
	c := v.gmailClient()
	if c == nil || !c.HasCalendar() {
		return calsync.Report{}, ""
	}
	v.gcal.syncMu.Lock()
	defer v.gcal.syncMu.Unlock()
	if !force && time.Since(v.gcal.synced) < googleTTL {
		return calsync.Report{}, v.gcal.syncErr
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	rep, err := calsync.Sync(ctx, v.s, c, time.Now())
	v.gcal.synced, v.gcal.syncErr = time.Now(), ""
	if err != nil {
		v.gcal.syncErr = err.Error()
	}
	if rep != (calsync.Report{}) {
		v.gcal.mu.Lock()
		v.gcal.fetched = time.Time{} // the events changed: read them again
		v.gcal.mu.Unlock()
	}
	return rep, v.gcal.syncErr
}

// syncSoon pushes an interview just created or edited to Google Calendar without holding up the page.
func (v *server) syncSoon() {
	go func() { _, _ = v.syncCalendar(context.Background(), true) }()
}

// calendarSync is the Calendar page's "Sync now".
func (v *server) calendarSync(w http.ResponseWriter, r *http.Request) {
	if err := sameOrigin(r); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	rep, serr := v.syncCalendar(r.Context(), true)
	dest := back(r, "/calendar")
	if serr != "" {
		http.Redirect(w, r, withParam(dest, "err", "Google Calendar sync: "+serr), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, withParam(dest, "msg", "Google Calendar: "+rep.String()), http.StatusSeeOther)
}

// googleEvent draws a Google event; one that ScoutJobs itself wrote or exported, or that is already
// tracked as an interview, is left out so nothing shows twice.
func googleEvent(g gmail.Event, open []store.Job, ivs []store.Interview) (calEvent, bool) {
	if strings.Contains(g.Text, "scoutjobs:") || g.Interview != "" || g.Start.IsZero() {
		return calEvent{}, false
	}
	for _, i := range ivs {
		if i.GoogleID == g.ID {
			return calEvent{}, false
		}
	}
	start, stop := g.Start.Local(), g.End.Local()
	if !stop.After(start) {
		stop = start.Add(time.Hour)
	}
	e := calEvent{Key: "g" + g.ID, Kind: "google", Title: g.Title, Sub: g.Location, Date: start.Format(dayLayout),
		Class: "c-google", start: start, stop: stop, allDay: g.AllDay, google: &g}
	if e.Title == "" {
		e.Title = "(busy)"
	}
	if !g.AllDay {
		e.Start, e.End = start.Format("15:04"), stop.Format("15:04")
	}
	if j, ok := calsync.MatchJob(g, open); ok {
		e.JobID = j.ID
		e.Sub = j.Company + " · " + j.Title
		for _, i := range ivs {
			if i.JobID == j.ID && strings.HasPrefix(i.At, start.Format("2006-01-02T15:04")) {
				return calEvent{}, false
			}
		}
	}
	return e, true
}

func (v *server) googleByKey(id string) (calEvent, bool) {
	v.gcal.mu.Lock()
	defer v.gcal.mu.Unlock()
	for _, e := range v.gcal.events {
		if e.Key == "g"+id {
			return e, true
		}
	}
	return calEvent{}, false
}
