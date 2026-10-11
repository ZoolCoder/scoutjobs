package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/zoolcoder/scoutjobs/internal/gmail"
)

// gmailFlows holds authorizations started from the Inbox page until Google redirects back.
type gmailFlows struct {
	mu      sync.Mutex
	pending map[string]gmailFlow
}

type gmailFlow struct {
	p   gmail.Pending
	exp time.Time
}

func (f *gmailFlows) put(p gmail.Pending) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pending == nil {
		f.pending = map[string]gmailFlow{}
	}
	for k, v := range f.pending {
		if time.Now().After(v.exp) {
			delete(f.pending, k)
		}
	}
	f.pending[p.State] = gmailFlow{p, time.Now().Add(10 * time.Minute)}
}

func (f *gmailFlows) take(state string) (gmail.Pending, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.pending[state]
	delete(f.pending, state)
	return v.p, ok && time.Now().Before(v.exp)
}

// sameOrigin refuses a form posted from another site: these handlers store secrets and start OAuth.
func sameOrigin(r *http.Request) error {
	if o := r.Header.Get("Origin"); o != "" && o != "http://"+r.Host && o != "https://"+r.Host {
		return errors.New("cross-origin request refused")
	}
	return nil
}

func (v *server) gmailClient() *gmail.Client {
	if v.cfg.Mail.Gmail == nil {
		return nil
	}
	return v.cfg.Mail.Gmail()
}

// downloadsDir is where the browser saves the client JSON Google offers; a variable so tests can move it.
var downloadsDir = func() string {
	h, _ := os.UserHomeDir()
	return filepath.Join(h, "Downloads")
}

// googleView is the Connect Google panel: how far setup got and what the grant allows.
type googleView struct {
	Offered bool // the dashboard can connect Google at all
	gmail.State
	Download string // a client_secret_….json waiting in Downloads, by name
	Home     string
}

func (v *server) googleView() googleView {
	c := v.gmailClient()
	if c == nil {
		return googleView{}
	}
	g := googleView{Offered: true, State: c.Status(), Home: v.s.Home}
	if !g.Client {
		g.Download = filepath.Base(gmail.FindDownload(downloadsDir(), time.Time{}))
		if g.Download == "." {
			g.Download = ""
		}
	}
	return g
}

// Missing names what the user unticked on Google's consent screen.
func (g googleView) Missing() []string {
	if !g.Connected {
		return nil
	}
	var out []string
	if !g.Mail {
		out = append(out, "reading mail")
	}
	if !g.Drafts {
		out = append(out, "creating drafts")
	}
	if !g.CalendarWrite {
		out = append(out, "your calendar")
	}
	return out
}

// gmailDetect tells the setup page when Google's client download has landed, so it can offer to import it.
func (v *server) gmailDetect(w http.ResponseWriter, r *http.Request) {
	g := v.googleView()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"file": g.Download, "client": g.Client})
}

// gmailSetClient imports the OAuth client JSON pasted or uploaded on the Inbox page, or the one
// found in Downloads when the form asks for it.
func (v *server) gmailSetClient(w http.ResponseWriter, r *http.Request) {
	if err := sameOrigin(r); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	b := []byte(strings.TrimSpace(r.FormValue("client")))
	if f, _, err := r.FormFile("file"); err == nil {
		b, _ = io.ReadAll(io.LimitReader(f, 64<<10))
		f.Close()
	}
	var found string
	if r.FormValue("detected") != "" {
		if found = gmail.FindDownload(downloadsDir(), time.Time{}); found != "" {
			b, _ = readSmall(found)
		}
	}
	c, err := gmail.ParseCredentials(b)
	if err == nil {
		err = gmail.SaveCredentials(v.s.Home, c)
	}
	if err != nil {
		http.Redirect(w, r, withParam("/inbox", "err", err.Error()), http.StatusSeeOther)
		return
	}
	msg := "Google client saved. Press Connect Google to finish."
	if found != "" {
		msg = "Google client imported from " + filepath.Base(found) + "; you can delete that download. Press Connect Google to finish."
	}
	http.Redirect(w, r, withParam("/inbox", "msg", msg), http.StatusSeeOther)
}

func readSmall(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, 64<<10))
}

// gmailConnect sends the browser to Google's consent page; Google returns it to gmailCallback.
func (v *server) gmailConnect(w http.ResponseWriter, r *http.Request) {
	if err := sameOrigin(r); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	c := v.gmailClient()
	err := gmail.LoopbackHost(r.Host)
	var authURL string
	var p gmail.Pending
	if err == nil && c == nil {
		err = errors.New("gmail is not available in this dashboard")
	}
	if err == nil {
		authURL, p, err = c.Begin("http://" + r.Host + "/gmail/callback")
	}
	feature := oauthFeature(r.FormValue("for"))
	if err != nil {
		http.Redirect(w, r, withParam(feature.page, "err", feature.name+" was not connected: "+err.Error()), http.StatusSeeOther)
		return
	}
	p.Feature = feature.key
	v.gmail.put(p)
	http.Redirect(w, r, authURL, http.StatusSeeOther)
}

func (v *server) gmailCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p, ok := v.gmail.take(q.Get("state"))
	c := v.gmailClient()
	feature := oauthFeature(p.Feature)
	if !ok || c == nil {
		http.Redirect(w, r, withParam("/inbox", "err", "this Google sign-in expired or was not started here; press Connect again"), http.StatusSeeOther)
		return
	}
	code, err := p.Code(q)
	var t gmail.Token
	if err == nil {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		t, err = c.Finish(ctx, p, code)
	}
	if err != nil {
		http.Redirect(w, r, withParam(feature.page, "err", feature.name+" was not connected: "+err.Error()), http.StatusSeeOther)
		return
	}
	g := googleView{Offered: true, State: c.Status()}
	if missing := g.Missing(); len(missing) > 0 {
		http.Redirect(w, r, withParam(feature.page, "err", connectedAs(t)+", but Google was not allowed "+strings.Join(missing, ", ")+
			". Press Connect Google again and tick every box on Google's screen."), http.StatusSeeOther)
		return
	}
	msg := connectedAs(t) + ": replies to your applications sync into the Inbox, drafts go to Gmail, and interviews to your Google Calendar."
	if g.Testing {
		msg += " Your Google Cloud app is still in Testing mode, so Google will disconnect it on " + g.Ends.Format("Jan 2") + ": publish it to stay connected."
	}
	http.Redirect(w, r, withParam(feature.page, "msg", msg), http.StatusSeeOther)
}

func connectedAs(t gmail.Token) string {
	if t.Email == "" {
		return "Google connected"
	}
	return "Google connected as " + t.Email
}

func (v *server) gmailDisconnect(w http.ResponseWriter, r *http.Request) {
	if err := sameOrigin(r); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	if c := v.gmailClient(); c != nil {
		if err := c.Disconnect(r.Context()); err != nil {
			http.Redirect(w, r, withParam("/inbox", "err", err.Error()), http.StatusSeeOther)
			return
		}
	}
	http.Redirect(w, r, withParam("/inbox", "msg", "Google disconnected: access revoked at Google, token deleted"), http.StatusSeeOther)
}

// oauthTarget is the feature a Google sign-in was started for: its name in messages and its page.
type oauthTarget struct{ key, name, page string }

func oauthFeature(key string) oauthTarget {
	if key == "calendar" {
		return oauthTarget{"calendar", "Google", "/calendar"}
	}
	return oauthTarget{"gmail", "Google", "/inbox"}
}
