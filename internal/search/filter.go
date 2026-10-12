package search

import (
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Filter narrows what the boards return; it is stored with a saved search as url-encoded values.
type Filter struct {
	RemoteOnly   bool
	Exclude      string // comma separated words; a title containing any of them is dropped
	Days         int    // posted within the last N days; undated postings stay
	MinSalary    int64  // drops postings whose top of range is below it; postings without a salary stay
	Location     string // comma separated cities, countries or regions; see LocationMatch
	HideMismatch bool   // drops postings that break a must in the profile: place, engagement, salary floor
}

func FilterFromValues(q url.Values) Filter {
	f := Filter{RemoteOnly: q.Get("remote_only") != "", Exclude: strings.TrimSpace(q.Get("exclude")),
		Location: strings.TrimSpace(q.Get("location")), HideMismatch: q.Get("hide_mismatch") != ""}
	f.Days, _ = strconv.Atoi(q.Get("days"))
	f.MinSalary, _ = strconv.ParseInt(q.Get("min_salary"), 10, 64)
	return f
}

// ParseFilter reads a filter stored with a saved search.
func ParseFilter(s string) Filter {
	q, _ := url.ParseQuery(s)
	return FilterFromValues(q)
}

func (f Filter) Encode() string {
	q := url.Values{}
	if f.RemoteOnly {
		q.Set("remote_only", "1")
	}
	if f.Exclude != "" {
		q.Set("exclude", f.Exclude)
	}
	if f.Days > 0 {
		q.Set("days", strconv.Itoa(f.Days))
	}
	if f.MinSalary > 0 {
		q.Set("min_salary", strconv.FormatInt(f.MinSalary, 10))
	}
	if f.Location != "" {
		q.Set("location", f.Location)
	}
	if f.HideMismatch {
		q.Set("hide_mismatch", "1")
	}
	return q.Encode()
}

func (f Filter) Keep(p Posting, now time.Time) bool {
	if f.RemoteOnly && !strings.Contains(strings.ToLower(p.Remote), "remote") {
		return false
	}
	title := strings.ToLower(p.Title)
	for _, w := range SplitList(f.Exclude) {
		if strings.Contains(title, strings.ToLower(w)) {
			return false
		}
	}
	if f.Days > 0 && len(p.PostedAt) >= 10 && p.PostedAt[:10] < now.AddDate(0, 0, -f.Days).Format("2006-01-02") {
		return false
	}
	if ok, _ := LocationMatch(p, SplitList(f.Location)); !ok {
		return false
	}
	return f.MinSalary <= 0 || p.SalaryMax == 0 || p.SalaryMax >= f.MinSalary
}

// SplitList splits a comma or newline separated list and drops the blanks.
func SplitList(s string) []string {
	var out []string
	for _, x := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' }) {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}
