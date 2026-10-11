// Package store owns the SQLite database that every skill and the dashboard read.
// One file, one source of truth: ~/.scoutjobs/scoutjobs.db (override with SCOUTJOBS_HOME).
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"modernc.org/sqlite"
)

// Status vocabulary for jobs. Order matters for the pipeline board.
var Statuses = []string{
	"new", "ranked", "skipped", "drafted", "applied", "interview", "offer",
	"hired", "rejected", "no_response", "withdrawn", "offer_declined",
}

// FinalStatuses close an application; nothing is due on them.
// NextStatuses lists the moves the pipeline offers as one-click buttons, forward steps first, the way out last.
var NextStatuses = map[string][]string{
	"new":            {"ranked", "drafted", "skipped"},
	"ranked":         {"drafted", "applied", "skipped"},
	"skipped":        {"ranked"},
	"drafted":        {"applied", "skipped"},
	"applied":        {"interview", "rejected", "no_response", "withdrawn"},
	"interview":      {"offer", "rejected", "withdrawn"},
	"offer":          {"hired", "offer_declined"},
	"rejected":       {"applied"},
	"no_response":    {"applied", "withdrawn"},
	"withdrawn":      {"applied"},
	"offer_declined": {"offer"},
}

// Leaving lists the statuses a one-click button paints as "out of the race".
var Leaving = map[string]bool{"skipped": true, "rejected": true, "no_response": true, "withdrawn": true, "offer_declined": true}

var FinalStatuses = map[string]bool{
	"hired": true, "rejected": true, "no_response": true, "withdrawn": true, "offer_declined": true, "skipped": true,
}

func ValidStatus(s string) bool {
	for _, v := range Statuses {
		if v == s {
			return true
		}
	}
	return false
}

type Store struct {
	DB   *sql.DB
	Home string
	// Notify, when set, receives every event the store records (used for outgoing webhooks).
	Notify func(Event)
	// Currency, when set, converts every new job's salary to a yearly figure in it.
	Currency string
}

// Home returns the data directory, creating nothing.
func Home() string {
	if h := os.Getenv("SCOUTJOBS_HOME"); h != "" {
		return h
	}
	dir, err := os.UserHomeDir()
	if err != nil {
		return ".scoutjobs"
	}
	return filepath.Join(dir, ".scoutjobs")
}

func Open() (*Store, error) {
	home := Home()
	path := filepath.Join(home, "scoutjobs.db")
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, openError(home, path, err)
	}
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, openError(home, path, err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{DB: db, Home: home}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, openError(home, path, err)
	}
	return s, nil
}

// openError names the database and the data dir it came from, with a hint for the SQLite codes
// people actually hit: a path that cannot be opened (14), a read-only file (8), a lock (5, 6).
func openError(home, path string, err error) error {
	src := "default"
	if os.Getenv("SCOUTJOBS_HOME") != "" {
		src = "SCOUTJOBS_HOME"
	}
	msg := fmt.Sprintf("open database %s (data dir %s, from %s): %v", path, home, src, err)
	if h := openHint(home, path, err); h != "" {
		msg += "\nhint: " + h
	}
	return errors.New(msg)
}

func openHint(home, path string, err error) string {
	var se *sqlite.Error
	code := 0
	if errors.As(err, &se) {
		code = se.Code() & 0xff
	}
	switch {
	case code == 5 || code == 6:
		return "another process holds a lock on the database; stop other scoutjobs commands or dashboards using " + home + " and retry"
	case code == 8:
		return path + " is read-only; check its owner and permissions (ls -l " + home + ")"
	case code != 14 && !errors.Is(err, fs.ErrPermission):
		return ""
	}
	if h := protectedHint(home); h != "" {
		return h
	}
	if st, e := os.Stat(home); e != nil || !st.IsDir() {
		return home + " is not a directory; set SCOUTJOBS_HOME to a folder or run scoutjobs init"
	}
	if f, e := os.CreateTemp(home, ".probe-*"); e != nil {
		return "no write permission on " + home + "; SQLite needs it for the journal. Check its owner (ls -ld " + home + ")"
	} else {
		f.Close()
		_ = os.Remove(f.Name())
	}
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if f, e := os.OpenFile(p, os.O_RDWR, 0); e != nil {
			if errors.Is(e, fs.ErrPermission) {
				return "no write permission on " + p + "; check its owner (ls -l " + home + "), e.g. files created by sudo"
			}
		} else {
			f.Close()
		}
	}
	return "SQLite cannot open " + path + "; check the path exists, is writable, and is not on a network or synced drive"
}

