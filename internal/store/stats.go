package store

import (
	"time"
)

type Stats struct {
	Total          int64            `json:"total"`
	Open           int64            `json:"open"`
	ByStatus       map[string]int64 `json:"by_status"`
	AppliedWeek    int64            `json:"applied_this_week"`
	AppliedTotal   int64            `json:"applied_total"`
	InterviewsWeek int64            `json:"interviews_this_week"`
	Offers         int64            `json:"offers"`
	ResponseRate   float64          `json:"response_rate"`
	AvgFit         float64          `json:"avg_fit_applied"`
	Streak         int64            `json:"activity_streak_days"`
	PracticeWeek   int64            `json:"practice_sessions_this_week"`
	LearningHours  float64          `json:"learning_hours"`
	DueFollowUps   []Job            `json:"due_follow_ups"`
	Stale          []Job            `json:"stale"`
}

func weekStart() string {
	t := time.Now().UTC()
	wd := int(t.Weekday())
	if wd == 0 {
		wd = 7
	}
	return t.AddDate(0, 0, -(wd - 1)).Format("2006-01-02")
}

// Stats computes the numbers the dashboard and daily briefing show.
// followUpDays and staleDays bound the "quiet application" lists.
func (s *Store) Stats(followUpDays, staleDays int) (Stats, error) {
	st := Stats{ByStatus: map[string]int64{}}
	rows, err := s.DB.Query(`SELECT status, COUNT(*) FROM jobs GROUP BY status`)
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var k string
		var n int64
		if err := rows.Scan(&k, &n); err != nil {
			rows.Close()
			return st, err
		}
		st.ByStatus[k] = n
		st.Total += n
		if !FinalStatuses[k] {
			st.Open += n
		}
	}
	rows.Close()

	ws := weekStart()
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='status' AND note LIKE '%-> applied' AND at >= ?`, ws).Scan(&st.AppliedWeek)
	_ = s.DB.QueryRow(`SELECT COUNT(DISTINCT job_id) FROM events WHERE kind='status' AND note LIKE '%-> applied'`).Scan(&st.AppliedTotal)
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM interviews WHERE at >= ? AND at < ?`, ws, time.Now().UTC().AddDate(0, 0, 7).Format("2006-01-02")).Scan(&st.InterviewsWeek)
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM sessions WHERE at >= ?`, ws).Scan(&st.PracticeWeek)
	_ = s.DB.QueryRow(`SELECT COALESCE(SUM(hours),0) FROM learning`).Scan(&st.LearningHours)
	_ = s.DB.QueryRow(`SELECT COALESCE(AVG(fit_score),0) FROM jobs WHERE fit_score>=0 AND status IN ('applied','interview','offer','hired','rejected','no_response')`).Scan(&st.AvgFit)
	st.Offers = st.ByStatus["offer"] + st.ByStatus["hired"] + st.ByStatus["offer_declined"]

	// Response rate counts jobs that moved past "applied" against every job that was ever applied to,
	// by current status, so a job set straight to interview still counts as applied.
	var everApplied, responded int64
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE status IN ('applied','interview','offer','hired','rejected','no_response','withdrawn','offer_declined')`).Scan(&everApplied)
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE status IN ('interview','offer','hired','rejected','offer_declined')`).Scan(&responded)
	if everApplied > st.AppliedTotal {
		st.AppliedTotal = everApplied
	}
	if st.AppliedTotal > 0 {
		st.ResponseRate = float64(responded) / float64(st.AppliedTotal)
	}

	st.Streak = s.streak()

	fu := time.Now().UTC().AddDate(0, 0, -followUpDays).Format(time.RFC3339)
	stale := time.Now().UTC().AddDate(0, 0, -staleDays).Format(time.RFC3339)
	st.DueFollowUps, _ = s.quiet("applied", stale, fu)
	st.Stale, _ = s.quiet("applied", "", stale)
	return st, nil
}

// quiet lists jobs in status whose last activity is between lo and hi (RFC3339, "" = unbounded).
func (s *Store) quiet(status, lo, hi string) ([]Job, error) {
	q := `SELECT ` + jobCols + ` FROM jobs WHERE status=? AND updated_at <= ?`
	args := []any{status, hi}
	if lo != "" {
		q += ` AND updated_at > ?`
		args = append(args, lo)
	}
	q += ` ORDER BY updated_at ASC`
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

// streak counts consecutive days (ending today or yesterday) with at least one event.
func (s *Store) streak() int64 {
	rows, err := s.DB.Query(`SELECT DISTINCT substr(at,1,10) FROM events ORDER BY 1 DESC LIMIT 400`)
	if err != nil {
		return 0
	}
	defer rows.Close()
	days := map[string]bool{}
	for rows.Next() {
		var d string
		if rows.Scan(&d) == nil {
			days[d] = true
		}
	}
	t := time.Now().UTC()
	if !days[t.Format("2006-01-02")] {
		t = t.AddDate(0, 0, -1)
	}
	var n int64
	for days[t.Format("2006-01-02")] {
		n++
		t = t.AddDate(0, 0, -1)
	}
	return n
}

// DecisionSummary aggregates decisions by kind and the signals attached to them,
// which is what the learn-preferences skill reads.
type DecisionSummary struct {
	Counts    map[string]int64            `json:"counts"`
	Signals   map[string]map[string]int64 `json:"signals_by_kind"`
	Reasons   map[string][]string         `json:"recent_reasons"`
	Outcomes  map[string]int64            `json:"interview_outcomes"`
	ScoreGap  []ScoreGap                  `json:"score_overrides"`
	Decisions []Decision                  `json:"decisions"`
}

type ScoreGap struct {
	JobID    int64  `json:"job_id"`
	Company  string `json:"company"`
	Title    string `json:"title"`
	FitScore int64  `json:"fit_score"`
	Kind     string `json:"decision"`
}

func (s *Store) DecisionSummary(limit int) (DecisionSummary, error) {
	ds := DecisionSummary{Counts: map[string]int64{}, Signals: map[string]map[string]int64{}, Reasons: map[string][]string{}, Outcomes: map[string]int64{}}
	decs, err := s.Decisions(limit)
	if err != nil {
		return ds, err
	}
	ds.Decisions = decs
	for _, d := range decs {
		ds.Counts[d.Kind]++
		if len(ds.Reasons[d.Kind]) < 15 && d.Reason != "" {
			ds.Reasons[d.Kind] = append(ds.Reasons[d.Kind], d.Reason)
		}
		for k, v := range flattenSignals(d.Signals) {
			if ds.Signals[d.Kind] == nil {
				ds.Signals[d.Kind] = map[string]int64{}
			}
			ds.Signals[d.Kind][k+"="+v]++
		}
	}
	rows, err := s.DB.Query(`SELECT outcome, COUNT(*) FROM interviews WHERE outcome<>'' GROUP BY outcome`)
	if err == nil {
		for rows.Next() {
			var k string
			var n int64
			if rows.Scan(&k, &n) == nil {
				ds.Outcomes[k] = n
			}
		}
		rows.Close()
	}
	// high-score skips and low-score applies tell the scorer where it disagrees with the user
	rows, err = s.DB.Query(`SELECT j.id,j.company,j.title,j.fit_score,d.kind FROM decisions d JOIN jobs j ON j.id=d.job_id
		WHERE j.fit_score>=0 AND ((d.kind='skip' AND j.fit_score>=70) OR (d.kind='apply' AND j.fit_score<50)) ORDER BY d.at DESC LIMIT 30`)
	if err == nil {
		for rows.Next() {
			var g ScoreGap
			if rows.Scan(&g.JobID, &g.Company, &g.Title, &g.FitScore, &g.Kind) == nil {
				ds.ScoreGap = append(ds.ScoreGap, g)
			}
		}
		rows.Close()
	}
	return ds, nil
}
