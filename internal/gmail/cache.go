package gmail

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/mail"
)

// CacheFile keeps the metadata of inbox messages already read, so a sync fetches each message once.
// It holds what the reply matcher reads (sender, subject, date, Gmail's snippet) and is 0600 like the token.
const CacheFile = "gmail-cache.json"

// cacheDays drops entries older than any sync window, so the file stays small.
const cacheDays = 120

// ErrRateLimited is Gmail's per-user quota running out. It refills within a minute.
var ErrRateLimited = errors.New("gmail is busy for this account (rate limit); wait a minute and sync again, messages already read are kept")

// Sleep waits between retries and paced requests; tests replace it.
var Sleep = func(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Gmail allows 250 quota units per second per user and a message read costs 5: 20 reads a second at most.
// Pacing at 15 leaves room for the dashboard and an agent using the same account.
const messageGap = time.Second / 15

const retries = 4

type cached struct {
	Thread    string `json:"thread,omitempty"`
	From      string `json:"from"`
	Subject   string `json:"subject"`
	Date      string `json:"date"`
	MessageID string `json:"message_id"`
	InReplyTo string `json:"in_reply_to,omitempty"`
	Snippet   string `json:"snippet,omitempty"`
}

func (c *Client) cachePath() string { return filepath.Join(c.Home, CacheFile) }

func (c *Client) loadCache() map[string]cached {
	m := map[string]cached{}
	if b, err := os.ReadFile(c.cachePath()); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

// saveCache writes atomically and forgets messages older than cacheDays.
func (c *Client) saveCache(m map[string]cached) error {
	cut := time.Now().AddDate(0, 0, -cacheDays)
	for id, e := range m {
		if t, err := time.Parse(time.RFC3339, e.Date); err == nil && t.Before(cut) {
			delete(m, id)
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmp := c.cachePath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.cachePath())
}

func toCache(m mail.Mail) cached {
	return cached{m.Thread, m.From, m.Subject, m.Date, m.MessageID, m.InReplyTo, m.Text}
}

func (e cached) mail() mail.Mail {
	return mail.Mail{Thread: e.Thread, From: e.From, Subject: e.Subject, Date: e.Date, MessageID: e.MessageID, InReplyTo: e.InReplyTo, Text: e.Snippet}
}

// retryAfter says how long to wait before repeating a failed request, 0 when retrying cannot help:
// rate limits (429, or 403 with a rate-limit reason) and Google's transient 5xx errors.
func retryAfter(resp *http.Response, msg string, attempt int) time.Duration {
	limited := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusForbidden && rateLimited(msg)
	switch resp.StatusCode {
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		limited = true
	}
	if !limited {
		return 0
	}
	if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
		return min(time.Duration(s)*time.Second, time.Minute)
	}
	return time.Duration(2<<attempt) * time.Second // 2s, 4s, 8s, 16s
}

func rateLimited(msg string) bool {
	for _, s := range []string{"rateLimitExceeded", "userRateLimitExceeded", "RATE_LIMIT_EXCEEDED", "RESOURCE_EXHAUSTED", "Quota exceeded"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}
