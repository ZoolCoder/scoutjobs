package store

import (
	"sort"
	"strings"
)

// FunnelRow counts, for one group of jobs that were applied to, how far they got.
type FunnelRow struct {
	Group     string `json:"group"`
	Applied   int    `json:"applied"`
	Responded int    `json:"responded"` // any answer: interview, offer or rejection
	Interview int    `json:"interview"`
	Offer     int    `json:"offer"`
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
var FitBands = []string{"75+", "50–74", "under 50", "not scored"}

// funnelMonths caps the month table to the most recent months.
const funnelMonths = 6

// Funnel uses the same status sets as the response rate in Stats.
func (s *Store) Funnel() (Funnel, error) {
	rows, err := s.DB.Query(`SELECT j.source, j.fit_score, j.status,
		COALESCE((SELECT MIN(e.at) FROM events e WHERE e.job_id=j.id AND e.kind='status' AND e.note LIKE '%-> applied'), j.updated_at),
		EXISTS(SELECT 1 FROM interviews i WHERE i.job_id=j.id)
		FROM jobs j WHERE j.status IN ('applied','interview','offer','hired','rejected','no_response','withdrawn','offer_declined')`)
	if err != nil {
		return Funnel{}, err
	}
	defer rows.Close()
	source, fit, month := map[string]*FunnelRow{}, map[string]*FunnelRow{}, map[string]*FunnelRow{}
	for rows.Next() {
		var src, status, at string
		var score int64
		var interviewed bool
		if err := rows.Scan(&src, &score, &status, &at, &interviewed); err != nil {
			return Funnel{}, err
		}
		if src = strings.TrimSpace(src); src == "" {
			src = "manual"
		}
		if len(at) >= 7 {
			at = at[:7]
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
		return "not scored"
	case score < 50:
		return "under 50"
	case score < 75:
		return "50–74"
	}
	return "75+"
}
