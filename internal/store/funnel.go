package store

import (
	"sort"
	"strings"
	"time"
)

// FunnelRow counts, for one group of jobs that were applied to, how far they got.
type FunnelRow struct {
	Group     string `json:"group"`
	Applied   int    `json:"applied"`
	Responded int    `json:"responded"` // any answer: interview, offer or rejection
	Interview int    `json:"interview"`
	Offer     int    `json:"offer"`
	Days      int    `json:"days,omitempty"` // median days from applying to the first answer, once three answers are known
	waits     []int
}

// Rate is the share of applications that got an answer, 0-100.
func (r FunnelRow) Rate() int {
	if r.Applied == 0 {
		return 0
	}
	return r.Responded * 100 / r.Applied
}

// Funnel groups the applied jobs three ways: by source, by fit band and by the month they were applied to.
type Funnel struct {
	Source []FunnelRow `json:"source"`
	Fit    []FunnelRow `json:"fit"`
	Month  []FunnelRow `json:"month"`
}

// FitBands are the fit groups, in display order.
var FitBands = []string{"75+", "50–74", "under 50", "unscored"}

// funnelMonths caps the month table to the most recent months.
const funnelMonths = 6

// Funnel uses the same status sets as the response rate in Stats.
func (s *Store) Funnel() (Funnel, error) {
	rows, err := s.DB.Query(`SELECT j.source, j.fit_score, j.status,
		COALESCE((SELECT MIN(e.at) FROM events e WHERE e.job_id=j.id AND e.kind='status' AND e.note LIKE '%-> applied'), j.updated_at),
		COALESCE((SELECT MIN(e.at) FROM events e WHERE e.job_id=j.id AND e.kind='status'
			AND (e.note LIKE '%-> interview' OR e.note LIKE '%-> rejected' OR e.note LIKE '%-> offer')), ''),
		EXISTS(SELECT 1 FROM interviews i WHERE i.job_id=j.id)
		FROM jobs j WHERE j.status IN ('applied','interview','offer','hired','rejected','no_response','withdrawn','offer_declined')`)
	if err != nil {
		return Funnel{}, err
	}
	defer rows.Close()
	source, fit, month := map[string]*FunnelRow{}, map[string]*FunnelRow{}, map[string]*FunnelRow{}
	for rows.Next() {
		var src, status, at, answered string
		var score int64
		var interviewed bool
		if err := rows.Scan(&src, &score, &status, &at, &answered, &interviewed); err != nil {
			return Funnel{}, err
		}
		wait, waited := daysBetween(at, answered)
		if src = strings.TrimSpace(src); src == "" {
			src = "manual"
		}
		if len(at) >= 7 {
			at = at[:7]
		}
		if waited {
			group(source, src).waits = append(group(source, src).waits, wait)
		}
		for _, g := range []*FunnelRow{group(source, src), group(fit, fitBand(score)), group(month, at)} {
			g.Applied++
			switch status {
			case "interview", "offer", "hired", "rejected", "offer_declined":
				g.Responded++
			}
			if interviewed || status == "interview" || status == "offer" || status == "hired" || status == "offer_declined" {
				g.Interview++
			}
			if status == "offer" || status == "hired" || status == "offer_declined" {
				g.Offer++
			}
		}
	}
	if err := rows.Err(); err != nil {
		return Funnel{}, err
	}
	var f Funnel
	for _, r := range source {
		r.Days = median(r.waits)
	}
	f.Source = flatten(source)
	sort.SliceStable(f.Source, func(i, k int) bool { return f.Source[i].Applied > f.Source[k].Applied })
	for _, b := range FitBands {
		if r := fit[b]; r != nil {
			f.Fit = append(f.Fit, *r)
		}
	}
	f.Month = flatten(month)
	sort.Slice(f.Month, func(i, k int) bool { return f.Month[i].Group > f.Month[k].Group })
	if len(f.Month) > funnelMonths {
		f.Month = f.Month[:funnelMonths]
	}
	return f, nil
}

// Best names the source with the highest response rate among those with at least three applications.
func (f Funnel) Best() *FunnelRow {
	var best *FunnelRow
	for i, r := range f.Source {
		if r.Applied >= 3 && (best == nil || r.Rate() > best.Rate()) {
			best = &f.Source[i]
		}
	}
	return best
}

// AnswerDays is how many days the source usually takes to answer; false while too few answers are known.
func (f Funnel) AnswerDays(source string) (int, bool) {
	if source = strings.TrimSpace(source); source == "" {
		source = "manual"
	}
	for _, r := range f.Source {
		if r.Group == source && r.Days > 0 {
			return r.Days, true
		}
	}
	return 0, false
}

// minAnswers is how many answers a source needs before its typical wait means anything.
const minAnswers = 3

func median(waits []int) int {
	if len(waits) < minAnswers {
		return 0
	}
	sort.Ints(waits)
	return max(1, waits[len(waits)/2])
}

func daysBetween(from, to string) (int, bool) {
	a, errA := parseAt(from)
	b, errB := parseAt(to)
	if errA != nil || errB != nil || b.Before(a) {
		return 0, false
	}
	return int(b.Sub(a).Hours() / 24), true
}

func parseAt(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02", s[:min(len(s), 10)])
}

func group(m map[string]*FunnelRow, key string) *FunnelRow {
	if m[key] == nil {
		m[key] = &FunnelRow{Group: key}
	}
	return m[key]
}

func flatten(m map[string]*FunnelRow) []FunnelRow {
	out := make([]FunnelRow, 0, len(m))
	for _, r := range m {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, k int) bool { return out[i].Group < out[k].Group })
	return out
}

func fitBand(score int64) string {
	switch {
	case score < 0:
		return "unscored"
	case score < 50:
		return "under 50"
	case score < 75:
		return "50–74"
	}
	return "75+"
}