// protectedHint covers macOS privacy protection: Documents, Desktop, Downloads and cloud
// folders open fine from a terminal that has access, but not from a dashboard started by an
// app, launch agent or agent sandbox that lacks it.
func protectedHint(home string) string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	user, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	abs, err := filepath.Abs(home)
	if err != nil {
		return ""
	}
	for _, dir := range []string{"Documents", "Desktop", "Downloads", "Library/Mobile Documents", "Library/CloudStorage"} {
		if within(abs, filepath.Join(user, dir)) {
			return home + " is in ~/" + dir + ", which macOS only lets apps with Files and Folders or Full Disk Access open. " +
				"If scoutjobs works in your terminal but not here, start serve from that terminal, grant the launching app access in System Settings > Privacy & Security, or move the data dir (e.g. ~/.scoutjobs)"
		}
	}
	return ""
}

func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (s *Store) Close() error { return s.DB.Close() }

const schema = `
CREATE TABLE IF NOT EXISTS jobs (
  id INTEGER PRIMARY KEY,
  key TEXT UNIQUE NOT NULL,
  company TEXT NOT NULL,
  title TEXT NOT NULL,
  url TEXT NOT NULL DEFAULT '',
  location TEXT NOT NULL DEFAULT '',
  remote TEXT NOT NULL DEFAULT 'unknown',
  salary_min INTEGER NOT NULL DEFAULT 0,
  salary_max INTEGER NOT NULL DEFAULT 0,
  currency TEXT NOT NULL DEFAULT '',
  source TEXT NOT NULL DEFAULT 'manual',
  posted_at TEXT NOT NULL DEFAULT '',
  deadline TEXT NOT NULL DEFAULT '',
  fit_score INTEGER NOT NULL DEFAULT -1,
  fit_notes TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'new',
  tags TEXT NOT NULL DEFAULT '',
  notes TEXT NOT NULL DEFAULT '',
  contact TEXT NOT NULL DEFAULT '',
  veto TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS events (
  id INTEGER PRIMARY KEY,
  job_id INTEGER REFERENCES jobs(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  note TEXT NOT NULL DEFAULT '',
  at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS events_job ON events(job_id, at);
CREATE TABLE IF NOT EXISTS mail (
  message_id TEXT PRIMARY KEY,
  job_id INTEGER REFERENCES jobs(id) ON DELETE CASCADE,
  dir TEXT NOT NULL,
  uid INTEGER NOT NULL DEFAULT 0,
  at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS decisions (
  id INTEGER PRIMARY KEY,
  job_id INTEGER REFERENCES jobs(id) ON DELETE SET NULL,
  kind TEXT NOT NULL,
  reason TEXT NOT NULL DEFAULT '',
  signals TEXT NOT NULL DEFAULT '{}',
  at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS preferences (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  confidence REAL NOT NULL DEFAULT 0.5,
  evidence TEXT NOT NULL DEFAULT '',
  source TEXT NOT NULL DEFAULT 'learned',
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS pref_proposals (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  confidence REAL NOT NULL DEFAULT 0.5,
  evidence TEXT NOT NULL DEFAULT '',
  support INTEGER NOT NULL DEFAULT 0,
  against INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'pending',
  note TEXT NOT NULL DEFAULT '',
  proposed_at TEXT NOT NULL,
  decided_at TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS interviews (
  id INTEGER PRIMARY KEY,
  job_id INTEGER NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
  stage TEXT NOT NULL,
  at TEXT NOT NULL DEFAULT '',
  format TEXT NOT NULL DEFAULT '',
  interviewer TEXT NOT NULL DEFAULT '',
  prep_status TEXT NOT NULL DEFAULT 'todo',
  outcome TEXT NOT NULL DEFAULT '',
  feedback TEXT NOT NULL DEFAULT '',
  duration INTEGER NOT NULL DEFAULT 60,
  location TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
  id INTEGER PRIMARY KEY,
  kind TEXT NOT NULL,
  topic TEXT NOT NULL DEFAULT '',
  score INTEGER NOT NULL DEFAULT -1,
  notes TEXT NOT NULL DEFAULT '',
  at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS cv_checks (
  id INTEGER PRIMARY KEY,
  file TEXT NOT NULL,
  score INTEGER NOT NULL DEFAULT -1,
  ats_score INTEGER NOT NULL DEFAULT -1,
  summary TEXT NOT NULL DEFAULT '',
  gaps TEXT NOT NULL DEFAULT '',
  job_id INTEGER REFERENCES jobs(id) ON DELETE SET NULL,
  at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS learning (
  id INTEGER PRIMARY KEY,
  skill TEXT UNIQUE NOT NULL,
  priority INTEGER NOT NULL DEFAULT 2,
  resource TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'planned',
  hours REAL NOT NULL DEFAULT 0,
  demand INTEGER NOT NULL DEFAULT 0,
  notes TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS contacts (
  id INTEGER PRIMARY KEY,
  source TEXT NOT NULL DEFAULT 'linkedin',
  name TEXT NOT NULL,
  url TEXT NOT NULL DEFAULT '',
  email TEXT NOT NULL DEFAULT '',
  company TEXT NOT NULL DEFAULT '',
  company_key TEXT NOT NULL DEFAULT '',
  position TEXT NOT NULL DEFAULT '',
  connected_on TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS contacts_company ON contacts(company_key);
CREATE TABLE IF NOT EXISTS saved_searches (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  queries TEXT NOT NULL DEFAULT '',
  boards TEXT NOT NULL DEFAULT '',
  remote INTEGER NOT NULL DEFAULT 0,
  filters TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL DEFAULT ''
);
`

