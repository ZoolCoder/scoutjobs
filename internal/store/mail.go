package store

import (
	"database/sql"
	"strings"
)

// RecordMail remembers a Message-ID: dir "out" for applications we sent (matched later by In-Reply-To),
// dir "in" for inbox replies already processed, so a sync never logs the same reply twice.
func (s *Store) RecordMail(messageID string, jobID int64, dir string, uid uint32) error {
	_, err := s.DB.Exec(`INSERT OR IGNORE INTO mail (message_id,job_id,dir,uid,at) VALUES (?,?,?,?,?)`,
		messageID, jobID, dir, uid, now())
	return err
}

// RecordDraft remembers a draft made in Gmail: its Message-ID and Gmail thread, so a reply in that
// thread finds the job even when Gmail gives the sent message a new Message-ID.
func (s *Store) RecordDraft(messageID string, jobID int64, thread string) error {
	_, err := s.DB.Exec(`INSERT OR IGNORE INTO mail (message_id,job_id,dir,uid,at,thread,source) VALUES (?,?,'out',0,?,?,'gmail')`,
		messageID, jobID, now(), thread)
	return err
}

// ThreadJob returns the job a Gmail thread was matched to before, by a draft or an earlier reply.
func (s *Store) ThreadJob(thread string) (int64, bool) {
	if thread == "" {
		return 0, false
	}
	var id int64
	err := s.DB.QueryRow(`SELECT job_id FROM mail WHERE thread=? AND job_id IS NOT NULL ORDER BY at DESC LIMIT 1`, thread).Scan(&id)
	return id, err == nil
}

// JobThread is the job's latest Gmail thread and a Message-ID in it, for a follow-up to answer.
func (s *Store) JobThread(jobID int64) (thread, messageID string) {
	_ = s.DB.QueryRow(`SELECT thread,message_id FROM mail WHERE job_id=? AND thread<>'' ORDER BY at DESC LIMIT 1`, jobID).Scan(&thread, &messageID)
	return thread, messageID
}

// MailJob returns the job an outbound Message-ID belongs to.
func (s *Store) MailJob(messageID string) (int64, bool) {
	var id int64
	err := s.DB.QueryRow(`SELECT job_id FROM mail WHERE message_id=? AND dir='out'`, messageID).Scan(&id)
	return id, err == nil
}

// MailSeen reports whether an inbox message was already processed.
func (s *Store) MailSeen(messageID string) bool {
	var one int
	err := s.DB.QueryRow(`SELECT 1 FROM mail WHERE message_id=? AND dir='in'`, messageID).Scan(&one)
	return err != sql.ErrNoRows && err == nil
}

// Mail is one message matched to a job, with what the dashboard needs to show and open it.
type Mail struct {
	MessageID string `json:"message_id"`
	JobID     int64  `json:"job_id"`
	Dir       string `json:"dir"` // in | out
	From      string `json:"from"`
	Subject   string `json:"subject"`
	Snippet   string `json:"snippet"`
	Date      string `json:"date"`   // when the message was sent, RFC3339 when known
	Thread    string `json:"thread"` // Gmail thread id when the source knows it
	Source    string `json:"source"` // imap | gmail | connector | smtp
	Kind      string `json:"kind"`   // rejected | interview | "" from the classifier or the agent
	At        string `json:"at"`     // when ScoutJobs recorded it
	Company   string `json:"company"`
	Title     string `json:"title"`
}

// LogMail stores a matched message with its details. A known Message-ID gains the details it lacked
// and keeps everything else; the bool reports whether the message was new.
func (s *Store) LogMail(m Mail) (bool, error) {
	if m.Dir == "" {
		m.Dir = "in"
	}
	if len(m.Snippet) > 400 {
		m.Snippet = m.Snippet[:400]
	}
	m.Snippet = strings.Join(strings.Fields(m.Snippet), " ")
	date := m.Date
	if date == "" {
		date = now()
	}
	res, err := s.DB.Exec(`INSERT OR IGNORE INTO mail (message_id,job_id,dir,uid,at,from_addr,subject,snippet,date,thread,source,kind) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.MessageID, m.JobID, m.Dir, 0, now(), m.From, m.Subject, m.Snippet, date, m.Thread, m.Source, m.Kind)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return true, nil
	}
	_, err = s.DB.Exec(`UPDATE mail SET from_addr=CASE WHEN from_addr='' THEN ? ELSE from_addr END, subject=CASE WHEN subject='' THEN ? ELSE subject END,
		snippet=CASE WHEN snippet='' THEN ? ELSE snippet END, thread=CASE WHEN thread='' THEN ? ELSE thread END WHERE message_id=?`,
		m.From, m.Subject, m.Snippet, m.Thread, m.MessageID)
	return false, err
}

// Mails lists messages with details, newest first; jobID 0 lists every job. Bare ids from before 0.3 are left out.
func (s *Store) Mails(jobID int64, limit int) ([]Mail, error) {
	q := `SELECT m.message_id,m.job_id,m.dir,m.from_addr,m.subject,m.snippet,m.date,m.thread,m.source,m.kind,m.at,j.company,j.title
		FROM mail m JOIN jobs j ON j.id=m.job_id WHERE m.subject<>''`
	var args []any
	if jobID > 0 {
		q += ` AND m.job_id=?`
		args = append(args, jobID)
	}
	q += ` ORDER BY m.date DESC, m.at DESC`
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Mail
	for rows.Next() {
		var m Mail
		if err := rows.Scan(&m.MessageID, &m.JobID, &m.Dir, &m.From, &m.Subject, &m.Snippet, &m.Date, &m.Thread, &m.Source, &m.Kind, &m.At, &m.Company, &m.Title); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// MailCounts maps job id to the number of messages with details, for badges on the board.
func (s *Store) MailCounts() map[int64]int {
	out := map[int64]int{}
	rows, err := s.DB.Query(`SELECT job_id, COUNT(*) FROM mail WHERE subject<>'' GROUP BY job_id`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var n int
		if rows.Scan(&id, &n) == nil {
			out[id] = n
		}
	}
	return out
}
