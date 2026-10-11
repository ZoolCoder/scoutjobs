package gmail

import (
	"context"
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
