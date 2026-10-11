package store

import (
	"net/url"
	"regexp"
	"strings"
)

// A posting shows up on several boards under slightly different titles and URLs. The key
// normalises the title; the URL match catches the same page tracked twice.

var (
	titleNoise = regexp.MustCompile(`(?i)\((?:all genders|m/?w/?d|w/?m/?d|m/?f/?d|f/?m/?d|m/?f/?x|f/?m/?x|d/?m/?w|x/?f/?m|h/?f|remote|hybrid|onsite|on-site|remote ok|fully remote)\)|\b(?:all genders|m/w/d|w/m/d|m/f/d|f/m/d|m/f/x|f/m/x|d/m/w)\b`)
	titleWords = map[string]string{"sr": "senior", "snr": "senior", "jr": "junior", "sw": "software", "swe": "software engineer", "eng": "engineer", "dev": "developer", "mgr": "manager"}
)

// TitleKey is the normalised title used in JobKey: lowercase, gender and remote markers dropped,
// common abbreviations expanded, punctuation collapsed.
func TitleKey(title string) string {
	t := titleNoise.ReplaceAllString(strings.ToLower(title), " ")
	t = strings.NewReplacer("&", " and ", "/", " ", "+", " plus ", "#", "sharp ").Replace(t)
	words := strings.Fields(nonAlnum.ReplaceAllString(t, " "))
	for i, w := range words {
		if full, ok := titleWords[w]; ok {
			words[i] = full
		}
	}
	return strings.Join(words, "-")
}

// JobKey identifies a posting across boards: company slug plus the normalised title.
func JobKey(company, title string) string { return Slug(company) + "|" + TitleKey(title) }

// NormalizeURL drops the parts that differ between two visits of the same posting:
// scheme, tracking query, fragment, trailing slash, host case.
func NormalizeURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return strings.ToLower(strings.TrimRight(raw, "/"))
	}
	host := strings.ToLower(strings.TrimPrefix(u.Host, "www."))
	path := strings.TrimRight(u.Path, "/")
	q := url.Values{}
	for k, vs := range u.Query() {
		lk := strings.ToLower(k)
		if strings.HasPrefix(lk, "utm_") || lk == "ref" || lk == "source" || lk == "gh_src" || lk == "lever-source" || lk == "src" {
			continue
		}
		q[k] = vs
	}
	out := host + path
	if enc := q.Encode(); enc != "" {
		out += "?" + enc
	}
	return out
}

// JobByURL finds a tracked job by its posting URL, ignoring tracking noise.
func (s *Store) JobByURL(raw string) (Job, bool) {
	want := NormalizeURL(raw)
	if want == "" {
		return Job{}, false
	}
	rows, err := s.DB.Query(`SELECT ` + jobCols + ` FROM jobs WHERE url<>'' ORDER BY id`)
	if err != nil {
		return Job{}, false
	}
	defer rows.Close()
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			continue
		}
		if NormalizeURL(j.URL) == want {
			return j, true
		}
	}
	return Job{}, false
}

// rekeyJobs recomputes every key once after the normalisation changed. A row whose new key
// collides with another keeps its old key; the duplicate is left for the user to merge.
func (s *Store) rekeyJobs() error {
	rows, err := s.DB.Query(`SELECT id, key, company, title FROM jobs ORDER BY id`)
	if err != nil {
		return err
	}
	type row struct {
		id             int64
		key, co, title string
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.key, &r.co, &r.title); err == nil {
			all = append(all, r)
		}
	}
	rows.Close()
	taken := map[string]bool{}
	for _, r := range all {
		taken[r.key] = true
	}
	for _, r := range all {
		nk := JobKey(r.co, r.title)
		if nk == r.key || taken[nk] {
			continue
		}
		if _, err := s.DB.Exec(`UPDATE jobs SET key=? WHERE id=?`, nk, r.id); err != nil {
			return err
		}
		delete(taken, r.key)
		taken[nk] = true
	}
	return nil
}
