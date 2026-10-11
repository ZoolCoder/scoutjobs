package store

import "database/sql"

// RecordMail remembers a Message-ID: dir "out" for applications we sent (matched later by In-Reply-To),
// dir "in" for inbox replies already processed, so a sync never logs the same reply twice.
func (s *Store) RecordMail(messageID string, jobID int64, dir string, uid uint32) error {
	_, err := s.DB.Exec(`INSERT OR IGNORE INTO mail (message_id,job_id,dir,uid,at) VALUES (?,?,?,?,?)`,
		messageID, jobID, dir, uid, now())
	return err
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
