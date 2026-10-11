package cli

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zoolcoder/scoutjobs/internal/web"
)

func fakeDashboard(t *testing.T, h *web.Health) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/healthz" && h != nil:
			_ = json.NewEncoder(w).Encode(h)
		case r.URL.Path == "/":
			_, _ = w.Write([]byte("<title>ScoutJobs · overview</title>"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

func TestDashboardCheck(t *testing.T) {
	defer func(v string) { Version = v }(Version)
	Version = "0.3.2"
	health := func(v, home string) *web.Health {
		return &web.Health{App: "scoutjobs", Version: v, PID: 42, Home: home}
	}
	other := httptest.NewServer(http.NotFoundHandler())
	defer other.Close()
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	free := l.Addr().String()
	l.Close()

	for _, c := range []struct {
		name, addr, level, want string
	}{
		{"nothing", free, "warn", "nothing on"},
		{"other program", strings.TrimPrefix(other.URL, "http://"), "warn", "another program"},
		{"pre-0.3.1", fakeDashboard(t, nil), "warn", "does not restart"},
		{"same", fakeDashboard(t, health("0.3.2", "/data")), "ok", "pid 42, data /data"},
		{"older", fakeDashboard(t, health("0.3.1", "/data")), "warn", "the CLI is 0.3.2"},
		{"other data", fakeDashboard(t, health("0.3.2", "/elsewhere")), "warn", "this shell uses /data"},
	} {
		got := dashboardCheck(c.addr, "/data")
		if got.Level != c.level || !strings.Contains(got.Detail, c.want) {
			t.Errorf("%s: %+v", c.name, got)
		}
	}
}

func TestPortBusyNamesTheDashboard(t *testing.T) {
	addr := fakeDashboard(t, &web.Health{App: "scoutjobs", Version: "0.3.1", PID: 7, Home: "/other"})
	_, err := net.Listen("tcp", addr)
	if err == nil {
		t.Fatal("port should be busy")
	}
	msg := portBusy(addr, "/data", err).Error()
	for _, want := range []string{"ScoutJobs 0.3.1 dashboard already runs", "pid 7", "SCOUTJOBS_HOME differs", "kill 7", "--addr 127.0.0.1:"} {
		if !strings.Contains(msg, want) {
			t.Errorf("lacks %q:\n%s", want, msg)
		}
	}
}
