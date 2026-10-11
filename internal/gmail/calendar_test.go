package gmail

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEventsReadsPrimaryCalendar(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cal/calendars/primary/events" || r.URL.Query().Get("singleEvents") != "true" || r.Header.Get("Authorization") != "Bearer at" {
			w.WriteHeader(404)
			return
		}
		_, _ = w.Write([]byte(`{"items":[
			{"id":"e1","status":"confirmed","summary":"Acme technical interview","htmlLink":"https://calendar.google.com/e1",
			 "start":{"dateTime":"2026-10-20T14:00:00+02:00"},"end":{"dateTime":"2026-10-20T15:00:00+02:00"},"attendees":[{"email":"hr@acme.com"}]},
			{"id":"e2","status":"cancelled","summary":"gone","start":{"date":"2026-10-21"},"end":{"date":"2026-10-22"}},
			{"id":"e3","summary":"Holiday","start":{"date":"2026-10-23"},"end":{"date":"2026-10-24"}}]}`))
	}))
	defer srv.Close()
	c := &Client{ID: "cid", Home: t.TempDir(), HTTP: srv.Client(), CalBase: srv.URL + "/cal"}
	tok := Token{AccessToken: "at", Expiry: time.Now().Add(time.Hour)}
	_ = c.save(tok)
	if _, err := c.Events(context.Background(), time.Now(), time.Now()); err == nil || !strings.Contains(err.Error(), "connect Gmail again") {
		t.Fatalf("token without calendar scope: %v", err)
	}
	tok.Scope = "https://www.googleapis.com/auth/gmail.readonly " + CalendarScope
	_ = c.save(tok)
	evs, err := c.Events(context.Background(), time.Now(), time.Now().AddDate(0, 0, 14))
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 || evs[0].Title != "Acme technical interview" || evs[0].End.Sub(evs[0].Start) != time.Hour || evs[0].Attendees[0] != "hr@acme.com" || !evs[1].AllDay {
		t.Errorf("%+v", evs)
	}
}

func TestAPIErrorNamesTheFix(t *testing.T) {
	if err := apiError(errors.New(`google GET /x: 403 {"reason":"SERVICE_DISABLED"}`)); !strings.Contains(err.Error(), "enableapi") {
		t.Errorf("disabled API: %v", err)
	}
	if err := apiError(errors.New(`403 ACCESS_TOKEN_SCOPE_INSUFFICIENT`)); !strings.Contains(err.Error(), "connect Gmail again") {
		t.Errorf("scope: %v", err)
	}
}

func TestSaveEventWritesOnlyItsOwnEventsAndInvitesNobody(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("sendUpdates") != "none" && r.Method != "GET" {
			t.Errorf("%s %s notifies guests", r.Method, r.URL)
		}
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b)
		switch {
		case r.Method == "POST" && r.URL.Path == "/cal/calendars/primary/events":
			_, _ = w.Write([]byte(`{"id":"new1"}`))
		case r.URL.Path == "/cal/calendars/primary/events/new1":
			_, _ = w.Write([]byte(`{"id":"new1","status":"confirmed","start":{"dateTime":"2026-10-20T14:00:00Z"},"end":{"dateTime":"2026-10-20T15:00:00Z"},
				"hangoutLink":"https://meet.google.com/abc","extendedProperties":{"private":{"scoutjobs_interview":"4"}}}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	c := &Client{ID: "cid", Home: t.TempDir(), HTTP: srv.Client(), CalBase: srv.URL + "/cal"}
	tok := Token{AccessToken: "at", Expiry: time.Now().Add(time.Hour), Scope: CalendarScope}
	_ = c.save(tok)
	start := time.Date(2026, 10, 20, 14, 0, 0, 0, time.UTC)
	in := EventInput{Title: "Interview: Acme, technical", Start: start, End: start.Add(time.Hour), Interview: "4"}
	if _, err := c.SaveEvent(context.Background(), "", in); err == nil || len(bodies) != 0 {
		t.Fatalf("read-only grant wrote: %v", err)
	}
	tok.Scope = CalendarEventsScope
	_ = c.save(tok)
	id, err := c.SaveEvent(context.Background(), "", in)
	if err != nil || id != "new1" {
		t.Fatalf("create %q %v", id, err)
	}
	if _, ok := bodies[0]["attendees"]; ok || bodies[0]["extendedProperties"].(map[string]any)["private"].(map[string]any)[InterviewKey] != "4" {
		t.Errorf("body %v", bodies[0])
	}
	if _, err := c.SaveEvent(context.Background(), "gone1", in); !errors.Is(err, ErrEventGone) {
		t.Errorf("patch of a deleted event: %v", err)
	}
	ev, err := c.Event(context.Background(), "new1")
	if err != nil || ev.Interview != "4" || ev.Location != "https://meet.google.com/abc" {
		t.Errorf("event %+v %v", ev, err)
	}
	if _, err := c.Event(context.Background(), "gone1"); !errors.Is(err, ErrEventGone) {
		t.Errorf("deleted event: %v", err)
	}
}
