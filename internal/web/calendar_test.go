package web

import (
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/gmail"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

func calendarHandler(t *testing.T, token string) (*store.Store, func(method, path, body string) *httptest.ResponseRecorder) {
	t.Helper()
	s := testStore(t)
	h, err := Handler(s, Config{FollowUpDays: 10, StaleDays: 60, APIToken: token})
	if err != nil {
		t.Fatal(err)
	}
	return s, func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
}

func TestCalendarViews(t *testing.T) {
	s, do := calendarHandler(t, "")
	tomorrow := time.Now().AddDate(0, 0, 1).Format(dayLayout)
	if _, err := s.AddInterview(store.Interview{JobID: 1, Stage: "screen", At: tomorrow + "T09:30", Duration: 30, Location: "https://meet.example/abc"}); err != nil {
		t.Fatal(err)
	}
	dl := tomorrow
	if err := s.UpdateJob(1, store.JobPatch{Deadline: &dl}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/calendar", "/calendar?view=day", "/calendar?view=month", "/calendar?view=schedule", "/calendar?view=week&d=" + tomorrow + "&ev=i2",
		"/calendar?view=month&ev=d1", "/calendar?new=" + tomorrow + "T14:00", "/calendar?view=bogus&d=nope&ev=x"} {
		rec := do("GET", p, "")
		b := rec.Body.String()
		if rec.Code != 200 || strings.Contains(b, "<no value>") || strings.Contains(b, "style=") {
			t.Errorf("%s: %d inline-style/no-value check failed", p, rec.Code)
		}
	}
	b := do("GET", "/calendar?view=week&d="+tomorrow+"&ev=i2", "").Body.String()
	for _, want := range []string{"Acme · screen", "09:30 – 10:00", "calendar.google.com/calendar/render?action=TEMPLATE", "/calendar/event/i2", "Deadline · Acme", "r-", "s-2"} {
		if !strings.Contains(b, want) {
			t.Errorf("week view missing %q", want)
		}
	}
}

func TestCalendarCreateAndReschedule(t *testing.T) {
	s, do := calendarHandler(t, "")
	rec := do("POST", "/interviews", "job=1&date=2999-02-03&time=15:00&stage=final&duration=90&format=onsite&location=HQ")
	if rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "d=2999-02-03") {
		t.Fatalf("create: %d %v", rec.Code, rec.Header())
	}
	if do("POST", "/interviews", "job=1&date=tomorrow").Code != 400 {
		t.Error("bad date accepted")
	}
	ivs, _ := s.Interviews(1, false)
	var got store.Interview
	for _, i := range ivs {
		if i.Stage == "final" {
			got = i
		}
	}
	if got.At != "2999-02-03T15:00" || got.Duration != 90 || got.Location != "HQ" {
		t.Fatalf("created %+v", got)
	}
	if j, _ := s.JobByID(1); j.Status != "interview" {
		t.Errorf("job status %q, want interview", j.Status)
	}
	do("POST", "/interviews/"+itoa(got.ID), "date=2999-02-04&time=&duration=45&location=")
	ivs, _ = s.Interviews(1, false)
	for _, i := range ivs {
		if i.ID == got.ID && (i.At != "2999-02-04" || i.Duration != 45 || i.Location != "") {
			t.Errorf("rescheduled %+v", i)
		}
	}
}

