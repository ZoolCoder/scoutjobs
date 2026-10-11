package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/gmail"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

// The calendar draws interviews, application deadlines and follow-ups in day, week, month and
// schedule views. Interview times are stored as local wall time ("2026-10-20T14:00"), so the
// calendar works in time.Local; exports convert to UTC.

const (
	slotMin   = 15 // week grid resolution in minutes
	maxLanes  = 4  // overlapping events side by side in one day column
	dayLayout = "2006-01-02"
)

type calEvent struct {
	Key         string // i<interview id> | d<job id> | f<job id>: selects the detail panel and the .ics download
	Kind        string // interview | deadline | followup
	JobID       int64
	Title, Sub  string
	Date        string // local YYYY-MM-DD
	Start, End  string // local HH:MM; empty for all-day
	Row, Span   int    // week grid placement in slots
	Lane        int
	Class       string // colour class
	Done        bool   // outcome recorded: drawn muted
	Cancelled   bool
	start, stop time.Time
	allDay      bool
	iv          *store.Interview
	google      *gmail.Event
}

type calDay struct {
	Date          string
	Dom           int
	Label         string
	Today, Other  bool // Other: outside the month on the month view
	AllDay, Timed []calEvent
	Shown         []calEvent // month view: first few events of the day
	More          int
	Lanes         int
	NowRow        int // > 0 when the current time falls in this day column
}

type calView struct {
	View, Title, D        string
	Prev, Next, Today     string
	Days                  []calDay
	Weeks                 [][]calDay
	Hours                 []string
	Slots                 []calSlot // one link per hour cell: click an empty hour to create
	Rows                  int       // hour count of the week grid, as a height class
	Agenda                []calDay
	Detail                *calDetail
	New                   *calNew
	Jobs                  []store.Job
	FeedURL, FeedNote     string
	FeedDownload          string
	Counts                map[string]int
	GoogleCal             string // on | reconnect | setup | "" when Gmail is not offered
	GoogleErr             string
	Stages, Formats, Mins []string
}

type calDetail struct {
	Event     calEvent
	When      string
	Google    string
	ICS       string
	Interview *store.Interview
	Job       store.Job
	Date      string
	Time      string
	Open      string // Google Calendar's own page for an event read from it
	Track     string // the new-interview form, filled from that event
}

type calNew struct {
	Date, Time string
	JobID      int64 // preselected job, from ?job=
}

type calSlot struct {
	Row  int
	Time string
}

// stageClass picks the colour of an interview by stage, the way calendars colour by calendar.
func stageClass(stage string) string {
	switch stage {
	case "screen", "recruiter":
		return "c-screen"
	case "technical", "system_design", "coding", "case":
		return "c-tech"
	case "behavioral":
		return "c-behav"
	case "onsite", "final":
		return "c-final"
	}
	return "c-other"
}

// parseAt reads an interview time: date only means all day.
func parseAt(s string) (t time.Time, timed, ok bool) {
	s = strings.TrimSpace(s)
	for _, l := range []string{"2006-01-02T15:04", "2006-01-02T15:04:05", "2006-01-02 15:04"} {
		if t, err := time.ParseInLocation(l, s, time.Local); err == nil {
			return t, true, true
		}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Local(), true, true
	}
	if t, err := time.ParseInLocation(dayLayout, day(s), time.Local); err == nil {
		return t, false, true
	}
	return time.Time{}, false, false
}

func midnight(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.Local)
}

func interviewEvent(i store.Interview) (calEvent, bool) {
	t, timed, ok := parseAt(i.At)
	if !ok {
		return calEvent{}, false
	}
	iv := i
	e := calEvent{Key: fmt.Sprintf("i%d", i.ID), Kind: "interview", JobID: i.JobID, Title: i.Company + " · " + strings.ReplaceAll(i.Stage, "_", " "), Sub: i.Title,
		Date: t.Format(dayLayout), Class: stageClass(i.Stage), Done: i.Outcome != "" && i.Outcome != "pending", Cancelled: i.Outcome == "cancelled", start: t, allDay: !timed, iv: &iv}
	if timed {
		d := i.Duration
		if d <= 0 {
			d = 60
		}
		e.stop = t.Add(time.Duration(d) * time.Minute)
		e.Start, e.End = t.Format("15:04"), e.stop.Format("15:04")
	} else {
		e.stop = t.AddDate(0, 0, 1)
	}
	return e, true
}

