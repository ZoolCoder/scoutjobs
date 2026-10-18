package search

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"
)

// Board references come in two shapes: "greenhouse:stripe" or the careers URL a user copies
// from the browser. ParseBoard turns the second into the first, so config, the CLI and the
// dashboard all accept either. A posting URL on a known ATS is turned into one tracked job
// by FetchPosting.

var boardHosts = []struct {
	source string
	hosts  []string // exact hosts; the slug is the first path segment
}{
	{"greenhouse", []string{"boards.greenhouse.io", "job-boards.greenhouse.io", "boards.eu.greenhouse.io", "job-boards.eu.greenhouse.io"}},
	{"lever", []string{"jobs.lever.co", "jobs.eu.lever.co"}},
	{"ashby", []string{"jobs.ashbyhq.com"}},
	{"workable", []string{"apply.workable.com"}},
	{"smartrecruiters", []string{"jobs.smartrecruiters.com", "careers.smartrecruiters.com"}},
}

// subdomain ATSes: the slug is the first label of the host
var boardSubdomains = []struct{ source, suffix string }{
	{"personio", ".jobs.personio.de"}, {"personio", ".jobs.personio.com"},
	{"recruitee", ".recruitee.com"}, {"bamboohr", ".bamboohr.com"}, {"workable", ".workable.com"},
}

// closedBoards have no public API and forbid scraping; the user pastes the posting text instead.
var closedBoards = map[string]string{"linkedin.com": "LinkedIn", "indeed.com": "Indeed", "glassdoor.com": "Glassdoor", "xing.com": "XING", "stepstone.de": "StepStone", "monster.com": "Monster"}

var feedPathRe = regexp.MustCompile(`(?i)(\.(rss|xml|atom)$|/feed/?$|/rss/?$)`)
var feedQueryRe = regexp.MustCompile(`(?i)(^|&)(format|feed)=(rss|atom|rss2)`)

// ParseBoard normalizes "source:slug" or a careers URL to "source:slug". A feed URL becomes "rss:<url>".
func ParseBoard(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", errors.New("empty board")
	}
	if src, slug, ok := strings.Cut(s, ":"); ok && !strings.HasPrefix(slug, "//") {
		if slug == "" {
			return "", fmt.Errorf("%q: want source:slug or a careers URL", s)
		}
		for _, b := range Boards {
			if b == src {
				return s, nil
			}
		}
		return "", fmt.Errorf("%q: unknown source %q (one of %s)", s, src, strings.Join(Boards, ", "))
	}
	if !strings.Contains(s, "://") {
		return "", fmt.Errorf("%q: want source:slug or a careers URL", s)
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("%q: not a URL", s)
	}
	if src, slug, _, ok := locate(u); ok && slug != "" {
		if src == "rss" {
			return "rss:" + s, nil
		}
		return src + ":" + slug, nil
	}
	if feedPathRe.MatchString(u.Path) || feedQueryRe.MatchString(u.RawQuery) {
		return "rss:" + s, nil
	}
	if name := closedBoard(u.Host); name != "" {
		return "", fmt.Errorf("%s has no public job API: paste the posting text, or use the company's own careers page", name)
	}
	return "", fmt.Errorf("%q: no known ATS on %s; give source:slug (%s) or an RSS feed URL", s, u.Host, strings.Join(Boards, ", "))
}

// NormalizeBoards parses every entry and keeps going past bad ones.
func NormalizeBoards(in []string) (boards []string, errs []string) {
	seen := map[string]bool{}
	for _, b := range in {
		n, err := ParseBoard(b)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		if !seen[n] {
			seen[n] = true
			boards = append(boards, n)
		}
	}
	return boards, errs
}

func closedBoard(host string) string {
	host = strings.ToLower(host)
	for dom, name := range closedBoards {
		if host == dom || strings.HasSuffix(host, "."+dom) {
			return name
		}
	}
	return ""
}

// locate maps a URL to (source, slug, posting id). The id is empty for a board URL.
func locate(u *url.URL) (source, slug, id string, ok bool) {
	host := strings.ToLower(u.Hostname())
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	seg := func(i int) string {
		if i < len(parts) {
			return parts[i]
		}
		return ""
	}
	for _, b := range boardHosts {
		for _, h := range b.hosts {
			if host != h {
				continue
			}
			slug = seg(0)
			switch b.source {
			case "greenhouse":
				if seg(1) == "jobs" {
					id = seg(2)
				}
				if slug == "embed" { // boards.greenhouse.io/embed/job_app?for=slug&token=id
					slug, id = u.Query().Get("for"), u.Query().Get("token")
				}
			case "lever", "ashby":
				id = seg(1)
			case "workable":
				if seg(1) == "j" {
					id = seg(2)
				}
			case "smartrecruiters":
				id, _, _ = strings.Cut(seg(1), "-")
			}
			return b.source, slug, id, slug != ""
		}
	}
	for _, b := range boardSubdomains {
		if !strings.HasSuffix(host, b.suffix) {
			continue
		}
		slug = strings.TrimSuffix(host, b.suffix)
		if slug == "" || slug == "www" || strings.Contains(slug, ".") {
			continue
		}
		switch b.source {
		case "personio":
			if seg(0) == "job" {
				id = seg(1)
			}
		case "recruitee":
			if seg(0) == "o" {
				id = seg(1)
			}
		case "bamboohr":
			if seg(0) == "careers" {
				id = seg(1)
			}
			if seg(1) == "view.php" {
				id = u.Query().Get("id")
			}
		case "workable":
			if seg(0) == "jobs" || seg(0) == "j" {
				id = seg(1)
			}
		}
		return b.source, slug, id, true
	}
	return "", "", "", false
}

