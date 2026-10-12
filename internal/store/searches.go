package store

import "strings"

// SavedSearch is a named search page setup: queries, boards, remote and the result filters.
type SavedSearch struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Queries   string `json:"queries"`
	Boards    string `json:"boards"`
	Remote    bool   `json:"remote"`
	Filters   string `json:"filters"`  // url-encoded result filters, e.g. remote_only=1&exclude=senior
	Daily     bool   `json:"daily"`    // the autopilot runs it once a day and tracks the good matches
	LastRun   string `json:"last_run"` // RFC3339 of the last autopilot run, "" when never
	CreatedAt string `json:"created_at"`
}

// SaveSearch stores a search under its name; the same name overwrites.
func (s *Store) SaveSearch(x SavedSearch) (int64, error) {
	x.Name = strings.TrimSpace(x.Name)
	_, err := s.DB.Exec(`INSERT INTO saved_searches (name,queries,boards,remote,filters,created_at) VALUES (?,?,?,?,?,?)
		ON CONFLICT(name) DO UPDATE SET queries=excluded.queries, boards=excluded.boards, remote=excluded.remote, filters=excluded.filters`,
		x.Name, x.Queries, x.Boards, x.Remote, x.Filters, now())
	if err != nil {
		return 0, err
	}
	var id int64
	err = s.DB.QueryRow(`SELECT id FROM saved_searches WHERE name=?`, x.Name).Scan(&id)
	return id, err
}

func (s *Store) SavedSearches() ([]SavedSearch, error) {
	rows, err := s.DB.Query(`SELECT id,name,queries,boards,remote,filters,daily,last_run,created_at FROM saved_searches ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SavedSearch
	for rows.Next() {
		var x SavedSearch
		if err := rows.Scan(&x.ID, &x.Name, &x.Queries, &x.Boards, &x.Remote, &x.Filters, &x.Daily, &x.LastRun, &x.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) SavedSearch(id int64) (SavedSearch, error) {
	var x SavedSearch
	err := s.DB.QueryRow(`SELECT id,name,queries,boards,remote,filters,daily,last_run,created_at FROM saved_searches WHERE id=?`, id).
		Scan(&x.ID, &x.Name, &x.Queries, &x.Boards, &x.Remote, &x.Filters, &x.Daily, &x.LastRun, &x.CreatedAt)
	return x, err
}

func (s *Store) DeleteSearch(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM saved_searches WHERE id=?`, id)
	return err
}

// SetSearchDaily turns the daily autopilot run of a saved search on or off.
func (s *Store) SetSearchDaily(id int64, on bool) error {
	_, err := s.DB.Exec(`UPDATE saved_searches SET daily=? WHERE id=?`, on, id)
	return err
}

// MarkSearchRun records when the autopilot last ran a saved search.
func (s *Store) MarkSearchRun(id int64, at string) error {
	_, err := s.DB.Exec(`UPDATE saved_searches SET last_run=? WHERE id=?`, at, id)
	return err
}