func deadlineEvent(j store.Job) (calEvent, bool) {
	t, err := time.ParseInLocation(dayLayout, day(j.Deadline), time.Local)
	if err != nil {
		return calEvent{}, false
	}
	return calEvent{Key: fmt.Sprintf("d%d", j.ID), Kind: "deadline", JobID: j.ID, Title: "Deadline · " + j.Company, Sub: j.Title, Date: t.Format(dayLayout),
		Class: "c-deadline", start: t, stop: t.AddDate(0, 0, 1), allDay: true}, true
}

// followUpEvent lands on the day the follow-up falls due; one already overdue shows today.
func followUpEvent(j store.Job, days int, today time.Time) (calEvent, bool) {
	u, err := time.Parse(time.RFC3339, j.UpdatedAt)
	if err != nil || days <= 0 {
		return calEvent{}, false
	}
	due := midnight(u.Local()).AddDate(0, 0, days)
	if due.Before(today) {
		due = today
	}
	return calEvent{Key: fmt.Sprintf("f%d", j.ID), Kind: "followup", JobID: j.ID, Title: "Follow up · " + j.Company, Sub: j.Title, Date: due.Format(dayLayout),
		Class: "c-follow", start: due, stop: due.AddDate(0, 0, 1), allDay: true}, true
}

// events collects every calendar entry; the views filter by date.
func (v *server) events(today time.Time) []calEvent {
	var out []calEvent
	ivs, _ := v.s.Interviews(0, false)
	for _, i := range ivs {
		if e, ok := interviewEvent(i); ok {
			out = append(out, e)
		}
	}
	open, _ := v.s.ListJobs(store.JobFilter{Open: true})
	for _, j := range open {
		if j.Deadline == "" {
			continue
		}
		if e, ok := deadlineEvent(j); ok {
			out = append(out, e)
		}
	}
	applied, _ := v.s.ListJobs(store.JobFilter{Status: "applied"})
	staleBefore := time.Now().AddDate(0, 0, -v.cfg.StaleDays).UTC().Format(time.RFC3339)
	for _, j := range applied {
		if v.cfg.StaleDays > 0 && j.UpdatedAt < staleBefore {
			continue // gone quiet past stale_days: the overview suggests no_response instead
		}
		if e, ok := followUpEvent(j, v.cfg.FollowUpDays, today); ok {
			out = append(out, e)
		}
	}
	return sortEvents(out)
}

func sortEvents(out []calEvent) []calEvent {
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].allDay != out[b].allDay {
			return out[a].allDay
		}
		return out[a].start.Before(out[b].start)
	})
	return out
}

func (v *server) eventByKey(key string, today time.Time) (calEvent, store.Job, bool) {
	if len(key) < 2 {
		return calEvent{}, store.Job{}, false
	}
	if key[0] == 'g' {
		e, ok := v.googleByKey(key[1:])
		j, _ := v.s.JobByID(e.JobID)
		return e, j, ok
	}
	id, err := strconv.ParseInt(key[1:], 10, 64)
	if err != nil {
		return calEvent{}, store.Job{}, false
	}
	switch key[0] {
	case 'i':
		ivs, _ := v.s.Interviews(0, false)
		for _, i := range ivs {
			if i.ID == id {
				e, ok := interviewEvent(i)
				j, _ := v.s.JobByID(i.JobID)
				return e, j, ok
			}
		}
	case 'd', 'f':
		j, err := v.s.JobByID(id)
		if err != nil {
			return calEvent{}, store.Job{}, false
		}
		var e calEvent
		var ok bool
		if key[0] == 'd' {
			e, ok = deadlineEvent(j)
		} else {
			e, ok = followUpEvent(j, v.cfg.FollowUpDays, today)
		}
		return e, j, ok
	}
	return calEvent{}, store.Job{}, false
}

