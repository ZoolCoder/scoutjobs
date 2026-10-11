package calsync

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/gmail"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

// fakeCal is a Google Calendar in memory.
type fakeCal struct {
	write  bool
	events map[string]gmail.Event
	saves  int
	next   int
}

func (f *fakeCal) HasCalendar() bool      { return true }
func (f *fakeCal) CanWriteCalendar() bool { return f.write }
func (f *fakeCal) Events(_ context.Context, from, to time.Time) ([]gmail.Event, error) {
	var out []gmail.Event
	for _, e := range f.events {
		if !e.Start.Before(from) && e.Start.Before(to) {
			out = append(out, e)
		}
	}
	return out, nil
}
func (f *fakeCal) Event(_ context.Context, id string) (gmail.Event, error) {
	if e, ok := f.events[id]; ok {
		return e, nil
	}
	return gmail.Event{}, gmail.ErrEventGone
}
func (f *fakeCal) SaveEvent(_ context.Context, id string, in gmail.EventInput) (string, error) {
	f.saves++
	if id == "" {
		f.next++
		id = "sj" + strconv.Itoa(f.next)
	} else if _, ok := f.events[id]; !ok {
		return "", gmail.ErrEventGone
	}
	if len(in.Interview) == 0 {
		panic("event without interview id")
	}
	f.events[id] = gmail.Event{ID: id, Title: in.Title, Start: in.Start, End: in.End, AllDay: in.AllDay, Location: in.Location, Text: in.Text, Interview: in.Interview}
	return id, nil
}