// Setting returns a dashboard switch stored in the database; "" when never set.
func (s *Store) Setting(key string) string {
	var v string
	_ = s.DB.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	return v
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.DB.Exec(`INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

func (s *Store) migrate() error {
	if _, err := s.DB.Exec(schema); err != nil {
		return err
	}
	// columns added after 0.1; sqlite has no ADD COLUMN IF NOT EXISTS
	for _, stmt := range []string{`ALTER TABLE jobs ADD COLUMN contact TEXT NOT NULL DEFAULT ''`, `ALTER TABLE jobs ADD COLUMN veto TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE interviews ADD COLUMN duration INTEGER NOT NULL DEFAULT 60`, `ALTER TABLE interviews ADD COLUMN location TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE interviews ADD COLUMN prep TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE mail ADD COLUMN from_addr TEXT NOT NULL DEFAULT ''`, `ALTER TABLE mail ADD COLUMN subject TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE mail ADD COLUMN snippet TEXT NOT NULL DEFAULT ''`, `ALTER TABLE mail ADD COLUMN date TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE mail ADD COLUMN thread TEXT NOT NULL DEFAULT ''`, `ALTER TABLE mail ADD COLUMN source TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE mail ADD COLUMN kind TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE interviews ADD COLUMN google_id TEXT NOT NULL DEFAULT ''`, `ALTER TABLE interviews ADD COLUMN google_mode TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE interviews ADD COLUMN google_sig TEXT NOT NULL DEFAULT ''`} {
		if _, err := s.DB.Exec(stmt); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
	}
	// user_version 1: title normalisation in JobKey changed, recompute keys once
	var v int
	_ = s.DB.QueryRow(`PRAGMA user_version`).Scan(&v)
	if v < 1 {
		if err := s.rekeyJobs(); err != nil {
			return err
		}
		if _, err := s.DB.Exec(`PRAGMA user_version = 1`); err != nil {
			return err
		}
	}
	return nil
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// Slug normalizes free text into a stable key fragment.
func Slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = nonAlnum.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

type Job struct {
	ID        int64  `json:"id"`
	Key       string `json:"key"`
	Company   string `json:"company"`
	Title     string `json:"title"`
	URL       string `json:"url"`
	Location  string `json:"location"`
	Remote    string `json:"remote"`
	SalaryMin int64  `json:"salary_min"`
	SalaryMax int64  `json:"salary_max"`
	Currency  string `json:"currency"`
	Source    string `json:"source"`
	PostedAt  string `json:"posted_at"`
	Deadline  string `json:"deadline"`
	FitScore  int64  `json:"fit_score"`
	FitNotes  string `json:"fit_notes"`
	Status    string `json:"status"`
	Tags      string `json:"tags"`
	Notes     string `json:"notes"`
	Contact   string `json:"contact"` // where the application goes: recruiter or jobs@ address
	Veto      string `json:"veto"`    // hard eligibility failure, kept outside the fit score; empty when none
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

const jobCols = `id,key,company,title,url,location,remote,salary_min,salary_max,currency,source,posted_at,deadline,fit_score,fit_notes,status,tags,notes,contact,veto,created_at,updated_at`

func scanJob(r interface{ Scan(...any) error }) (Job, error) {
	var j Job
	err := r.Scan(&j.ID, &j.Key, &j.Company, &j.Title, &j.URL, &j.Location, &j.Remote, &j.SalaryMin, &j.SalaryMax, &j.Currency, &j.Source, &j.PostedAt, &j.Deadline, &j.FitScore, &j.FitNotes, &j.Status, &j.Tags, &j.Notes, &j.Contact, &j.Veto, &j.CreatedAt, &j.UpdatedAt)
	return j, err
}

// UpsertJob inserts a job or returns the existing one by key. created reports which.
func (s *Store) UpsertJob(j Job) (Job, bool, error) {
	j.Key = JobKey(j.Company, j.Title)
	if existing, err := s.JobByKey(j.Key); err == nil {
		return existing, false, nil
	}
	if existing, ok := s.JobByURL(j.URL); ok {
		return existing, false, nil
	}
	NormalizeSalary(&j, s.Currency)
	if j.Remote == "" {
		j.Remote = "unknown"
	}
	if j.Source == "" {
		j.Source = "manual"
	}
	if j.Status == "" {
		j.Status = "new"
	}
	if j.FitScore == 0 && j.FitNotes == "" {
		j.FitScore = -1
	}
	ts := now()
	res, err := s.DB.Exec(`INSERT INTO jobs (key,company,title,url,location,remote,salary_min,salary_max,currency,source,posted_at,deadline,fit_score,fit_notes,status,tags,notes,contact,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		j.Key, j.Company, j.Title, j.URL, j.Location, j.Remote, j.SalaryMin, j.SalaryMax, j.Currency, j.Source, j.PostedAt, j.Deadline, j.FitScore, j.FitNotes, j.Status, j.Tags, j.Notes, j.Contact, ts, ts)
	if err != nil {
		return Job{}, false, err
	}
	id, _ := res.LastInsertId()
	_ = s.AddEvent(id, "added", "source: "+j.Source)
	job, err := s.JobByID(id)
	return job, true, err
}

func (s *Store) JobByID(id int64) (Job, error) {
	return scanJob(s.DB.QueryRow(`SELECT `+jobCols+` FROM jobs WHERE id=?`, id))
}

func (s *Store) JobByKey(key string) (Job, error) {
	return scanJob(s.DB.QueryRow(`SELECT `+jobCols+` FROM jobs WHERE key=?`, key))
}

// ResolveJob accepts a numeric id, a key, or "company/title" text.
func (s *Store) ResolveJob(ref string) (Job, error) {
	ref = strings.TrimSpace(ref)
	var id int64
	if _, err := fmt.Sscanf(ref, "%d", &id); err == nil && fmt.Sprint(id) == ref {
		return s.JobByID(id)
	}
	if strings.Contains(ref, "|") {
		return s.JobByKey(ref)
	}
	if c, t, ok := strings.Cut(ref, "/"); ok {
		return s.JobByKey(JobKey(c, t))
	}
	rows, err := s.DB.Query(`SELECT `+jobCols+` FROM jobs WHERE lower(company) LIKE ? ORDER BY updated_at DESC`, "%"+strings.ToLower(ref)+"%")
	if err != nil {
		return Job{}, err
	}
	defer rows.Close()
	var found []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return Job{}, err
		}
		found = append(found, j)
	}
	switch len(found) {
	case 0:
		return Job{}, sql.ErrNoRows
	case 1:
		return found[0], nil
	}
	var names []string
	for _, j := range found {
		names = append(names, fmt.Sprintf("#%d %s / %s", j.ID, j.Company, j.Title))
	}
	return Job{}, errors.New("ambiguous, pick one: " + strings.Join(names, "; "))
}

