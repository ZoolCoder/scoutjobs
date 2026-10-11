package cli

import (
	"context"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/search"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

// fetchPosting is search.FetchPosting; tests point it at a fake.
var fetchPosting = func(ctx context.Context, u string) (search.Posting, error) {
	return search.FetchPosting(ctx, u, nil, nil)
}

// fillFromPosting fetches the posting at u and fills every field of j the user left empty.
// The posting's description lands in Notes so the fit score has text to read.
func fillFromPosting(j *store.Job, u string) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	p, err := fetchPosting(ctx, u)
	if err != nil {
		return err
	}
	pj := p.Job()
	pick := func(dst *string, v string) {
		if *dst == "" {
			*dst = v
		}
	}
	pick(&j.Company, pj.Company)
	pick(&j.Title, pj.Title)
	pick(&j.URL, u)
	pick(&j.Location, pj.Location)
	pick(&j.Remote, pj.Remote)
	pick(&j.Source, pj.Source)
	pick(&j.PostedAt, pj.PostedAt)
	pick(&j.Tags, pj.Tags)
	pick(&j.Currency, pj.Currency)
	pick(&j.Notes, search.Excerpt(p.Description, 4000))
	if j.SalaryMin == 0 && j.SalaryMax == 0 {
		j.SalaryMin, j.SalaryMax = pj.SalaryMin, pj.SalaryMax
	}
	return nil
}
