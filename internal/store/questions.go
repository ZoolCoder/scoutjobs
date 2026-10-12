package store

import (
	"strings"
)

// Question is one interview question in the bank of a company: from the posting, reported by other
// candidates, asked in a real interview, or added by the user.
type Question struct {
	ID        int64  `json:"id"`
	Company   string `json:"company_key"`
	JobID     int64  `json:"job_id,omitempty"`
	Text      string `json:"text"`
	Kind      string `json:"kind,omitempty"` // behavioral, technical, system_design, role, culture
	Source    string `json:"source"`         // posting, prep, reported, debrief, manual
	Rehearsed bool   `json:"rehearsed"`
	Rating    int    `json:"rating,omitempty"` // how well the last answer went, 1-5; 0 when not rated
	CreatedAt string `json:"created_at"`
}

// QuestionKinds and QuestionSources are the accepted values.
var (
	QuestionKinds   = []string{"behavioral", "technical", "system_design", "role", "culture"}
	QuestionSources = map[string]string{"posting": "From the posting", "prep": "Likely, from the prep pack", "reported": "Reported by candidates", "debrief": "Asked in your interview", "manual": "Added by you"}
)

// maxQuestion caps the length of one question.
const maxQuestion = 500

// AddQuestion files a question under the company of the job; a question the bank already holds is
// left as it is. added is false then.
func (s *Store) AddQuestion(j Job, q Question) (added bool, err error) {
	q.Text = strings.Join(strings.Fields(q.Text), " ")
	if q.Text == "" {
		return false, nil
	}
	if len(q.Text) > maxQuestion {
		q.Text = q.Text[:maxQuestion]
	}
	if _, ok := QuestionSources[q.Source]; !ok {
		q.Source = "manual"
	}
	if !knownKind(q.Kind) {
		q.Kind = ""
	}
	res, err := s.DB.Exec(`INSERT OR IGNORE INTO questions (company_key,job_id,text,kind,source,created_at) VALUES (?,?,?,?,?,?)`,
		CompanyKey(j.Company), j.ID, q.Text, q.Kind, q.Source, now())
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func knownKind(k string) bool {
	for _, v := range QuestionKinds {
		if v == k {
			return true
		}
	}
	return false
}

// Questions is the bank of the job's company, questions not yet rehearsed first.
func (s *Store) Questions(company string) ([]Question, error) {
	rows, err := s.DB.Query(`SELECT id,company_key,COALESCE(job_id,0),text,kind,source,rehearsed,rating,created_at FROM questions WHERE company_key=? ORDER BY rehearsed, created_at, id`, CompanyKey(company))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Question
	for rows.Next() {
		var q Question
		if err := rows.Scan(&q.ID, &q.Company, &q.JobID, &q.Text, &q.Kind, &q.Source, &q.Rehearsed, &q.Rating, &q.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// QuestionByID returns one question of the bank.
func (s *Store) QuestionByID(id int64) (Question, error) {
	var q Question
	err := s.DB.QueryRow(`SELECT id,company_key,COALESCE(job_id,0),text,kind,source,rehearsed,rating,created_at FROM questions WHERE id=?`, id).
		Scan(&q.ID, &q.Company, &q.JobID, &q.Text, &q.Kind, &q.Source, &q.Rehearsed, &q.Rating, &q.CreatedAt)
	return q, err
}

// RateQuestion marks a question rehearsed and keeps how the answer went, 1-5; 0 keeps the rating.
func (s *Store) RateQuestion(id int64, rehearsed bool, rating int) error {
	if rating < 0 || rating > 5 {
		rating = 0
	}
	_, err := s.DB.Exec(`UPDATE questions SET rehearsed=?, rating=CASE WHEN ?>0 THEN ? ELSE rating END WHERE id=?`, rehearsed, rating, rating, id)
	return err
}

func (s *Store) DeleteQuestion(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM questions WHERE id=?`, id)
	return err
}

// Rehearsed counts the questions of a bank and those rehearsed.
func Rehearsed(qs []Question) (done, total int) {
	for _, q := range qs {
		if q.Rehearsed {
			done++
		}
	}
	return done, len(qs)
}
