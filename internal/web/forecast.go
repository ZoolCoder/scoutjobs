package web

import (
	"net/http"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/store"
)

// chance is the forecast for one job from the user's own history; nil when there is too little.
func (v *server) chance(j store.Job) *store.Chance {
	f, err := v.s.Funnel()
	if err != nil {
		return nil
	}
	c := f.Chance(j.Source, j.FitScore)
	return &c
}

// apiForecast is the 30-day outlook plus the chance per source, for automations.
func (v *server) apiForecast(w http.ResponseWriter, r *http.Request) {
	f, err := v.s.Funnel()
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	o, err := v.s.Outlook(f, time.Now().UTC())
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	bySource := map[string]store.Chance{}
	for _, row := range f.Source {
		bySource[row.Group] = f.Chance(row.Group, -1)
	}
	writeJSON(w, 200, map[string]any{"outlook": o, "by_source": bySource, "min_applications": store.ForecastMin})
}
