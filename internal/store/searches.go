package store

import "strings"

// SavedSearch is a named search page setup: queries, boards, remote and the result filters.
type SavedSearch struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Queries   string `json:"queries"`
	Boards    string `json:"boards"`
	Remote    bool   `json:"remote"`
	Filters   string `json:"filters"` // url-encoded result filters, e.g. remote_only=1&exclude=senior
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
	rows, err := s.DB.Query(`SELECT id,name,queries,boards,remote,filters,created_at FROM saved_searches ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SavedSearch
	for rows.Next() {
		var x SavedSearch
		if err := rows.Scan(&x.ID, &x.Name, &x.Queries, &x.Boards, &x.Remote, &x.Filters, &x.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) SavedSearch(id int64) (SavedSearch, error) {
	var x SavedSearch
	err := s.DB.QueryRow(`SELECT id,name,queries,boards,remote,filters,created_at FROM saved_searches WHERE id=?`, id).
		Scan(&x.ID, &x.Name, &x.Queries, &x.Boards, &x.Remote, &x.Filters, &x.CreatedAt)
	return x, err
}

func (s *Store) DeleteSearch(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM saved_searches WHERE id=?`, id)
	return err
}
