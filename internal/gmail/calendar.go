package gmail

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"
)

// CalendarScope reads events; grants from before calendar sync have only this one.
const CalendarScope = "https://www.googleapis.com/auth/calendar.readonly"

// CalendarEventsScope reads and writes events. ScoutJobs writes only the interview events it created
// itself, without attendees, so Google sends no invitation; events of others are only read.
const CalendarEventsScope = "https://www.googleapis.com/auth/calendar.events"

// InterviewKey marks an event ScoutJobs created, in its private extended properties, with the interview id.
const InterviewKey = "scoutjobs_interview"

// EnableAPIs is the console page that turns on both APIs in the user's project in one step.
const EnableAPIs = "https://console.cloud.google.com/flows/enableapi?apiid=gmail.googleapis.com,calendar-json.googleapis.com"

// Event is a Google Calendar event, reduced to what the dashboard draws.
type Event struct {
	ID        string
	Status    string // confirmed, tentative or cancelled
	Title     string
	Start     time.Time
	End       time.Time
	AllDay    bool
	Location  string
	Link      string   // opens the event in Google Calendar
	Attendees []string // e-mail addresses
	Text      string   // description, for matching a company
	Interview string   // the ScoutJobs interview id when ScoutJobs created the event
}

// HasCalendar reports whether the saved grant includes calendar read; tokens from before it was asked do not.
func (c *Client) HasCalendar() bool {
	return c.granted(CalendarScope) || c.granted(CalendarEventsScope)
}

// CanWriteCalendar reports whether the grant lets ScoutJobs put interviews in Google Calendar.
func (c *Client) CanWriteCalendar() bool { return c.granted(CalendarEventsScope) }

func (c *Client) granted(scope string) bool {
	t, err := c.load()
	return err == nil && HasScope(t.Scope, scope)
}

// HasScope matches a whole scope: calendar.events must not match calendar.events.readonly.
func HasScope(granted, scope string) bool {
	for _, s := range strings.Fields(granted) {
		if s == scope {
			return true
		}
	}
	return false
}

// Events lists the primary calendar's events that overlap [from, to), recurring ones expanded.
func (c *Client) Events(ctx context.Context, from, to time.Time) ([]Event, error) {
	if !c.HasCalendar() {
		return nil, errors.New("google calendar not allowed yet: connect Gmail again and tick Calendar")
	}
	tok, err := c.access(ctx)
	if err != nil {
		return nil, err
	}
	q := url.Values{"timeMin": {from.Format(time.RFC3339)}, "timeMax": {to.Format(time.RFC3339)}, "singleEvents": {"true"},
		"orderBy": {"startTime"}, "maxResults": {"250"},
		"fields": {"items(id,status,summary,description,location,hangoutLink,htmlLink,start,end,attendees(email),extendedProperties)"}}
	var out struct {
		Items []apiEvent `json:"items"`
	}
	if err := c.callURL(ctx, tok, "GET", c.CalBase+"/calendars/primary/events?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	evs := make([]Event, 0, len(out.Items))
	for _, it := range out.Items {
		if it.Status == "cancelled" {
			continue
		}
		evs = append(evs, it.event())
	}
	return evs, nil
}

type apiEvent struct {
	ID, Status, Summary, Description, Location, HangoutLink, HTMLLink string
	Start, End                                                        struct{ Date, DateTime string }
	Attendees                                                         []struct{ Email string }
	ExtendedProperties                                                struct{ Private map[string]string }
}

func (it apiEvent) event() Event {
	e := Event{ID: it.ID, Status: it.Status, Title: it.Summary, Location: it.Location, Link: it.HTMLLink, Text: it.Description,
		Interview: it.ExtendedProperties.Private[InterviewKey]}
	if e.Location == "" {
		e.Location = it.HangoutLink
	}
	if it.Start.DateTime != "" {
		e.Start, _ = time.Parse(time.RFC3339, it.Start.DateTime)
		e.End, _ = time.Parse(time.RFC3339, it.End.DateTime)
	} else {
		e.AllDay = true
		e.Start, _ = time.ParseInLocation("2006-01-02", it.Start.Date, time.Local)
		e.End, _ = time.ParseInLocation("2006-01-02", it.End.Date, time.Local)
	}
	for _, a := range it.Attendees {
		e.Attendees = append(e.Attendees, a.Email)
	}
	return e
}

// ErrEventGone is an event deleted in Google Calendar.
var ErrEventGone = errors.New("event deleted in google calendar")

// Event reads one event by id; ErrEventGone when it was deleted.
func (c *Client) Event(ctx context.Context, id string) (Event, error) {
	tok, err := c.access(ctx)
	if err != nil {
		return Event{}, err
	}
	var it apiEvent
	err = c.callURL(ctx, tok, "GET", c.CalBase+"/calendars/primary/events/"+url.PathEscape(id), nil, &it)
	if err != nil && (strings.Contains(err.Error(), " 404 ") || strings.Contains(err.Error(), " 410 ")) {
		return Event{}, ErrEventGone
	}
	if err == nil && it.Status == "cancelled" {
		return Event{}, ErrEventGone
	}
	return it.event(), err
}

// EventInput is an interview ScoutJobs puts in Google Calendar. It has no attendees: nobody is invited.
type EventInput struct {
	Title, Location, Text string
	Start, End            time.Time
	AllDay                bool
	Interview             string
}

func (in EventInput) body() map[string]any {
	when := func(t time.Time) map[string]string {
		if in.AllDay {
			return map[string]string{"date": t.Format("2006-01-02")}
		}
		return map[string]string{"dateTime": t.Format(time.RFC3339)}
	}
	return map[string]any{"summary": in.Title, "location": in.Location, "description": in.Text,
		"start": when(in.Start), "end": when(in.End),
		"extendedProperties": map[string]any{"private": map[string]string{InterviewKey: in.Interview}}}
}

// SaveEvent creates the event when id is empty, otherwise updates it, and returns its id.
// sendUpdates=none: Google e-mails nobody, and the event has no attendees anyway.
func (c *Client) SaveEvent(ctx context.Context, id string, in EventInput) (string, error) {
	if !c.CanWriteCalendar() {
		return "", errors.New("google calendar sync not allowed yet: connect Gmail again and tick Calendar")
	}
	tok, err := c.access(ctx)
	if err != nil {
		return "", err
	}
	method, u := "POST", c.CalBase+"/calendars/primary/events?sendUpdates=none"
	if id != "" {
		method, u = "PATCH", c.CalBase+"/calendars/primary/events/"+url.PathEscape(id)+"?sendUpdates=none"
	}
	var out struct{ ID string }
	err = c.callURL(ctx, tok, method, u, in.body(), &out)
	if err != nil && id != "" && (strings.Contains(err.Error(), " 404 ") || strings.Contains(err.Error(), " 410 ")) {
		return "", ErrEventGone
	}
	return out.ID, err
}

// apiError turns Google's usual setup failures into the step that fixes them.
func apiError(err error) error {
	m := err.Error()
	switch {
	case strings.Contains(m, "SERVICE_DISABLED") || strings.Contains(m, "accessNotConfigured") || strings.Contains(m, "has not been used in project"):
		return errors.New("an API is not enabled in your Google Cloud project: enable Gmail and Calendar at " + EnableAPIs + ", wait a minute, then retry")
	case strings.Contains(m, "ACCESS_TOKEN_SCOPE_INSUFFICIENT") || strings.Contains(m, "insufficientPermissions"):
		return errors.New("google did not grant this permission: connect Gmail again and tick every box on the consent page")
	}
	return err
}