func TestCalendarICS(t *testing.T) {
	s, do := calendarHandler(t, "secret")
	if _, err := s.AddInterview(store.Interview{JobID: 1, Stage: "screen", At: "2999-03-01T09:00", Duration: 30, Location: "Room 1, floor 2"}); err != nil {
		t.Fatal(err)
	}
	if do("GET", "/calendar.ics", "").Code != 401 {
		t.Error("feed open without key")
	}
	rec := do("GET", "/calendar.ics?key="+feedKey("secret"), "")
	b := rec.Body.String()
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/calendar") {
		t.Fatalf("feed: %d %s", rec.Code, rec.Header())
	}
	want := time.Date(2999, 3, 1, 9, 0, 0, 0, time.Local).UTC().Format("20060102T150405Z")
	for _, w := range []string{"BEGIN:VCALENDAR\r\n", "UID:interview-2@scoutjobs", "DTSTART:" + want, `LOCATION:Room 1\, floor 2`, "BEGIN:VALARM", "END:VCALENDAR\r\n"} {
		if !strings.Contains(b, w) {
			t.Errorf("feed missing %q", w)
		}
	}
	for _, line := range strings.Split(b, "\r\n") {
		if len(line) > 75 {
			t.Errorf("unfolded line: %q", line)
		}
	}
	rec = do("GET", "/calendar/event/i2", "")
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Disposition"), "acme-interview.ics") {
		t.Errorf("event ics: %d %v", rec.Code, rec.Header())
	}
	if do("GET", "/calendar/event/z9", "").Code != 404 {
		t.Error("unknown event served")
	}
	if !strings.Contains(do("GET", "/calendar", "").Body.String(), "/calendar.ics?key="+feedKey("secret")) {
		t.Error("calendar page does not show the keyed feed")
	}
}

func TestICSFoldKeepsUTF8(t *testing.T) {
	var b strings.Builder
	icsLine(&b, "SUMMARY:"+strings.Repeat("é", 60))
	for _, l := range strings.Split(strings.TrimSuffix(b.String(), "\r\n"), "\r\n") {
		if len(l) > 75 || !strings.Contains(l, "é") || strings.ContainsRune(l, '�') {
			t.Errorf("bad fold %q", l)
		}
	}
}

func TestGoogleURLAllDay(t *testing.T) {
	e, ok := deadlineEvent(store.Job{ID: 4, Company: "Acme", Deadline: "2999-05-01"})
	if !ok {
		t.Fatal("deadline not parsed")
	}
	u := googleURL(e, "http://x/jobs/4")
	if !strings.Contains(u, "dates=29990501%2F29990502") || !strings.Contains(u, "scoutjobs%3Adeadline%3A4") {
		t.Errorf("google url %s", u)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestGoogleEventMatchesJobAndSkipsDuplicates(t *testing.T) {
	open := []store.Job{{ID: 7, Company: "Acme", Title: "Go Dev"}, {ID: 8, Company: "Globex Corp", Title: "SRE"}}
	start := time.Date(2026, 10, 20, 14, 0, 0, 0, time.Local)
	g := gmail.Event{ID: "e1", Title: "Technical round", Start: start, End: start.Add(time.Hour), Attendees: []string{"jane@globexcorp.com"}}
	e, ok := googleEvent(g, open, nil)
	if !ok || e.JobID != 8 || e.Key != "ge1" || e.Start != "14:00" || e.Kind != "google" {
		t.Fatalf("%+v %v", e, ok)
	}
	if _, ok := googleEvent(g, open, []store.Interview{{JobID: 8, At: "2026-10-20T14:00"}}); ok {
		t.Error("an event already tracked as an interview shows twice")
	}
	if _, ok := googleEvent(gmail.Event{ID: "x", Title: "Interview", Start: start, Text: "scoutjobs:interview:3"}, open, nil); ok {
		t.Error("an event ScoutJobs exported shows twice")
	}
	if _, ok := googleEvent(gmail.Event{ID: "w", Title: "Interview: Acme", Start: start, Interview: "4"}, open, nil); ok {
		t.Error("an event ScoutJobs wrote shows twice")
	}
	if _, ok := googleEvent(gmail.Event{ID: "m", Title: "Acme call", Start: start.Add(time.Hour)}, open, []store.Interview{{JobID: 7, GoogleID: "m"}}); ok {
		t.Error("an event linked to an interview shows twice")
	}
	if e, _ := googleEvent(gmail.Event{ID: "d", Title: "Dentist", Start: start}, open, nil); e.JobID != 0 || e.End != "15:00" {
		t.Errorf("unrelated event: %+v", e)
	}
}
