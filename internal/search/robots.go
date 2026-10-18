package search

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ErrRobots marks a page the site's robots.txt asks ScoutJobs not to fetch.
var ErrRobots = errors.New("robots.txt disallows it")

// robotsTTL is how long one host's robots.txt is trusted before it is fetched again.
const robotsTTL = 12 * time.Hour

type robotsEntry struct {
	rules   []robotsRule // nil allows everything
	blocked bool         // the server failed (5xx): RFC 9309 says assume complete disallow
	at      time.Time
}

type robotsRule struct {
	allow bool
	path  string
	re    *regexp.Regexp
}

var robotsCache sync.Map // scheme://host -> robotsEntry

// robotsAllowed reports whether robots.txt on u's host lets ScoutJobs fetch u, following RFC 9309:
// the group for "scoutjobs" or else "*", longest matching rule wins, allow wins a tie.
func robotsAllowed(ctx context.Context, client *http.Client, u *url.URL) error {
	origin := u.Scheme + "://" + u.Host
	e, ok := robotsCache.Load(origin)
	if !ok || time.Since(e.(robotsEntry).at) > robotsTTL {
		fresh, err := fetchRobots(ctx, client, origin)
		if err != nil {
			return err
		}
		robotsCache.Store(origin, fresh)
		e = fresh
	}
	entry := e.(robotsEntry)
	if entry.blocked {
		return fmt.Errorf("%s: robots.txt unavailable: %w", u.Host, ErrRobots)
	}
	p := u.EscapedPath()
	if p == "" {
		p = "/"
	}
	if u.RawQuery != "" {
		p += "?" + u.RawQuery
	}
	best, allow := -1, true
	for _, r := range entry.rules {
		if !r.re.MatchString(p) {
			continue
		}
		if n := len(r.path); n > best || (n == best && r.allow) {
			best, allow = n, r.allow
		}
	}
	if !allow {
		return fmt.Errorf("%s: %w", u, ErrRobots)
	}
	return nil
}

func fetchRobots(ctx context.Context, client *http.Client, origin string) (robotsEntry, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", origin+"/robots.txt", nil)
	if err != nil {
		return robotsEntry{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	res, err := client.Do(req)
	if err != nil {
		return robotsEntry{}, err
	}
	defer res.Body.Close()
	now := time.Now()
	switch {
	case res.StatusCode >= 500:
		return robotsEntry{blocked: true, at: now}, nil
	case res.StatusCode >= 400: // no robots.txt: everything is allowed
		return robotsEntry{at: now}, nil
	}
	return robotsEntry{rules: parseRobots(io.LimitReader(res.Body, 500<<10)), at: now}, nil
}

// parseRobots keeps the rules of the group naming scoutjobs, or of the "*" group when none does.
func parseRobots(r io.Reader) []robotsRule {
	var mine, star []robotsRule
	var agents []string
	inRules := false
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line, _, _ := strings.Cut(sc.Text(), "#")
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key, val = strings.ToLower(strings.TrimSpace(key)), strings.TrimSpace(val)
		switch key {
		case "user-agent":
			if inRules { // a user-agent line after rules starts a new group
				agents, inRules = nil, false
			}
			agents = append(agents, strings.ToLower(val))
		case "allow", "disallow":
			inRules = true
			if val == "" { // "Disallow:" with no path allows everything
				continue
			}
			rule := robotsRule{allow: key == "allow", path: val, re: robotsPattern(val)}
			for _, a := range agents {
				switch {
				case a == "*":
					star = append(star, rule)
				case strings.HasPrefix(a, "scoutjobs"):
					mine = append(mine, rule)
				}
			}
		}
	}
	if mine != nil {
		return mine
	}
	return star
}

// robotsPattern turns a robots path with * and a trailing $ into an anchored expression.
func robotsPattern(p string) *regexp.Regexp {
	end := strings.HasSuffix(p, "$")
	p = strings.TrimSuffix(p, "$")
	parts := strings.Split(p, "*")
	for i := range parts {
		parts[i] = regexp.QuoteMeta(parts[i])
	}
	expr := "^" + strings.Join(parts, ".*")
	if end {
		expr += "$"
	}
	return regexp.MustCompile(expr)
}
