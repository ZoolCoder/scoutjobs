package store

import "fmt"

// FollowUpEvent marks one follow-up the user sent on a quiet application.
const FollowUpEvent = "followup:sent"

// MaxFollowUps is how many follow-ups an application gets; after that it waits for the stale sweep.
const MaxFollowUps = 2

// FollowedUp records a sent follow-up and restarts the job's quiet clock, so the next one falls due a
// full follow-up interval later. It returns how many follow-ups the job has had.
func (s *Store) FollowedUp(id int64, note string) (int, error) {
	n := s.FollowUps(id)
	if n >= MaxFollowUps {
		return n, fmt.Errorf("already followed up %d times; leave it or mark it no_response", n)
	}
	if _, err := s.DB.Exec(`UPDATE jobs SET updated_at=? WHERE id=?`, now(), id); err != nil {
		return n, err
	}
	if note == "" {
		note = fmt.Sprintf("follow-up %d of %d", n+1, MaxFollowUps)
	}
	return n + 1, s.AddEvent(id, FollowUpEvent, note)
}

// CloseStale marks each quiet job no_response.
func (s *Store) CloseStale(jobs []Job) error {
	st := "no_response"
	for _, j := range jobs {
		if err := s.UpdateJob(j.ID, JobPatch{Status: &st}); err != nil {
			return err
		}
		_ = s.AddEvent(j.ID, "sweep", "closed after going quiet")
	}
	return nil
}

// FollowUps counts the follow-ups sent on a job.
func (s *Store) FollowUps(id int64) int {
	var n int
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE job_id=? AND kind=?`, id, FollowUpEvent).Scan(&n)
	return n
}
