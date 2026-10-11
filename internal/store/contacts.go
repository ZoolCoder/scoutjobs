package store

import (
	"strings"
)

// Contact is a person the user knows, imported from a LinkedIn data export. It feeds the
// referral finder: who in the network works at the company of a tracked job.
type Contact struct {
	ID          int64  `json:"id"`
	Source      string `json:"source"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	Email       string `json:"email"`
	Company     string `json:"company"`
	Position    string `json:"position"`
	ConnectedOn string `json:"connected_on"`
}

// legalSuffix lists words that differ between how a company names itself on a posting and on LinkedIn.
var legalSuffix = map[string]bool{
	"inc": true, "llc": true, "ltd": true, "limited": true, "gmbh": true, "ag": true, "se": true, "sa": true, "sas": true,
	"bv": true, "nv": true, "plc": true, "corp": true, "corporation": true, "co": true, "company": true, "group": true,
	"holding": true, "holdings": true, "technologies": true, "technology": true, "labs": true, "hq": true, "the": true,
}

// CompanyKey normalises a company name so "Stripe, Inc." and "stripe" match.
func CompanyKey(name string) string {
	var kept []string
	for _, w := range strings.Split(Slug(name), "-") {
		if w != "" && !legalSuffix[w] {
			kept = append(kept, w)
		}
	}
	if len(kept) == 0 {
		return Slug(name)
	}
	return strings.Join(kept, "-")
}

// ReplaceContacts swaps every contact of one source for the given list, so a fresh export import is idempotent.
func (s *Store) ReplaceContacts(source string, list []Contact) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM contacts WHERE source=?`, source); err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO contacts (source,name,url,email,company,company_key,position,connected_on) VALUES (?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()
	for _, c := range list {
		if _, err := stmt.Exec(source, c.Name, c.URL, c.Email, c.Company, CompanyKey(c.Company), c.Position, c.ConnectedOn); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ContactsAt lists the contacts whose current company matches company, newest connection first.
func (s *Store) ContactsAt(company string) ([]Contact, error) {
	key := CompanyKey(company)
	if key == "" {
		return nil, nil
	}
	rows, err := s.DB.Query(`SELECT id,source,name,url,email,company,position,connected_on FROM contacts WHERE company_key=? ORDER BY connected_on DESC, name`, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Contact
	for rows.Next() {
		var c Contact
		if err := rows.Scan(&c.ID, &c.Source, &c.Name, &c.URL, &c.Email, &c.Company, &c.Position, &c.ConnectedOn); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ContactCount is the number of imported contacts, for the doctor and the profile page.
func (s *Store) ContactCount() int {
	var n int
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM contacts`).Scan(&n)
	return n
}
