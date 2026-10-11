package gmail

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"
)

// CalendarScope reads events; ScoutJobs never creates, moves or answers one.
const CalendarScope = "https://www.googleapis.com/auth/calendar.readonly"

// EnableAPIs is the console page that turns on both APIs in the user's project in one step.
const EnableAPIs = "https://console.cloud.google.com/flows/enableapi?apiid=gmail.googleapis.com,calendar-json.googleapis.com"

// Event is a Google Calendar event, reduced to what the dashboard draws.
type Event struct {
	ID        string
	Title     string
	Start     time.Time
	End       time.Time
	AllDay    bool
	Location  string
	Link      string   // opens the event in Google Calendar
	Attendees []string // e-mail addresses
	Text      string   // description, for matching a company
}

// HasCalendar reports whether the saved grant includes calendar read; tokens from before it was asked do not.
func (c *Client) HasCalendar() bool {
	t, err := c.load()
	return err == nil && strings.Contains(t.Scope, CalendarScope)
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
		"fields": {"items(id,status,summary,description,location,htmlLink,start,end,attendees(email))"}}
	var out struct {
		Items []struct {
			ID, Status, Summary, Description, Location, HTMLLink string
			Start, End                                           struct{ Date, DateTime string }
			Attendees                                            []struct{ Email string }
		} `json:"items"`
	}
	if err := c.callURL(ctx, tok, "GET", c.CalBase+"/calendars/primary/events?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	evs := make([]Event, 0, len(out.Items))
	for _, it := range out.Items {
		if it.Status == "cancelled" {
			continue
		}
		e := Event{ID: it.ID, Title: it.Summary, Location: it.Location, Link: it.HTMLLink, Text: it.Description}
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
		evs = append(evs, e)
	}
	return evs, nil
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
