package web

import (
	"context"
	"strings"
	"sync"
	"time"

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

// googleEvent draws a Google event; one that ScoutJobs itself exported, or that is already tracked
// as an interview at the same time, is left out so nothing shows twice.
func googleEvent(g gmail.Event, open []store.Job, ivs []store.Interview) (calEvent, bool) {
	if strings.Contains(g.Text, "scoutjobs:") || g.Start.IsZero() {
		return calEvent{}, false
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
	if j, ok := companyOf(g, open); ok {
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

// companyOf finds the open job an event is about: the company named in it, or an attendee at its domain.
func companyOf(g gmail.Event, open []store.Job) (store.Job, bool) {
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