func (v *server) calendar(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	now := time.Now()
	today := midnight(now)
	view := q.Get("view")
	if view != "day" && view != "week" && view != "month" && view != "schedule" {
		view = "week"
	}
	d := today
	if t, err := time.ParseInLocation(dayLayout, q.Get("d"), time.Local); err == nil {
		d = t
	}
	all := v.events(today)
	gev, gerr := v.googleEvents(r.Context(), d)
	all = sortEvents(append(all, gev...))
	cv := calView{View: view, D: d.Format(dayLayout), Today: today.Format(dayLayout), Counts: map[string]int{},
		Stages:  []string{"screen", "technical", "system_design", "behavioral", "case", "onsite", "final", "other"},
		Formats: []string{"video", "phone", "onsite"}, Mins: []string{"15", "30", "45", "60", "90", "120", "180", "240"}}
	for _, e := range all {
		if !e.start.Before(today) {
			cv.Counts[e.Kind]++
		}
	}
	link := func(t time.Time) string { return t.Format(dayLayout) }
	switch view {
	case "day":
		cv.Title = d.Format("Monday 2 January 2006")
		cv.Prev, cv.Next = link(d.AddDate(0, 0, -1)), link(d.AddDate(0, 0, 1))
		cv.Days = timeGrid(&cv, d, 1, all, now)
	case "week":
		start := d.AddDate(0, 0, -((int(d.Weekday()) + 6) % 7)) // weeks start on Monday
		end := start.AddDate(0, 0, 6)
		cv.Title = rangeTitle(start, end)
		cv.Prev, cv.Next = link(start.AddDate(0, 0, -7)), link(start.AddDate(0, 0, 7))
		cv.Days = timeGrid(&cv, start, 7, all, now)
	case "month":
		first := time.Date(d.Year(), d.Month(), 1, 0, 0, 0, 0, time.Local)
		cv.Title = first.Format("January 2006")
		cv.Prev, cv.Next = link(first.AddDate(0, -1, 0)), link(first.AddDate(0, 1, 0))
		cv.Weeks = monthGrid(first, all, today)
	case "schedule":
		cv.Title = "From " + d.Format("2 January 2006")
		cv.Prev, cv.Next = link(d.AddDate(0, 0, -28)), link(d.AddDate(0, 0, 28))
		for i := 0; i < 56; i++ {
			day := dayOf(d.AddDate(0, 0, i), all, today)
			if len(day.AllDay)+len(day.Timed) > 0 {
				cv.Agenda = append(cv.Agenda, day)
			}
		}
	}
	if key := q.Get("ev"); key != "" {
		if e, j, ok := v.eventByKey(key, today); ok {
			cv.Detail = v.detail(r, e, j)
		}
	}
	if n := q.Get("new"); n != "" {
		nd, nt, _ := strings.Cut(n, "T")
		if _, err := time.Parse(dayLayout, nd); err == nil {
			cv.New = &calNew{Date: nd, Time: nt}
			cv.New.JobID, _ = strconv.ParseInt(q.Get("job"), 10, 64)
			cv.Jobs, _ = v.s.ListJobs(store.JobFilter{Open: true})
		}
	}
	cv.GoogleErr = gerr
	if c := v.gmailClient(); c != nil {
		switch {
		case c.HasCalendar():
			cv.GoogleCal = "on"
		case c.Connected():
			cv.GoogleCal = "reconnect"
		default:
			cv.GoogleCal = "setup"
		}
	}
	cv.FeedURL, cv.FeedNote = v.feedURL(r)
	cv.FeedDownload = "/calendar.ics?download=1"
	if k := feedKey(v.cfg.APIToken); k != "" {
		cv.FeedDownload += "&key=" + k
	}
	v.render(w, r, "calendar.html", "calendar", cv)
}

func rangeTitle(a, b time.Time) string {
	switch {
	case a.Year() != b.Year():
		return a.Format("2 Jan 2006") + " – " + b.Format("2 Jan 2006")
	case a.Month() != b.Month():
		return a.Format("2 Jan") + " – " + b.Format("2 Jan 2006")
	}
	return a.Format("2") + " – " + b.Format("2 January 2006")
}

func dayOf(t time.Time, all []calEvent, today time.Time) calDay {
	ds := t.Format(dayLayout)
	cd := calDay{Date: ds, Dom: t.Day(), Label: t.Format("Mon"), Today: ds == today.Format(dayLayout)}
	for _, e := range all {
		if e.Date != ds {
			continue
		}
		if e.allDay {
			cd.AllDay = append(cd.AllDay, e)
		} else {
			cd.Timed = append(cd.Timed, e)
		}
	}
	return cd
}