type JobFilter struct {
	Status string
	Open   bool
	Limit  int
	Query  string
}

func (s *Store) ListJobs(f JobFilter) ([]Job, error) {
	q := `SELECT ` + jobCols + ` FROM jobs WHERE 1=1`
	var args []any
	if f.Status != "" {
		q += ` AND status=?`
		args = append(args, f.Status)
	}
	if f.Open {
		q += ` AND status NOT IN ('hired','rejected','no_response','withdrawn','offer_declined','skipped')`
	}
	if f.Query != "" {
		q += ` AND (lower(company) LIKE ? OR lower(title) LIKE ? OR lower(tags) LIKE ?)`
		like := "%" + strings.ToLower(f.Query) + "%"
		args = append(args, like, like, like)
	}
	q += ` ORDER BY CASE WHEN fit_score<0 THEN 1 ELSE 0 END, fit_score DESC, updated_at DESC`
	if f.Limit > 0 {
		q += fmt.Sprintf(` LIMIT %d`, f.Limit)
	}
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// JobPatch holds optional field updates; nil means unchanged.
type JobPatch struct {
	Status, URL, Location, Remote, Currency, Source, PostedAt, Deadline, FitNotes, Tags, Notes, Contact, Veto *string
	FitScore, SalaryMin, SalaryMax                                                                            *int64
}

func (s *Store) UpdateJob(id int64, p JobPatch) error {
	before, err := s.JobByID(id)
	if err != nil {
		return err
	}
	var sets []string
	var args []any
	set := func(col string, v any) { sets = append(sets, col+"=?"); args = append(args, v) }
	strs := map[string]*string{"status": p.Status, "url": p.URL, "location": p.Location, "remote": p.Remote, "currency": p.Currency, "source": p.Source, "posted_at": p.PostedAt, "deadline": p.Deadline, "fit_notes": p.FitNotes, "tags": p.Tags, "notes": p.Notes, "contact": p.Contact, "veto": p.Veto}
	for col, v := range strs {
		if v != nil {
			set(col, *v)
		}
	}
	ints := map[string]*int64{"fit_score": p.FitScore, "salary_min": p.SalaryMin, "salary_max": p.SalaryMax}
	for col, v := range ints {
		if v != nil {
			set(col, *v)
		}
	}
	if len(sets) == 0 {
		return nil
	}
	if p.Status != nil && !ValidStatus(*p.Status) {
		return fmt.Errorf("invalid status %q (valid: %s)", *p.Status, strings.Join(Statuses, ", "))
	}
	set("updated_at", now())
	args = append(args, id)
	if _, err := s.DB.Exec(`UPDATE jobs SET `+strings.Join(sets, ",")+` WHERE id=?`, args...); err != nil {
		return err
	}
	if p.Status != nil && *p.Status != before.Status {
		_ = s.AddEvent(id, "status", before.Status+" -> "+*p.Status)
	}
	if p.FitScore != nil && *p.FitScore != before.FitScore {
		_ = s.AddEvent(id, "scored", fmt.Sprintf("fit %d", *p.FitScore))
	}
	if p.Veto != nil && *p.Veto != before.Veto {
		if *p.Veto == "" {
			_ = s.AddEvent(id, "veto:cleared", before.Veto)
		} else {
			_ = s.AddEvent(id, "veto", *p.Veto)
		}
	}
	return nil
}

func (s *Store) DeleteJob(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM jobs WHERE id=?`, id)
	return err
}

type Event struct {
	ID    int64  `json:"id"`
	JobID int64  `json:"job_id"`
	Kind  string `json:"kind"`
	Note  string `json:"note"`
	At    string `json:"at"`
	// joined
	Company string `json:"company"`
	Title   string `json:"title"`
}

func (s *Store) AddEvent(jobID int64, kind, note string) error {
	var jid any = jobID
	if jobID == 0 {
		jid = nil
	}
	ts := now()
	_, err := s.DB.Exec(`INSERT INTO events (job_id,kind,note,at) VALUES (?,?,?,?)`, jid, kind, note, ts)
	if err == nil && jobID != 0 {
		_, _ = s.DB.Exec(`UPDATE jobs SET updated_at=? WHERE id=?`, ts, jobID)
	}
	if err == nil && s.Notify != nil {
		e := Event{JobID: jobID, Kind: kind, Note: note, At: ts}
		if jobID != 0 {
			if j, jerr := s.JobByID(jobID); jerr == nil {
				e.Company, e.Title = j.Company, j.Title
			}
		}
		s.Notify(e)
	}
	return err
}

func (s *Store) Events(jobID int64, limit int) ([]Event, error) {
	q := `SELECT e.id, COALESCE(e.job_id,0), e.kind, e.note, e.at, COALESCE(j.company,''), COALESCE(j.title,'') FROM events e LEFT JOIN jobs j ON j.id=e.job_id`
	var args []any
	if jobID > 0 {
		q += ` WHERE e.job_id=?`
		args = append(args, jobID)
	}
	q += ` ORDER BY e.at DESC, e.id DESC`
	if limit > 0 {
		q += fmt.Sprintf(` LIMIT %d`, limit)
	}
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.JobID, &e.Kind, &e.Note, &e.At, &e.Company, &e.Title); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

type Decision struct {
	ID      int64  `json:"id"`
	JobID   int64  `json:"job_id"`
	Kind    string `json:"kind"`
	Reason  string `json:"reason"`
	Signals string `json:"signals"`
	At      string `json:"at"`
	Company string `json:"company"`
	Title   string `json:"title"`
}

func (s *Store) AddDecision(jobID int64, kind, reason, signals string) error {
	if signals == "" {
		signals = "{}"
	}
	var jid any = jobID
	if jobID == 0 {
		jid = nil
	}
	_, err := s.DB.Exec(`INSERT INTO decisions (job_id,kind,reason,signals,at) VALUES (?,?,?,?,?)`, jid, kind, reason, signals, now())
	if err == nil && jobID != 0 {
		_ = s.AddEvent(jobID, "decision:"+kind, reason)
	}
	return err
}

func (s *Store) Decisions(limit int) ([]Decision, error) {
	q := `SELECT d.id, COALESCE(d.job_id,0), d.kind, d.reason, d.signals, d.at, COALESCE(j.company,''), COALESCE(j.title,'') FROM decisions d LEFT JOIN jobs j ON j.id=d.job_id ORDER BY d.at DESC, d.id DESC`
	if limit > 0 {
		q += fmt.Sprintf(` LIMIT %d`, limit)
	}
	rows, err := s.DB.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Decision
	for rows.Next() {
		var d Decision
		if err := rows.Scan(&d.ID, &d.JobID, &d.Kind, &d.Reason, &d.Signals, &d.At, &d.Company, &d.Title); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

type Preference struct {
	Key        string  `json:"key"`
	Value      string  `json:"value"`
	Confidence float64 `json:"confidence"`
	Evidence   string  `json:"evidence"`
	Source     string  `json:"source"`
	UpdatedAt  string  `json:"updated_at"`
}

// ErrStated refuses to let an inferred value replace one the user stated.
var ErrStated = errors.New("stated by the user; learned values never overwrite it (set it with --source stated, or pref rm it first)")

func (s *Store) SetPreference(p Preference) error {
	if p.Source == "" {
		p.Source = "learned"
	}
	if p.Source != "stated" && s.statedPreference(p.Key) {
		return fmt.Errorf("%s: %w", p.Key, ErrStated)
	}
	if p.Confidence <= 0 {
		p.Confidence = 0.5
	}
	_, err := s.DB.Exec(`INSERT INTO preferences (key,value,confidence,evidence,source,updated_at) VALUES (?,?,?,?,?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, confidence=excluded.confidence, evidence=excluded.evidence, source=excluded.source, updated_at=excluded.updated_at`,
		p.Key, p.Value, p.Confidence, p.Evidence, p.Source, now())
	return err
}

func (s *Store) DeletePreference(key string) error {
	_, err := s.DB.Exec(`DELETE FROM preferences WHERE key=?`, key)
	return err
}

func (s *Store) Preferences() ([]Preference, error) {
	rows, err := s.DB.Query(`SELECT key,value,confidence,evidence,source,updated_at FROM preferences ORDER BY CASE source WHEN 'stated' THEN 0 ELSE 1 END, confidence DESC, key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Preference
	for rows.Next() {
		var p Preference
		if err := rows.Scan(&p.Key, &p.Value, &p.Confidence, &p.Evidence, &p.Source, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

type Interview struct {
	ID          int64  `json:"id"`
	JobID       int64  `json:"job_id"`
	Stage       string `json:"stage"`
	At          string `json:"at"`
	Format      string `json:"format"`
	Interviewer string `json:"interviewer"`
	PrepStatus  string `json:"prep_status"`
	Outcome     string `json:"outcome"`
	Feedback    string `json:"feedback"`
	Duration    int64  `json:"duration"` // minutes
	Location    string `json:"location"` // meeting link or address
	Prep        string `json:"prep"`     // done checklist items, comma separated (see PrepItems)
	CreatedAt   string `json:"created_at"`
	Company     string `json:"company"`
	Title       string `json:"title"`
	// Google Calendar link: the event id; mode "push" (ScoutJobs created it), "pull" (found in the
	// user's calendar) or "gone" (deleted there, never recreated); sig is the state last synced.
	GoogleID   string `json:"google_id,omitempty"`
	GoogleMode string `json:"google_mode,omitempty"`
	GoogleSig  string `json:"-"`
}

func (s *Store) AddInterview(i Interview) (int64, error) {
	if i.PrepStatus == "" {
		i.PrepStatus = "todo"
	}
	if i.Duration <= 0 {
		i.Duration = 60
	}
	res, err := s.DB.Exec(`INSERT INTO interviews (job_id,stage,at,format,interviewer,prep_status,outcome,feedback,duration,location,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		i.JobID, i.Stage, i.At, i.Format, i.Interviewer, i.PrepStatus, i.Outcome, i.Feedback, i.Duration, i.Location, now())
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	_ = s.AddEvent(i.JobID, "interview", i.Stage+" "+i.At)
	return id, nil
}

type InterviewPatch struct {
	At, Format, Interviewer, PrepStatus, Outcome, Feedback, Stage, Location, Prep *string
	Duration                                                                      *int64
}

func (s *Store) UpdateInterview(id int64, p InterviewPatch) error {
	var sets []string
	var args []any
	if p.Prep != nil {
		done := checkedPrep(*p.Prep)
		p.Prep = &done
		if p.PrepStatus == nil {
			st := PrepStatusOf(done)
			p.PrepStatus = &st
		}
	}
	for col, v := range map[string]*string{"at": p.At, "format": p.Format, "interviewer": p.Interviewer, "prep_status": p.PrepStatus, "outcome": p.Outcome, "feedback": p.Feedback, "stage": p.Stage, "location": p.Location, "prep": p.Prep} {
		if v != nil {
			sets = append(sets, col+"=?")
			args = append(args, *v)
		}
	}
	if p.Duration != nil && *p.Duration > 0 {
		sets = append(sets, "duration=?")
		args = append(args, *p.Duration)
	}
	if len(sets) == 0 {
		return nil
	}
	args = append(args, id)
	_, err := s.DB.Exec(`UPDATE interviews SET `+strings.Join(sets, ",")+` WHERE id=?`, args...)
	if err == nil && p.Outcome != nil {
		var jobID int64
		_ = s.DB.QueryRow(`SELECT job_id FROM interviews WHERE id=?`, id).Scan(&jobID)
		_ = s.AddEvent(jobID, "interview_outcome", *p.Outcome)
	}
	return err
}

// LinkGoogle records the Google Calendar event an interview is synced with.
func (s *Store) LinkGoogle(id int64, eventID, mode, sig string) error {
	_, err := s.DB.Exec(`UPDATE interviews SET google_id=?,google_mode=?,google_sig=? WHERE id=?`, eventID, mode, sig, id)
	return err
}

func (s *Store) Interviews(jobID int64, upcomingOnly bool) ([]Interview, error) {
	q := `SELECT i.id,i.job_id,i.stage,i.at,i.format,i.interviewer,i.prep_status,i.outcome,i.feedback,i.duration,i.location,i.prep,i.created_at,j.company,j.title,i.google_id,i.google_mode,i.google_sig FROM interviews i JOIN jobs j ON j.id=i.job_id WHERE 1=1`
	var args []any
	if jobID > 0 {
		q += ` AND i.job_id=?`
		args = append(args, jobID)
	}
	if upcomingOnly {
		q += ` AND i.outcome='' AND (i.at='' OR i.at >= ?)`
		args = append(args, time.Now().UTC().Add(-24*time.Hour).Format("2006-01-02"))
	}
	q += ` ORDER BY CASE WHEN i.at='' THEN 1 ELSE 0 END, i.at ASC`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Interview
	for rows.Next() {
		var i Interview
		if err := rows.Scan(&i.ID, &i.JobID, &i.Stage, &i.At, &i.Format, &i.Interviewer, &i.PrepStatus, &i.Outcome, &i.Feedback, &i.Duration, &i.Location, &i.Prep, &i.CreatedAt, &i.Company, &i.Title, &i.GoogleID, &i.GoogleMode, &i.GoogleSig); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

type Session struct {
	ID    int64  `json:"id"`
	Kind  string `json:"kind"`
	Topic string `json:"topic"`
	Score int64  `json:"score"`
	Notes string `json:"notes"`
	At    string `json:"at"`
}

func (s *Store) AddSession(k Session) error {
	if k.Score == 0 && k.Notes == "" {
		k.Score = -1
	}
	_, err := s.DB.Exec(`INSERT INTO sessions (kind,topic,score,notes,at) VALUES (?,?,?,?,?)`, k.Kind, k.Topic, k.Score, k.Notes, now())
	if err == nil {
		_ = s.AddEvent(0, "practice:"+k.Kind, k.Topic)
	}
	return err
}

func (s *Store) Sessions(limit int) ([]Session, error) {
	q := `SELECT id,kind,topic,score,notes,at FROM sessions ORDER BY at DESC, id DESC`
	if limit > 0 {
		q += fmt.Sprintf(` LIMIT %d`, limit)
	}
	rows, err := s.DB.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var k Session
		if err := rows.Scan(&k.ID, &k.Kind, &k.Topic, &k.Score, &k.Notes, &k.At); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

type Learning struct {
	ID        int64   `json:"id"`
	Skill     string  `json:"skill"`
	Priority  int64   `json:"priority"`
	Resource  string  `json:"resource"`
	Status    string  `json:"status"`
	Hours     float64 `json:"hours"`
	Demand    int64   `json:"demand"`
	Notes     string  `json:"notes"`
	UpdatedAt string  `json:"updated_at"`
}

func (s *Store) UpsertLearning(l Learning) error {
	if l.Status == "" {
		l.Status = "planned"
	}
	if l.Priority == 0 {
		l.Priority = 2
	}
	_, err := s.DB.Exec(`INSERT INTO learning (skill,priority,resource,status,hours,demand,notes,updated_at) VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(skill) DO UPDATE SET priority=excluded.priority, resource=CASE WHEN excluded.resource='' THEN learning.resource ELSE excluded.resource END,
		demand=MAX(learning.demand, excluded.demand), notes=CASE WHEN excluded.notes='' THEN learning.notes ELSE excluded.notes END, updated_at=excluded.updated_at`,
		strings.ToLower(strings.TrimSpace(l.Skill)), l.Priority, l.Resource, l.Status, l.Hours, l.Demand, l.Notes, now())
	return err
}

type LearningPatch struct {
	Status, Resource, Notes *string
	Hours                   *float64
	Priority, Demand        *int64
}

func (s *Store) UpdateLearning(id int64, p LearningPatch) error {
	var sets []string
	var args []any
	if p.Status != nil {
		sets = append(sets, "status=?")
		args = append(args, *p.Status)
	}
	if p.Resource != nil {
		sets = append(sets, "resource=?")
		args = append(args, *p.Resource)
	}
	if p.Notes != nil {
		sets = append(sets, "notes=?")
		args = append(args, *p.Notes)
	}
	if p.Hours != nil {
		sets = append(sets, "hours=hours+?")
		args = append(args, *p.Hours)
	}
	if p.Priority != nil {
		sets = append(sets, "priority=?")
		args = append(args, *p.Priority)
	}
	if p.Demand != nil {
		sets = append(sets, "demand=?")
		args = append(args, *p.Demand)
	}
	if len(sets) == 0 {
		return nil
	}
	sets = append(sets, "updated_at=?")
	args = append(args, now(), id)
	_, err := s.DB.Exec(`UPDATE learning SET `+strings.Join(sets, ",")+` WHERE id=?`, args...)
	if err == nil && p.Hours != nil {
		var skill string
		_ = s.DB.QueryRow(`SELECT skill FROM learning WHERE id=?`, id).Scan(&skill)
		_ = s.AddEvent(0, "learning", fmt.Sprintf("%s +%.1fh", skill, *p.Hours))
	}
	return err
}

func (s *Store) LearningItems() ([]Learning, error) {
	rows, err := s.DB.Query(`SELECT id,skill,priority,resource,status,hours,demand,notes,updated_at FROM learning ORDER BY CASE status WHEN 'in_progress' THEN 0 WHEN 'planned' THEN 1 ELSE 2 END, priority ASC, demand DESC, skill`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Learning
	for rows.Next() {
		var l Learning
		if err := rows.Scan(&l.ID, &l.Skill, &l.Priority, &l.Resource, &l.Status, &l.Hours, &l.Demand, &l.Notes, &l.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ---- cv checks ----

type CVCheck struct {
	ID       int64  `json:"id"`
	File     string `json:"file"`
	Score    int64  `json:"score"`
	ATSScore int64  `json:"ats_score"`
	Summary  string `json:"summary"`
	Gaps     string `json:"gaps"`
	JobID    int64  `json:"job_id"`
	At       string `json:"at"`
}

func (s *Store) AddCVCheck(c CVCheck) (int64, error) {
	var jid any = c.JobID
	if c.JobID == 0 {
		jid = nil
	}
	res, err := s.DB.Exec(`INSERT INTO cv_checks (file,score,ats_score,summary,gaps,job_id,at) VALUES (?,?,?,?,?,?,?)`,
		c.File, c.Score, c.ATSScore, c.Summary, c.Gaps, jid, now())
	if err != nil {
		return 0, err
	}
	_ = s.AddEvent(c.JobID, "cv_check", fmt.Sprintf("%s scored %d (ats %d)", c.File, c.Score, c.ATSScore))
	return res.LastInsertId()
}

func (s *Store) CVChecks(limit int) ([]CVCheck, error) {
	q := `SELECT id,file,score,ats_score,summary,gaps,COALESCE(job_id,0),at FROM cv_checks ORDER BY at DESC, id DESC`
	if limit > 0 {
		q += fmt.Sprintf(` LIMIT %d`, limit)
	}
	rows, err := s.DB.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CVCheck
	for rows.Next() {
		var c CVCheck
		if err := rows.Scan(&c.ID, &c.File, &c.Score, &c.ATSScore, &c.Summary, &c.Gaps, &c.JobID, &c.At); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// WeeklyCounts returns the number of events of kind matching notePattern per ISO week for the last n weeks, oldest first.
func (s *Store) WeeklyCounts(kind, notePattern string, n int) []int64 {
	out := make([]int64, n)
	start := time.Now().UTC()
	wd := int(start.Weekday())
	if wd == 0 {
		wd = 7
	}
	start = start.AddDate(0, 0, -(wd-1)-7*(n-1))
	rows, err := s.DB.Query(`SELECT at FROM events WHERE kind=? AND note LIKE ? AND at >= ?`, kind, notePattern, start.Format("2006-01-02"))
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var at string
		if rows.Scan(&at) != nil || len(at) < 10 {
			continue
		}
		t, err := time.Parse("2006-01-02", at[:10])
		if err != nil {
			continue
		}
		if i := int(t.Sub(start).Hours() / 24 / 7); i >= 0 && i < n {
			out[i]++
		}
	}
	return out
}
