package cli

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/store"
)

// webhook returns a store.Notify that POSTs every event to url as JSON. nil when url is empty.
// With a secret, every request carries X-ScoutJobs-Timestamp and X-ScoutJobs-Signature so the
// receiver can check the body came from this install (see webhookSignature).
// Failures are ignored: automation must never block the CLI.
func webhook(url, secret string) func(store.Event) {
	if url == "" {
		return nil
	}
	client := &http.Client{Timeout: 3 * time.Second}
	return func(e store.Event) {
		body, _ := json.Marshal(map[string]any{"event": e.Kind, "job_id": e.JobID, "company": e.Company, "title": e.Title, "note": e.Note, "at": e.At})
		req, err := http.NewRequest("POST", url, bytes.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "scoutjobs-webhook")
		if secret != "" {
			ts := strconv.FormatInt(time.Now().Unix(), 10)
			req.Header.Set("X-ScoutJobs-Timestamp", ts)
			req.Header.Set("X-ScoutJobs-Signature", "sha256="+webhookSignature(secret, ts, body))
		}
		if res, err := client.Do(req); err == nil {
			res.Body.Close()
		}
	}
}

// webhookSignature is HMAC-SHA256 over "<unix timestamp>.<body>", hex encoded. The timestamp
// is part of the signed input so a captured request cannot be replayed later.
func webhookSignature(secret, ts string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(ts))
	m.Write([]byte("."))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}