// timeGrid lays out n days from start on a 15-minute grid. The visible hours grow to fit every event.
func timeGrid(cv *calView, start time.Time, n int, all []calEvent, now time.Time) []calDay {
	today := midnight(now)
	fromH, toH := 8, 19
	var days []calDay
	for i := 0; i < n; i++ {
		cd := dayOf(start.AddDate(0, 0, i), all, today)
		for _, e := range cd.Timed {
			if h := e.start.Hour(); h < fromH {
				fromH = h
			}
			end := e.stop
			if !sameDay(end, e.start) {
				end = midnight(e.start).Add(24 * time.Hour)
			}
			h := end.Hour()
			if end.Minute() > 0 || !sameDay(end, e.start) {
				h++
			}
			if !sameDay(end, e.start) {
				h = 24
			}
			if h > toH {
				toH = h
			}
		}
		days = append(days, cd)
	}
	if toH > 24 {
		toH = 24
	}
	slots := (toH - fromH) * 60 / slotMin
	for i := range days {
		cd := &days[i]
		var laneEnd []time.Time
		for k := range cd.Timed {
			e := &cd.Timed[k]
			gridStart := midnight(e.start).Add(time.Duration(fromH) * time.Hour)
			e.Row = int(e.start.Sub(gridStart).Minutes())/slotMin + 1
			e.Span = int((e.stop.Sub(e.start).Minutes() + slotMin - 1) / slotMin)
			if e.Span < 2 {
				e.Span = 2 // keep a 15-minute call readable
			}
			if e.Row+e.Span-1 > slots {
				e.Span = slots - e.Row + 1
			}
			e.Lane = 0
			for l, end := range laneEnd {
				if !e.start.Before(end) {
					e.Lane = l + 1
					laneEnd[l] = e.stop
					break
				}
			}
			if e.Lane == 0 {
				if len(laneEnd) < maxLanes {
					laneEnd = append(laneEnd, e.stop)
					e.Lane = len(laneEnd)
				} else {
					e.Lane = maxLanes
				}
			}
		}
		cd.Lanes = len(laneEnd)
		if cd.Lanes == 0 {
			cd.Lanes = 1
		}
		if cd.Today && now.Hour() >= fromH && now.Hour() < toH {
			cd.NowRow = (now.Hour()-fromH)*60/slotMin + now.Minute()/slotMin + 1
		}
	}
	for h := fromH; h < toH; h++ {
		cv.Hours = append(cv.Hours, fmt.Sprintf("%02d:00", h))
		cv.Slots = append(cv.Slots, calSlot{Row: (h-fromH)*60/slotMin + 1, Time: fmt.Sprintf("%02d:00", h)})
	}
	cv.Rows = toH - fromH
	return days
}

func sameDay(a, b time.Time) bool { return a.Format(dayLayout) == b.Format(dayLayout) }

// monthGrid returns Monday-first weeks covering the month, three events per cell and a "+N more".
func monthGrid(first time.Time, all []calEvent, today time.Time) [][]calDay {
	start := first.AddDate(0, 0, -((int(first.Weekday()) + 6) % 7))
	var weeks [][]calDay
	for t := start; t.Before(first.AddDate(0, 1, 0)); {
		var week []calDay
		for i := 0; i < 7; i++ {
			cd := dayOf(t, all, today)
			cd.Other = t.Month() != first.Month()
			cd.Shown = append(append([]calEvent{}, cd.AllDay...), cd.Timed...)
			if len(cd.Shown) > 3 {
				cd.More = len(cd.Shown) - 2
				cd.Shown = cd.Shown[:2]
			}
			week = append(week, cd)
			t = t.AddDate(0, 0, 1)
		}
		weeks = append(weeks, week)
	}
	return weeks
}

func (v *server) detail(r *http.Request, e calEvent, j store.Job) *calDetail {
	d := &calDetail{Event: e, Job: j, Interview: e.iv}
	if g := e.google; g != nil {
		d.Open = g.Link
		d.Track = "/calendar?view=week&d=" + e.Date + "&new=" + e.Date
		if !e.allDay {
			d.Track += "T" + e.Start
		}
		if e.JobID != 0 {
			d.Track += fmt.Sprintf("&job=%d", e.JobID)
		}
	} else {
		d.Google, d.ICS = googleURL(e, v.jobURL(r, e.JobID)), "/calendar/event/"+e.Key
	}
	if e.allDay {
		d.When = e.start.Format("Monday 2 January 2006") + " · all day"
	} else {
		d.When = e.start.Format("Monday 2 January 2006 · 15:04") + " – " + e.End
	}
	d.Date = e.Date
	d.Time = e.Start
	return d
}

