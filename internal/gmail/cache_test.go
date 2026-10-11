package gmail

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const quota = `{"error":{"code":403,"message":"Quota exceeded for quota metric 'Queries' and limit 'Queries per minute per user'","errors":[{"reason":"rateLimitExceeded"}]}}`

// quotaGoogle lists m1..m3; each message read fails with the quota error while busy[id] > 0.
func quotaGoogle(t *testing.T, busy map[string]int) (*Client, map[string]int, *[]time.Duration) {
	t.Helper()
	var mu sync.Mutex
	gets := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/api/messages" {
			_, _ = w.Write([]byte(`{"messages":[{"id":"m1"},{"id":"m2"},{"id":"m3"}]}`))
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/api/messages/")
		gets[id]++
		if busy[id] > 0 {
			busy[id]--
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(quota))
			return
		}
		_, _ = w.Write([]byte(`{"threadId":"t-` + id + `","snippet":"hi","internalDate":"` + ms(time.Now()) + `",
			"payload":{"headers":[{"name":"From","value":"hr@acme.test"},{"name":"Subject","value":"` + id + `"}]}}`))
	}))
	t.Cleanup(srv.Close)
	var waits []time.Duration
	old := Sleep
	Sleep = func(ctx context.Context, d time.Duration) error { waits = append(waits, d); return nil }
	t.Cleanup(func() { Sleep = old })
	c := &Client{ID: "cid", Home: t.TempDir(), HTTP: srv.Client(), APIBase: srv.URL + "/api"}
	if err := c.save(Token{AccessToken: "at", RefreshToken: "rt", Expiry: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	return c, gets, &waits
}

func ms(t time.Time) string { return strconv.FormatInt(t.UnixMilli(), 10) }

func TestFetchRetriesARateLimitAndCachesMessages(t *testing.T) {
	c, gets, waits := quotaGoogle(t, map[string]int{"m2": 2})
	got, err := c.Fetch(context.Background(), time.Now().AddDate(0, 0, -1), 10)
	if err != nil || len(got) != 3 {
		t.Fatalf("got %d, %v", len(got), err)
	}
	if gets["m2"] != 3 {
		t.Errorf("m2 read %d times, want 2 refusals and a success", gets["m2"])
	}
	backoff := 0
	for _, d := range *waits {
		if d >= 2*time.Second {
			backoff++
		}
	}
	if backoff != 2 {
		t.Errorf("waits %v, want two backoffs", *waits)
	}
	if fi, err := os.Stat(filepath.Join(c.Home, CacheFile)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("cache %v %v", fi, err)
	}
	// the second sync lists again but reads no message
	if again, err := c.Fetch(context.Background(), time.Now().AddDate(0, 0, -1), 10); err != nil || len(again) != 3 || again[1].Subject != "m2" || again[1].Text != "hi" {
		t.Fatalf("again %+v, %v", again, err)
	}
	if gets["m1"] != 1 || gets["m3"] != 1 {
		t.Errorf("cached messages read again: %v", gets)
	}
}

func TestFetchStopsOnAQuotaThatDoesNotRefillAndResumes(t *testing.T) {
	busy := map[string]int{"m2": retries + 1}
	c, gets, _ := quotaGoogle(t, busy)
	got, err := c.Fetch(context.Background(), time.Now().AddDate(0, 0, -1), 10)
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err %v, want the short rate-limit message", err)
	}
	if strings.Contains(err.Error(), "403") || len(got) != 1 {
		t.Errorf("got %d mails, err %q", len(got), err)
	}
	if gets["m3"] != 0 {
		t.Error("kept reading after the quota ran out")
	}
	// quota refilled: m1 comes from the cache, the rest are read
	got, err = c.Fetch(context.Background(), time.Now().AddDate(0, 0, -1), 10)
	if err != nil || len(got) != 3 || gets["m1"] != 1 {
		t.Fatalf("resume got %d, %v, gets %v", len(got), err, gets)
	}
}

func TestDisconnectDeletesTheCache(t *testing.T) {
	c, _, _ := quotaGoogle(t, nil)
	c.RevokeURL = c.APIBase + "/revoke"
	if _, err := c.Fetch(context.Background(), time.Now().AddDate(0, 0, -1), 10); err != nil {
		t.Fatal(err)
	}
	if err := c.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(c.Home, CacheFile)); !os.IsNotExist(err) {
		t.Errorf("cache left behind: %v", err)
	}
}

func TestRetryAfter(t *testing.T) {
	resp := func(code int, after string) *http.Response {
		r := &http.Response{StatusCode: code, Header: http.Header{}}
		if after != "" {
			r.Header.Set("Retry-After", after)
		}
		return r
	}
	for _, tc := range []struct {
		r       *http.Response
		msg     string
		attempt int
		want    time.Duration
	}{
		{resp(403, ""), quota, 0, 2 * time.Second},
		{resp(403, ""), quota, 3, 16 * time.Second},
		{resp(429, "7"), "", 0, 7 * time.Second},
		{resp(429, "600"), "", 0, time.Minute},
		{resp(503, ""), "", 1, 4 * time.Second},
		{resp(403, ""), `{"error":{"errors":[{"reason":"insufficientPermissions"}]}}`, 0, 0},
		{resp(404, ""), "", 0, 0},
	} {
		if got := retryAfter(tc.r, tc.msg, tc.attempt); got != tc.want {
			t.Errorf("%d %q attempt %d: %v, want %v", tc.r.StatusCode, tc.msg, tc.attempt, got, tc.want)
		}
	}
}
