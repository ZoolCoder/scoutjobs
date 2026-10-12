package store

import (
	"math"
	"time"
)

// ForecastMin is how many applications the forecast needs before it says anything.
const ForecastMin = 15

// prior is how many applications' worth of weight the overall rate gets when a group is small, so
// three applications from one source do not swing its chances to 0 or 100.
const prior = 5

// Chance is what the user's own history says about one more application of a kind.
type Chance struct {
	Answer    int  `json:"answer"`    // chance of any answer, percent
	Interview int  `json:"interview"` // chance of an interview, percent
	Low       int  `json:"low"`       // answer chance range, percent
	High      int  `json:"high"`
	Days      int  `json:"days,omitempty"` // usual wait for an answer from this source
	Basis     int  `json:"basis"`          // applications the estimate rests on
	Enough    bool `json:"enough"`
}

func (f Funnel) totals() (applied, answered, interviews int) {
	for _, r := range f.Source {
		applied, answered, interviews = applied+r.Applied, answered+r.Responded, interviews+r.Interview
	}
	return
}

func rowOf(rows []FunnelRow, group string) FunnelRow {
	for _, r := range rows {
		if r.Group == group {
			return r
		}
	}
	return FunnelRow{Group: group}
}

// smooth pulls a group's rate towards the overall rate in proportion to how little data it has.
func smooth(hits, n int, overall float64) float64 {
	return (float64(hits) + prior*overall) / float64(n+prior)
}

// Chance estimates the answer and interview chances of an application from source with fit score
// fit: the source's rate, adjusted by how the fit band does against the overall rate.
func (f Funnel) Chance(source string, fit int64) Chance {
	applied, answered, interviews := f.totals()
	c := Chance{Basis: applied, Enough: applied >= ForecastMin}
	if !c.Enough {
		return c
	}
	if source == "" {
		source = "manual"
	}
	p0, q0 := float64(answered)/float64(applied), float64(interviews)/float64(applied)
	src, band := rowOf(f.Source, source), rowOf(f.Fit, fitBand(fit))
	p, q := smooth(src.Responded, src.Applied, p0), smooth(src.Interview, src.Applied, q0)
	if p0 > 0 {
		p *= smooth(band.Responded, band.Applied, p0) / p0
	}
	if q0 > 0 {
		q *= smooth(band.Interview, band.Applied, q0) / q0
	}
	p, q = clamp(p, 0.01, 0.95), clamp(q, 0.01, 0.95)
	q = math.Min(q, p)
	lo, hi := wilson(p, src.Applied+band.Applied+prior)
	c.Answer, c.Interview, c.Low, c.High = pct(p), pct(q), pct(lo), pct(hi)
	c.Days, _ = f.AnswerDays(source)
	return c
}

// wilson is the 80% Wilson score interval of rate p over n trials.
func wilson(p float64, n int) (float64, float64) {
	const z = 1.2816
	fn := float64(n)
	mid := (p + z*z/(2*fn)) / (1 + z*z/fn)
	half := z * math.Sqrt(p*(1-p)/fn+z*z/(4*fn*fn)) / (1 + z*z/fn)
	return math.Max(0, mid-half), math.Min(1, mid+half)
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

func pct(v float64) int { return int(math.Round(v * 100)) }

// Outlook is the forecast for the next 30 days.
type Outlook struct {
	Waiting   int     `json:"waiting"`  // applications from the last 45 days still without an answer
	Expected  float64 `json:"expected"` // interviews those applications should bring
	Low       int     `json:"low"`
	High      int     `json:"high"`
	Scheduled int     `json:"scheduled"` // interviews already booked
	ToApply   int     `json:"to_apply"`  // more applications for one more interview, at the usual rate
	Rate      int     `json:"rate"`      // interviews per 100 applications so far
	Basis     int     `json:"basis"`
	Enough    bool    `json:"enough"`
}

// outlookWindow is how old an unanswered application may be and still count as waiting.
const outlookWindow = 45

// Outlook sums the interview chances of the applications still waiting for an answer.
func (s *Store) Outlook(f Funnel, now time.Time) (Outlook, error) {
	applied, _, interviews := f.totals()
	o := Outlook{Basis: applied, Enough: applied >= ForecastMin}
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM interviews WHERE outcome='' AND at >= ?`, now.Format("2006-01-02")).Scan(&o.Scheduled)
	if !o.Enough {
		return o, nil
	}
	o.Rate = interviews * 100 / applied
	if interviews > 0 {
		o.ToApply = int(math.Ceil(float64(applied) / float64(interviews)))
	}
	since := now.AddDate(0, 0, -outlookWindow).UTC().Format(time.RFC3339)
	rows, err := s.DB.Query(`SELECT j.source, j.fit_score FROM jobs j WHERE j.status='applied' AND
		COALESCE((SELECT MIN(e.at) FROM events e WHERE e.job_id=j.id AND e.kind='status' AND e.note LIKE '%-> applied'), j.updated_at) >= ?`, since)
	if err != nil {
		return o, err
	}
	defer rows.Close()
	var variance float64
	for rows.Next() {
		var src string
		var fit int64
		if err := rows.Scan(&src, &fit); err != nil {
			return o, err
		}
		q := float64(f.Chance(src, fit).Interview) / 100
		o.Waiting++
		o.Expected += q
		variance += q * (1 - q)
	}
	spread := 1.2816 * math.Sqrt(variance)
	o.Low, o.High = int(math.Max(0, math.Floor(o.Expected-spread))), int(math.Ceil(o.Expected+spread))
	o.Expected = math.Round(o.Expected*10) / 10
	return o, rows.Err()
}

// AppliedSince lists the jobs applied to that changed since the RFC 3339 time.
func (s *Store) AppliedSince(since string) ([]Job, error) {
	rows, err := s.DB.Query(`SELECT `+jobCols+` FROM jobs WHERE status IN ('applied','interview','offer','hired','rejected','no_response','withdrawn','offer_declined') AND updated_at >= ? ORDER BY updated_at DESC`, since)
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