func (v *server) jobURL(r *http.Request, id int64) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s/jobs/%d", scheme, r.Host, id)
}

// description is the text both Google links and .ics files carry. The marker line lets the
// calendar-sync skill find an event it created before instead of adding it twice.
func description(e calEvent, jobURL string) string {
	var b strings.Builder
	if e.Sub != "" {
		fmt.Fprintf(&b, "Role: %s\n", e.Sub)
	}
	if i := e.iv; i != nil {
		fmt.Fprintf(&b, "Stage: %s\n", strings.ReplaceAll(i.Stage, "_", " "))
		if i.Format != "" {
			fmt.Fprintf(&b, "Format: %s\n", i.Format)
		}
		if i.Interviewer != "" {
			fmt.Fprintf(&b, "With: %s\n", i.Interviewer)
		}
		fmt.Fprintf(&b, "Prep: %s\n", strings.ReplaceAll(i.PrepStatus, "_", " "))
	}
	fmt.Fprintf(&b, "ScoutJobs: %s\n", jobURL)
	fmt.Fprintf(&b, "scoutjobs:%s:%s", e.Kind, e.Key[1:])
	return b.String()
}

func location(e calEvent) string {
	if e.iv != nil {
		return e.iv.Location
	}
	return ""
}

// googleURL opens Google Calendar's "new event" form filled in; nothing is sent until the user saves it there.
func googleURL(e calEvent, jobURL string) string {
	var dates string
	if e.allDay {
		dates = e.start.Format("20060102") + "/" + e.stop.Format("20060102")
	} else {
		dates = e.start.UTC().Format("20060102T150405Z") + "/" + e.stop.UTC().Format("20060102T150405Z")
	}
	q := url.Values{"action": {"TEMPLATE"}, "text": {e.Title}, "dates": {dates}, "details": {description(e, jobURL)}}
	if loc := location(e); loc != "" {
		q.Set("location", loc)
	}
	return "https://calendar.google.com/calendar/render?" + q.Encode()
}

// ---- iCalendar ----

func icsEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\r\n", `\n`, "\n", `\n`).Replace(s)
}

// icsLine folds at 75 octets as RFC 5545 asks, without splitting a UTF-8 sequence.
func icsLine(b *strings.Builder, line string) {
	for len(line) > 75 {
		cut := 75
		for cut > 0 && line[cut]&0xC0 == 0x80 {
			cut--
		}
		b.WriteString(line[:cut] + "\r\n")
		line = " " + line[cut:]
	}
	b.WriteString(line + "\r\n")
}

func icsEvent(b *strings.Builder, e calEvent, jobURL string, stamp time.Time) {
	icsLine(b, "BEGIN:VEVENT")
	kind := map[string]string{"i": "interview", "d": "deadline", "f": "followup"}[e.Key[:1]]
	icsLine(b, fmt.Sprintf("UID:%s-%s@scoutjobs", kind, e.Key[1:]))
	icsLine(b, "DTSTAMP:"+stamp.UTC().Format("20060102T150405Z"))
	if e.allDay {
		icsLine(b, "DTSTART;VALUE=DATE:"+e.start.Format("20060102"))
		icsLine(b, "DTEND;VALUE=DATE:"+e.stop.Format("20060102"))
	} else {
		icsLine(b, "DTSTART:"+e.start.UTC().Format("20060102T150405Z"))
		icsLine(b, "DTEND:"+e.stop.UTC().Format("20060102T150405Z"))
	}
	icsLine(b, "SUMMARY:"+icsEscape(e.Title))
	icsLine(b, "DESCRIPTION:"+icsEscape(description(e, jobURL)))
	if loc := location(e); loc != "" {
		icsLine(b, "LOCATION:"+icsEscape(loc))
	}
	icsLine(b, "URL:"+jobURL)
	icsLine(b, "CATEGORIES:"+strings.ToUpper(e.Kind))
	if e.Cancelled {
		icsLine(b, "STATUS:CANCELLED")
	} else {
		icsLine(b, "STATUS:CONFIRMED")
	}
	if e.Kind == "interview" && !e.allDay {
		icsLine(b, "BEGIN:VALARM")
		icsLine(b, "ACTION:DISPLAY")
		icsLine(b, "DESCRIPTION:"+icsEscape(e.Title))
		icsLine(b, "TRIGGER:-PT30M")
		icsLine(b, "END:VALARM")
	}
	icsLine(b, "END:VEVENT")
}

