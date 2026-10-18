package store

import (
	"strconv"
	"strings"
)

// SpendEvent is the event kind an agent session's cost is logged under: "<usd> <agent> <model>".
const SpendEvent = "ai:cost"

// Spend is what the agent sessions cost over a period.
type Spend struct {
	USD      float64 `json:"usd"`
	Sessions int     `json:"sessions"`
}

// SpendSince sums the logged agent costs from since, an RFC 3339 time or a date.
func (s *Store) SpendSince(since string) (Spend, error) {
	var sp Spend
	rows, err := s.DB.Query(`SELECT note FROM events WHERE kind=? AND at>=?`, SpendEvent, since)
	if err != nil {
		return sp, err
	}
	defer rows.Close()
	for rows.Next() {
		var note string
		if err := rows.Scan(&note); err != nil {
			return sp, err
		}
		usd, _, _ := strings.Cut(note, " ")
		if f, err := strconv.ParseFloat(usd, 64); err == nil {
			sp.USD += f
			sp.Sessions++
		}
	}
	return sp, rows.Err()
}
