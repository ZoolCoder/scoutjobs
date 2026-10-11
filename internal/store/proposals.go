package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Proposal is a learned preference waiting for the user: the learn-preferences skill proposes it
// with its evidence, the user confirms, edits or rejects it. Only a confirmed proposal reaches the
// preferences table; a rejected one stays here so it is not proposed again without new evidence.
type Proposal struct {
	Key        string  `json:"key"`
	Value      string  `json:"value"`
	Confidence float64 `json:"confidence"`
	Evidence   string  `json:"evidence"`
	Support    int     `json:"support"`
	Against    int     `json:"against"`
	Status     string  `json:"status"` // pending | rejected
	Note       string  `json:"note,omitempty"`
	ProposedAt string  `json:"proposed_at"`
	DecidedAt  string  `json:"decided_at,omitempty"`
	// Current is the value it would replace, empty for a new key.
	Current string `json:"current,omitempty"`
}

var ErrNoProposal = errors.New("no pending proposal for that key")

func (s *Store) statedPreference(key string) bool {
	var src string
	return s.DB.QueryRow(`SELECT source FROM preferences WHERE key=?`, key).Scan(&src) == nil && src == "stated"
}

// ProposePreference queues a learned value for review. It refuses a key the user stated, and a value
// the user rejected unless more decisions support it now than when it was rejected.
func (s *Store) ProposePreference(p Proposal) error {
	if p.Key == "" || p.Value == "" {
		return errors.New("a proposal needs a key and a value")
	}
	if s.statedPreference(p.Key) {
		return fmt.Errorf("%s: %w", p.Key, ErrStated)
	}
	if p.Confidence <= 0 {
		p.Confidence = 0.5
	}
	if p.Confidence > 0.95 {
		p.Confidence = 0.95
	}
	var old Proposal
	err := s.DB.QueryRow(`SELECT value, support, status, decided_at FROM pref_proposals WHERE key=?`, p.Key).Scan(&old.Value, &old.Support, &old.Status, &old.DecidedAt)
	if err == nil && old.Status == "rejected" && strings.EqualFold(old.Value, p.Value) && p.Support <= old.Support {
		return fmt.Errorf("%s = %s was rejected on %s with %d supporting decisions; propose it again only with more", p.Key, p.Value, day(old.DecidedAt), old.Support)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = s.DB.Exec(`INSERT INTO pref_proposals (key,value,confidence,evidence,support,against,status,note,proposed_at,decided_at) VALUES (?,?,?,?,?,?,'pending','',?,'')
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, confidence=excluded.confidence, evidence=excluded.evidence, support=excluded.support,
		against=excluded.against, status='pending', note='', proposed_at=excluded.proposed_at, decided_at=''`,
		p.Key, p.Value, p.Confidence, p.Evidence, p.Support, p.Against, now())
	if err == nil {
		_ = s.AddEvent(0, "pref:proposed", fmt.Sprintf("%s = %s (%.0f%%, %d for, %d against)", p.Key, p.Value, p.Confidence*100, p.Support, p.Against))
	}
	return err
}

// Proposals lists pending proposals first, then rejected ones when all is set.
func (s *Store) Proposals(all bool) ([]Proposal, error) {
	q := `SELECT p.key,p.value,p.confidence,p.evidence,p.support,p.against,p.status,p.note,p.proposed_at,p.decided_at,COALESCE(f.value,'')
		FROM pref_proposals p LEFT JOIN preferences f ON f.key=p.key`
	if !all {
		q += ` WHERE p.status='pending'`
	}
	q += ` ORDER BY CASE p.status WHEN 'pending' THEN 0 ELSE 1 END, p.confidence DESC, p.key`
	rows, err := s.DB.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Proposal
	for rows.Next() {
		var p Proposal
		if err := rows.Scan(&p.Key, &p.Value, &p.Confidence, &p.Evidence, &p.Support, &p.Against, &p.Status, &p.Note, &p.ProposedAt, &p.DecidedAt, &p.Current); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ConfirmProposal turns a pending proposal into a preference. An edited value is the user's own
// words, so it is stored as stated with full confidence; otherwise it is learned at the proposed
// confidence. The proposal row is removed: the preferences table is the record from here on.
func (s *Store) ConfirmProposal(key, edited string) (Preference, error) {
	var p Proposal
	err := s.DB.QueryRow(`SELECT value, confidence, evidence FROM pref_proposals WHERE key=? AND status='pending'`, key).Scan(&p.Value, &p.Confidence, &p.Evidence)
	if errors.Is(err, sql.ErrNoRows) {
		return Preference{}, fmt.Errorf("%s: %w", key, ErrNoProposal)
	}
	if err != nil {
		return Preference{}, err
	}
	pref := Preference{Key: key, Value: p.Value, Confidence: p.Confidence, Evidence: p.Evidence, Source: "learned"}
	kind := "pref:confirmed"
	if edited = strings.TrimSpace(edited); edited != "" && edited != p.Value {
		pref.Value, pref.Confidence, pref.Source = edited, 1, "stated"
		kind = "pref:edited"
	}
	if err := s.SetPreference(pref); err != nil {
		return Preference{}, err
	}
	if _, err := s.DB.Exec(`DELETE FROM pref_proposals WHERE key=?`, key); err != nil {
		return Preference{}, err
	}
	note := fmt.Sprintf("%s = %s (%s)", key, pref.Value, pref.Source)
	if kind == "pref:edited" {
		note += ", proposed " + p.Value
	}
	_ = s.AddEvent(0, kind, note)
	return pref, s.RenderPreferences()
}

// RejectProposal keeps the proposal as rejected, with the user's reason, so it is not proposed again.
func (s *Store) RejectProposal(key, reason string) error {
	res, err := s.DB.Exec(`UPDATE pref_proposals SET status='rejected', note=?, decided_at=? WHERE key=? AND status='pending'`, strings.TrimSpace(reason), now(), key)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%s: %w", key, ErrNoProposal)
	}
	note := key
	if reason != "" {
		note += ": " + reason
	}
	return s.AddEvent(0, "pref:rejected", note)
}

// RenderPreferences rewrites preferences.md from the preferences table; skills read that file.
func (s *Store) RenderPreferences() error {
	prefs, err := s.Preferences()
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("# Preferences\n\nRendered by `scoutjobs pref render`. Edit with `scoutjobs pref set <key> <value>`.\n")
	b.WriteString("Stated = the user said so. Learned = inferred from decisions; confidence shows how sure.\n\n")
	section := func(title, source string) {
		var any bool
		for _, p := range prefs {
			if p.Source != source {
				continue
			}
			if !any {
				b.WriteString("## " + title + "\n\n")
				any = true
			}
			fmt.Fprintf(&b, "- **%s**: %s", p.Key, p.Value)
			if source == "learned" {
				fmt.Fprintf(&b, " _(confidence %.0f%%)_", p.Confidence*100)
			}
			if p.Evidence != "" {
				fmt.Fprintf(&b, " — %s", p.Evidence)
			}
			b.WriteString("\n")
		}
		if any {
			b.WriteString("\n")
		}
	}
	section("Stated", "stated")
	section("Learned", "learned")
	if len(prefs) == 0 {
		b.WriteString("Nothing learned yet. Decisions you make (skip, apply, decline) teach this file.\n")
	}
	return os.WriteFile(filepath.Join(s.Home, "preferences.md"), []byte(b.String()), 0o600)
}

func day(ts string) string {
	if len(ts) >= 10 {
		return ts[:10]
	}
	return ts
}