// PostingLive reports whether a posting is still served: through the ATS API when its host has one,
// otherwise by the page's HTTP status. A closed posting gives an error wrapping ErrGone.
func PostingLive(ctx context.Context, rawURL string, client *http.Client, base map[string]string) error {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return fmt.Errorf("%q: not a URL", rawURL)
	}
	if _, _, id, ok := locate(u); (ok && id != "") || strings.TrimPrefix(u.Hostname(), "www.") == "remotive.com" {
		_, err := FetchPosting(ctx, rawURL, client, base)
		return err
	}
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	if err := robotsAllowed(ctx, client, u); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return nil
	}
	return httpError(u.String(), res.StatusCode)
}

// FetchPosting fetches one posting by its public URL on a supported ATS.
func FetchPosting(ctx context.Context, rawURL string, client *http.Client, base map[string]string) (Posting, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return Posting{}, fmt.Errorf("%q: not a URL", rawURL)
	}
	f := fetcher{client: client, base: base}
	if f.client == nil {
		f.client = &http.Client{Timeout: 20 * time.Second}
	}
	if f.base == nil {
		f.base = map[string]string{}
	}
	if strings.TrimPrefix(u.Hostname(), "www.") == "remotive.com" {
		return f.remotiveJob(ctx, u, rawURL)
	}
	src, slug, id, ok := locate(u)
	if !ok {
		if name := closedBoard(u.Host); name != "" {
			return Posting{}, fmt.Errorf("%s has no public job API: add the job with --company and --title and paste the posting into --notes", name)
		}
		return Posting{}, fmt.Errorf("no known ATS on %s: add the job with --company and --title", u.Host)
	}
	if id == "" {
		return Posting{}, fmt.Errorf("%s is a board, not a posting: search it with --board %s:%s", u.Host, src, slug)
	}
	var p Posting
	switch src {
	case "greenhouse":
		p, err = f.greenhouseJob(ctx, slug, id)
	case "lever":
		p, err = f.leverJob(ctx, slug, id)
	case "workable":
		p, err = f.workableJob(ctx, slug, id)
	case "smartrecruiters":
		p, err = f.smartrecruitersJob(ctx, slug, id)
	case "bamboohr":
		p, err = f.bamboohrJob(ctx, slug, id)
	default: // list sources: fetch the board and pick the posting whose URL or id matches
		var list []Posting
		switch src {
		case "ashby":
			list, err = f.ashby(ctx, slug)
		case "personio":
			list, err = f.personio(ctx, slug)
		case "recruitee":
			list, err = f.recruitee(ctx, slug)
		}
		if err == nil {
			p, err = pick(list, id)
		}
	}
	if err != nil {
		return Posting{}, err
	}
	if p.URL == "" {
		p.URL = rawURL
	}
	Enrich(&p)
	return p, nil
}

// remotiveJob finds a Remotive posting through its search API: the URL's last segment is the
// title's words and the posting id (/remote-jobs/<category>/<title-words>-<id>).
func (f fetcher) remotiveJob(ctx context.Context, u *url.URL, rawURL string) (Posting, error) {
	last := path.Base(strings.TrimSuffix(u.Path, "/"))
	i := strings.LastIndex(last, "-")
	if i < 0 || strings.Trim(last[i+1:], "0123456789") != "" {
		return Posting{}, fmt.Errorf("%s is not a Remotive posting URL", rawURL)
	}
	list, err := f.remotive(ctx, strings.ReplaceAll(last[:i], "-", " "))
	if err != nil {
		return Posting{}, err
	}
	p, err := pick(list, last[i:])
	if err == nil {
		Enrich(&p)
	}
	return p, err
}

func pick(list []Posting, id string) (Posting, error) {
	for _, p := range list {
		if strings.Contains(p.URL, "/"+id) || strings.HasSuffix(p.URL, id) {
			return p, nil
		}
	}
	return Posting{}, fmt.Errorf("posting %s not found on the board (closed, or not published): %w", id, ErrGone)
}

// Excerpt cuts text to n runes on a word boundary, for notes fields.
func Excerpt(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	cut := string(r[:n])
	if i := strings.LastIndexAny(cut, " \n"); i > n/2 {
		cut = cut[:i]
	}
	return cut + " …"
}
