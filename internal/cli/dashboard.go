package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/web"
)

// dashboard is what answers on an address: nothing, a ScoutJobs dashboard (Health set when it is
// new enough to say who it is) or some other program.
type dashboard struct {
	Listening bool
	ScoutJobs bool
	Health    *web.Health
}

func probeDashboard(addr string) dashboard {
	c, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
	if err != nil {
		return dashboard{}
	}
	c.Close()
	d := dashboard{Listening: true}
	hc := &http.Client{Timeout: time.Second}
	if res, err := hc.Get("http://" + addr + "/healthz"); err == nil {
		var h web.Health
		ok := res.StatusCode == http.StatusOK && json.NewDecoder(io.LimitReader(res.Body, 1<<16)).Decode(&h) == nil && h.App == "scoutjobs"
		res.Body.Close()
		if ok {
			d.ScoutJobs, d.Health = true, &h
			return d
		}
	}
	// dashboards before 0.3.1 have no /healthz; their pages still say who they are
	if res, err := hc.Get("http://" + addr + "/"); err == nil {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<16))
		res.Body.Close()
		d.ScoutJobs = strings.Contains(string(b), "<title>ScoutJobs")
	}
	return d
}

// portBusy explains a failed listen: which dashboard already runs there, or a free port to use.
func portBusy(addr, home string, err error) error {
	var op *net.OpError
	if !errors.As(err, &op) || op.Op != "listen" {
		return err
	}
	d := probeDashboard(addr)
	switch {
	case d.Health != nil:
		h := d.Health
		msg := fmt.Sprintf("a ScoutJobs %s dashboard already runs on http://%s (pid %d, data %s)", h.Version, addr, h.PID, h.Home)
		if h.Home != home {
			msg += fmt.Sprintf("\nthis shell uses data %s: SCOUTJOBS_HOME differs", home)
		}
		return fmt.Errorf("%s\nopen it, stop it (kill %d), or run this one elsewhere: scoutjobs serve --addr %s", msg, h.PID, freeAddr(addr))
	case d.ScoutJobs:
		return fmt.Errorf("an older ScoutJobs dashboard already runs on http://%s\nstop it (pkill -f \"scoutjobs serve\") and start again, or: scoutjobs serve --addr %s", addr, freeAddr(addr))
	case d.Listening:
		return fmt.Errorf("%s is used by another program\nuse another port: scoutjobs serve --addr %s, or set dashboard_addr in config.yaml", addr, freeAddr(addr))
	}
	return fmt.Errorf("cannot listen on %s: %w", addr, err)
}

// freeAddr suggests the next port after addr's that can be bound.
func freeAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "127.0.0.1:0"
	}
	p, _ := strconv.Atoi(port)
	for i := 1; i <= 20 && p > 0; i++ {
		a := net.JoinHostPort(host, strconv.Itoa(p+i))
		if l, err := net.Listen("tcp", a); err == nil {
			l.Close()
			return a
		}
	}
	return net.JoinHostPort(host, "0")
}

// dashboardCheck is the doctor line for the configured dashboard address.
func dashboardCheck(addr, home string) check {
	d := probeDashboard(addr)
	switch {
	case !d.Listening:
		return check{"warn", "dashboard", "nothing on " + addr, "scoutjobs serve (only needed for the browser UI, API and n8n)"}
	case !d.ScoutJobs:
		return check{"warn", "dashboard", addr + " is used by another program", "set dashboard_addr in config.yaml, e.g. " + freeAddr(addr)}
	case d.Health == nil:
		return check{"warn", "dashboard", "an old dashboard runs on " + addr + "; it does not restart on upgrades", "pkill -f \"scoutjobs serve\" && scoutjobs serve"}
	}
	h := d.Health
	detail := fmt.Sprintf("%s on %s, pid %d, data %s", h.Version, addr, h.PID, h.Home)
	switch {
	case h.Home != home:
		return check{"warn", "dashboard", detail + "; this shell uses " + home, "start both with the same SCOUTJOBS_HOME"}
	case h.Version != Version:
		return check{"warn", "dashboard", detail + "; the CLI is " + Version, "kill -HUP " + strconv.Itoa(h.PID) + " restarts it on the installed binary"}
	}
	return check{"ok", "dashboard", detail, ""}
}