func (v *server) writeICS(w http.ResponseWriter, r *http.Request, events []calEvent, filename string) {
	var b strings.Builder
	for _, l := range []string{"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//ScoutJobs//Calendar//EN", "CALSCALE:GREGORIAN", "METHOD:PUBLISH", "X-WR-CALNAME:ScoutJobs", "X-PUBLISHED-TTL:PT1H"} {
		icsLine(&b, l)
	}
	stamp := time.Now()
	for _, e := range events {
		icsEvent(&b, e, v.jobURL(r, e.JobID), stamp)
	}
	icsLine(&b, "END:VCALENDAR")
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	if filename != "" {
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	}
	_, _ = w.Write([]byte(b.String()))
}

// feedKey is a read-only capability for the calendar feed, derived from api_token so the token itself
// never appears in a calendar app's settings.
func feedKey(token string) string {
	if token == "" {
		return ""
	}
	m := hmac.New(sha256.New, []byte(token))
	m.Write([]byte("scoutjobs calendar feed"))
	return hex.EncodeToString(m.Sum(nil))[:32]
}

func (v *server) feedURL(r *http.Request) (string, string) {
	u := "http://" + r.Host + "/calendar.ics"
	if k := feedKey(v.cfg.APIToken); k != "" {
		return u + "?key=" + k, "Keyed by api_token: changing the token changes this address."
	}
	return u, "Open to anything that can reach this address; set api_token in config.yaml to require a key."
}

// calendarFeed is the subscribable feed: every interview, open deadline and follow-up.
func (v *server) calendarFeed(w http.ResponseWriter, r *http.Request) {
	if k := feedKey(v.cfg.APIToken); k != "" && subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("key")), []byte(k)) != 1 {
		http.Error(w, "calendar feed needs ?key= (shown on the Calendar page)", http.StatusUnauthorized)
		return
	}
	name := ""
	if r.URL.Query().Get("download") == "1" {
		name = "scoutjobs.ics"
	}
	v.writeICS(w, r, v.events(midnight(time.Now())), name)
}

func (v *server) calendarEvent(w http.ResponseWriter, r *http.Request) {
	e, j, ok := v.eventByKey(r.PathValue("key"), midnight(time.Now()))
	if !ok {
		http.NotFound(w, r)
		return
	}
	v.writeICS(w, r, []calEvent{e}, fmt.Sprintf("%s-%s.ics", store.Slug(j.Company), e.Kind))
}

// createInterview is the calendar's "Create" form. Scheduling moves an earlier-stage job to interview, as the CLI does.
func (v *server) createInterview(w http.ResponseWriter, r *http.Request) {
	jobID, err := strconv.ParseInt(r.FormValue("job"), 10, 64)
	if err != nil {
		fail(w, fmt.Errorf("pick a job"))
		return
	}
	j, err := v.s.JobByID(jobID)
	if err != nil {
		fail(w, err)
		return
	}
	at, err := formAt(r)
	if err != nil {
		fail(w, err)
		return
	}
	stage := r.FormValue("stage")
	if stage == "" {
		stage = "other"
	}
	dur, _ := strconv.ParseInt(r.FormValue("duration"), 10, 64)
	id, err := v.s.AddInterview(store.Interview{JobID: j.ID, Stage: stage, At: at, Format: r.FormValue("format"), Interviewer: strings.TrimSpace(r.FormValue("interviewer")),
		Location: strings.TrimSpace(r.FormValue("location")), Duration: dur})
	if err != nil {
		fail(w, err)
		return
	}
	switch j.Status {
	case "new", "ranked", "drafted", "applied":
		st := "interview"
		_ = v.s.UpdateJob(j.ID, store.JobPatch{Status: &st})
	}
	http.Redirect(w, r, fmt.Sprintf("/calendar?view=week&d=%s&ev=i%d", day(at), id), http.StatusSeeOther)
}

// formAt joins the date and optional time inputs into the stored form.
func formAt(r *http.Request) (string, error) {
	d := r.FormValue("date")
	if _, err := time.Parse(dayLayout, d); err != nil {
		return "", fmt.Errorf("date must be YYYY-MM-DD")
	}
	t := strings.TrimSpace(r.FormValue("time"))
	if t == "" {
		return d, nil
	}
	if _, err := time.Parse("15:04", t); err != nil {
		return "", fmt.Errorf("time must be HH:MM")
	}
	return d + "T" + t, nil
}
