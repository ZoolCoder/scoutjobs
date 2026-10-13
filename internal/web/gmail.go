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
	Download  string // a client_secret_….json waiting in Downloads, by name
	Blind     bool   // Downloads cannot be read (macOS privacy), so the file must be added by hand
	Home      string
	For       string // the page Google returns to: "" for the Inbox, connections
	IDFrom    string // where the client ID comes from, "" when missing
	HasSecret bool
}

// Publish is the checklist that moves a Testing-mode app to In production.
func (g googleView) Publish() []gmail.Step { return gmail.PublishSteps }

func (v *server) googleView() googleView {
	c := v.gmailClient()
	if c == nil {
		return googleView{}
	}
	g := googleView{Offered: true, State: c.Status(), Home: v.s.Home, IDFrom: c.IDFrom, HasSecret: c.Secret != ""}
	if !g.Client {
		dir := downloadsDir()
		if _, err := os.ReadDir(dir); err != nil {
			g.Blind = true
		} else if f := gmail.FindDownload(dir, time.Time{}); f != "" {
			g.Download = filepath.Base(f)
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
	_ = json.NewEncoder(w).Encode(map[string]any{"file": g.Download, "client": g.Client, "blind": g.Blind})
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
	page := oauthFeature(r.FormValue("for")).page
	c, err := gmail.ParseCredentials(b)
	if err == nil {
		err = gmail.SaveCredentials(v.s.Home, c)
	}
	if err != nil {
		http.Redirect(w, r, withParam(page, "err", err.Error()), http.StatusSeeOther)
		return
	}
	msg := "Google client saved. Press Connect Google to finish."
	if found != "" {
		msg = "Google client imported from " + filepath.Base(found) + "; you can delete that download. Press Connect Google to finish."
	}
	http.Redirect(w, r, withParam(page, "msg", msg), http.StatusSeeOther)
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
		http.Redirect(w, r, withParam(feature.page, "err", feature.name+" was not connected: "+gmail.Classify(err).Error()), http.StatusSeeOther)
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
		f := gmail.Classify(err)
		msg := feature.name + " was not connected: " + f.Error()
		if c.Connected() {
			msg += ". Your earlier connection is unchanged."
		}
		if f.Retry {
			msg += " Press Reconnect Google to try again."
		}
		http.Redirect(w, r, withParam(feature.page, "err", msg), http.StatusSeeOther)
		return
	}
	g := googleView{Offered: true, State: c.Status()}
	if missing := g.Missing(); len(missing) > 0 {
		http.Redirect(w, r, withParam(feature.page, "err", connectedAs(t)+", but Google was not allowed "+strings.Join(missing, ", ")+
			". Press Connect Google again and tick every box on Google's screen."), http.StatusSeeOther)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if bad := failedChecks(c.Verify(ctx)); bad != "" {
		http.Redirect(w, r, withParam(feature.page, "err", connectedAs(t)+", but "+bad), http.StatusSeeOther)
		return
	}
	msg := connectedAs(t) + " and checked: Gmail and Google Calendar both answer. Replies to your applications sync into the Inbox, drafts go to Gmail, and interviews to your Google Calendar."
	if g.Testing {
		msg += " Your Google Cloud app is still in Testing mode, so Google will disconnect it on " + g.Ends.Format("Jan 2") + ": publish it, then reconnect."
	}
	http.Redirect(w, r, withParam(feature.page, "msg", msg), http.StatusSeeOther)
}

// failedChecks names each integration that failed its check, with its fix; "" when all passed.
func failedChecks(checks []gmail.Check) string {
	var bad []string
	for _, ch := range checks {
		if !ch.OK {
			bad = append(bad, ch.Name+": "+ch.Detail)
		}
	}
	return strings.Join(bad, "; ")
}

// verifyGoogle is the Connections card's Verify: one light read of each integration.
func (v *server) verifyGoogle(ctx context.Context) (bool, string) {
	c := v.gmailClient()
	if c == nil || !c.Connected() {
		return false, "Google is not connected yet."
	}
	checks := c.Verify(ctx)
	if bad := failedChecks(checks); bad != "" {
		return false, bad
	}
	var ok []string
	for _, ch := range checks {
		ok = append(ok, ch.Name+": "+ch.Detail)
	}
	return true, strings.Join(ok, "; ")
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
			http.Redirect(w, r, withParam(back(r, "/inbox"), "err", err.Error()), http.StatusSeeOther)
			return
		}
	}
	http.Redirect(w, r, withParam(back(r, "/inbox"), "msg", "Google disconnected: access revoked at Google, token deleted"), http.StatusSeeOther)
}

// oauthTarget is the feature a Google sign-in was started for: its name in messages and its page.
type oauthTarget struct{ key, name, page string }

func oauthFeature(key string) oauthTarget {
	switch key {
	case "calendar":
		return oauthTarget{"calendar", "Google", "/calendar"}
	case "connections":
		return oauthTarget{"connections", "Google", "/connections#google"}
	}
	return oauthTarget{"gmail", "Google", "/inbox"}
}
