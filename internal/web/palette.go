package web

import (
	"net/http"

	"github.com/zoolcoder/scoutjobs/internal/store"
)

// paletteJob is what the command palette searches: enough to find and open a job, nothing more.
type paletteJob struct {
	ID      int64  `json:"id"`
	Company string `json:"company"`
	Title   string `json:"title"`
	Status  string `json:"status"`
}

func (v *server) paletteJSON(w http.ResponseWriter, r *http.Request) {
	jobs, err := v.s.ListJobs(store.JobFilter{})
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	out := make([]paletteJob, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, paletteJob{j.ID, j.Company, j.Title, j.Status})
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, out)
}