func open(t *testing.T) *store.Store {
	t.Helper()
	t.Setenv("SCOUTJOBS_HOME", t.TempDir())
	s, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func job(t *testing.T, s *store.Store, company string) int64 {
	t.Helper()
	j, _, err := s.UpsertJob(store.Job{Company: company, Title: "Go Engineer", Source: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	return j.ID
}

func interview(t *testing.T, s *store.Store, id int64) store.Interview {
	t.Helper()
	ivs, _ := s.Interviews(0, false)
	for _, iv := range ivs {
		if iv.ID == id {
			return iv
		}
	}
	t.Fatalf("no interview %d", id)
	return store.Interview{}
}

func TestPushesInterviewsAndFollowsEditsBothWays(t *testing.T) {
	s := open(t)
	jid := job(t, s, "Acme")
	now := time.Now()
	at := now.Add(48 * time.Hour).Truncate(time.Hour)
	id, _ := s.AddInterview(store.Interview{JobID: jid, Stage: "technical", At: at.Format("2006-01-02T15:04"), Duration: 45, Location: "https://meet.google.com/x"})
	cal := &fakeCal{write: true, events: map[string]gmail.Event{}}

	r, err := Sync(context.Background(), s, cal, now)
	if err != nil || r.Added != 1 {
		t.Fatalf("report %+v, %v", r, err)
	}
	iv := interview(t, s, id)
	ev := cal.events[iv.GoogleID]
	if iv.GoogleMode != "push" || ev.Interview != strconv.FormatInt(id, 10) || ev.End.Sub(ev.Start) != 45*time.Minute || ev.Title != "Interview: Acme, technical" {
		t.Fatalf("interview %+v event %+v", iv, ev)
	}

	// nothing changed: no write
	if r, _ := Sync(context.Background(), s, cal, now); r != (Report{}) || cal.saves != 1 {
		t.Fatalf("idle sync %+v, %d saves", r, cal.saves)
	}

	// edited in ScoutJobs: Google follows
	later := at.Add(2 * time.Hour).Format("2006-01-02T15:04")
	_ = s.UpdateInterview(id, store.InterviewPatch{At: &later})
	if r, _ := Sync(context.Background(), s, cal, now); r.Updated != 1 || cal.events[iv.GoogleID].Start.Format("2006-01-02T15:04") != later {
		t.Fatalf("push edit %+v %+v", r, cal.events[iv.GoogleID])
	}

	// moved in Google: ScoutJobs follows
	e := cal.events[iv.GoogleID]
	e.Start, e.End = e.Start.Add(24*time.Hour), e.End.Add(24*time.Hour)
	cal.events[iv.GoogleID] = e
	if r, _ := Sync(context.Background(), s, cal, now); r.Moved != 1 || interview(t, s, id).At != e.Start.Format("2006-01-02T15:04") {
		t.Fatalf("pull edit %+v %+v", r, interview(t, s, id))
	}
	if r, _ := Sync(context.Background(), s, cal, now); r != (Report{}) {
		t.Fatalf("after move %+v", r)
	}

	// deleted in Google: kept here, never recreated
	delete(cal.events, iv.GoogleID)
	if r, _ := Sync(context.Background(), s, cal, now); r.Removed != 1 || interview(t, s, id).GoogleMode != "gone" {
		t.Fatalf("delete %+v", r)
	}
	if r, _ := Sync(context.Background(), s, cal, now); r != (Report{}) || len(cal.events) != 0 {
		t.Fatalf("recreated: %+v %v", r, cal.events)
	}
}

func TestReadOnlyGrantNeverWrites(t *testing.T) {
	s := open(t)
	jid := job(t, s, "Acme")
	_, _ = s.AddInterview(store.Interview{JobID: jid, Stage: "screen", At: time.Now().Add(time.Hour).Format("2006-01-02T15:04")})
	cal := &fakeCal{events: map[string]gmail.Event{}}
	if r, err := Sync(context.Background(), s, cal, time.Now()); err != nil || r != (Report{}) || cal.saves != 0 {
		t.Fatalf("%+v %v %d", r, err, cal.saves)
	}
}

func TestMatchesInterviewsInTheUsersCalendar(t *testing.T) {
	s := open(t)
	acme := job(t, s, "Acme")
	beta := job(t, s, "Betacorp")
	now := time.Now()
	start := now.Add(72 * time.Hour).Truncate(time.Hour)
	// already tracked at the same time: adopted, not doubled
	_, _ = s.AddInterview(store.Interview{JobID: beta, Stage: "screen", At: start.Format("2006-01-02T15:04")})
	cal := &fakeCal{events: map[string]gmail.Event{
		"g1": {ID: "g1", Title: "Acme technical interview", Start: start, End: start.Add(time.Hour), Location: "https://zoom.us/j/1"},
		"g2": {ID: "g2", Title: "Lunch with Sam", Start: start, End: start.Add(time.Hour), Attendees: []string{"sam@acme.com"}},
		"g3": {ID: "g3", Title: "Intro call", Start: start, End: start.Add(30 * time.Minute), Attendees: []string{"hr@betacorp.io"}},
		"g4": {ID: "g4", Title: "Dentist", Start: start, End: start.Add(time.Hour)},
	}}
	r, err := Sync(context.Background(), s, cal, now)
	if err != nil || r.Matched != 1 {
		t.Fatalf("%+v %v", r, err)
	}
	ivs, _ := s.Interviews(acme, false)
	if len(ivs) != 1 || ivs[0].Stage != "technical" || ivs[0].Format != "video" || ivs[0].GoogleMode != "pull" || ivs[0].GoogleID != "g1" {
		t.Fatalf("acme %+v", ivs)
	}
	bs, _ := s.Interviews(beta, false)
	if len(bs) != 1 || bs[0].GoogleID != "g3" {
		t.Fatalf("beta %+v", bs)
	}
	// a matched event moves: the interview follows; a second sync matches nothing new
	e := cal.events["g1"]
	e.Start, e.End = e.Start.Add(time.Hour), e.End.Add(time.Hour)
	cal.events["g1"] = e
	if r, _ := Sync(context.Background(), s, cal, now); r.Moved != 1 || r.Matched != 0 {
		t.Fatalf("second %+v", r)
	}
}

func TestStageAndInterviewish(t *testing.T) {
	for title, want := range map[string]string{"Acme: System Design round": "system_design", "Phone screen": "screen", "Onsite loop": "onsite", "Chat": "other"} {
		if got := Stage(title); got != want {
			t.Errorf("%q: %s, want %s", title, got, want)
		}
	}
	if Interviewish("Dinner") || !Interviewish("Recruiter call") {
		t.Error("interviewish")
	}
}
