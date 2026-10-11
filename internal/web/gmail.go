package web

import (
	"context"
	"errors"
	"io"
	"net/http"
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

// gmailSetClient imports the OAuth client JSON pasted or uploaded on the Inbox page.
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
	c, err := gmail.ParseCredentials(b)
	if err == nil {
		err = gmail.SaveCredentials(v.s.Home, c)
	}
	if err != nil {
		http.Redirect(w, r, withParam("/inbox", "err", err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, withParam("/inbox", "msg", "Google client saved. Connect Gmail to finish."), http.StatusSeeOther)
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
	if err != nil {
		http.Redirect(w, r, withParam("/inbox", "err", err.Error()), http.StatusSeeOther)
		return
	}
	v.gmail.put(p)
	http.Redirect(w, r, authURL, http.StatusSeeOther)
}

func (v *server) gmailCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p, ok := v.gmail.take(q.Get("state"))
	c := v.gmailClient()
	if !ok || c == nil {
		http.Redirect(w, r, withParam("/inbox", "err", "this Gmail sign-in expired or was not started here; press Connect Gmail again"), http.StatusSeeOther)
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
		http.Redirect(w, r, withParam("/inbox", "err", "Gmail was not connected: "+err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, withParam("/inbox", "msg", "Gmail connected as "+t.Email+". Sync now reads replies to your applications."), http.StatusSeeOther)
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
	http.Redirect(w, r, withParam("/inbox", "msg", "Gmail disconnected: access revoked at Google, token deleted"), http.StatusSeeOther)
}
