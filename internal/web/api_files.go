package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/zoolcoder/scoutjobs/internal/apply"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

// The file endpoints let an agent that has no shell on this machine (Codex cloud, Claude Code on the web,
// an n8n step) run the CV and cover-letter flows: read the posting and profile, write the drafts, build the
// e-mail. Sending stays in the CLI behind --yes, so nothing leaves through the API.

func (v *server) apiFiles(w http.ResponseWriter, r *http.Request) {
	j, ok := v.apiJobFor(w, r)
	if !ok {
		return
	}
	events, _ := v.s.Events(j.ID, 100)
	kit, dir, files := apply.Status(v.s, j, events)
	if files == nil {
		files = []apply.File{}
	}
	writeJSON(w, 200, map[string]any{"job": j.ID, "dir": dir, "files": files, "kit": kit})
}

func (v *server) apiReadFile(w http.ResponseWriter, r *http.Request) {
	j, ok := v.apiJobFor(w, r)
	if !ok {
		return
	}
	body, name, err := apply.ReadFile(apply.Dir(v.s, j), r.PathValue("name"))
	if err != nil {
		apiErr(w, 404, fmt.Errorf("%s: not found or not a text draft", name))
		return
	}
	writeJSON(w, 200, map[string]any{"job": j.ID, "name": name, "size": len(body), "body": string(body)})
}

// apiWriteFile takes the raw text as body, or {"body": "..."} when sent as JSON.
func (v *server) apiWriteFile(w http.ResponseWriter, r *http.Request) {
	j, ok := v.apiJobFor(w, r)
	if !ok {
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, apply.MaxFile+1024))
	if err != nil {
		apiErr(w, 413, err)
		return
	}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var in struct{ Body string }
		if err := json.Unmarshal(raw, &in); err != nil {
			apiErr(w, 400, err)
			return
		}
		raw = []byte(in.Body)
	}
	dir := apply.Dir(v.s, j)
	_, existed := os.Stat(filepath.Join(dir, filepath.Base(r.PathValue("name"))))
	name, err := apply.WriteFile(dir, r.PathValue("name"), raw)
	if err != nil {
		apiErr(w, 400, err)
		return
	}
	_ = v.s.AddEvent(j.ID, "file:write", name)
	code := 201
	if existed == nil {
		code = 200
	}
	writeJSON(w, code, map[string]any{"job": j.ID, "name": name, "size": len(raw), "path": filepath.Join(dir, name)})
}

func (v *server) apiEmailDraft(w http.ResponseWriter, r *http.Request) {
	j, ok := v.apiJobFor(w, r)
	if !ok {
		return
	}
	var o apply.DraftOptions
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&o); err != nil {
			apiErr(w, 400, err)
			return
		}
	}
	m, dir, err := apply.Draft(v.s, j, v.cfg.EmailFrom, o)
	if err != nil {
		apiErr(w, 400, err)
		return
	}
	writeJSON(w, 201, map[string]any{"job": j.ID, "dir": dir, "email": m, "send": fmt.Sprintf("scoutjobs email send %d --yes", j.ID)})
}

// apiExport renders resume.md (or {"file": "x.md"}) in the job folder to PDF and returns the path.
func (v *server) apiExport(w http.ResponseWriter, r *http.Request) {
	j, ok := v.apiJobFor(w, r)
	if !ok {
		return
	}
	var in struct{ File string }
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&in); err != nil {
			apiErr(w, 400, err)
			return
		}
	}
	if in.File == "" {
		in.File = "resume.md"
	}
	dst, size, err := apply.ExportPDF(apply.Dir(v.s, j), in.File, "")
	if err != nil {
		apiErr(w, 400, err)
		return
	}
	_ = v.s.AddEvent(j.ID, "cv:export", filepath.Base(dst))
	writeJSON(w, 201, map[string]any{"job": j.ID, "file": dst, "bytes": size})
}

// apiProfile is what the CV skills read before writing anything: facts, preferences, CV files, latest check.
func (v *server) apiProfile(w http.ResponseWriter, r *http.Request) {
	prof, _ := os.ReadFile(filepath.Join(v.s.Home, "profile.md"))
	prefs, _ := os.ReadFile(filepath.Join(v.s.Home, "preferences.md"))
	cvDir := filepath.Join(v.s.Home, "cv")
	files := apply.Files(cvDir)
	if files == nil {
		files = []apply.File{}
	}
	checks, _ := v.s.CVChecks(1)
	var latest *store.CVCheck
	if len(checks) > 0 {
		latest = &checks[0]
	}
	writeJSON(w, 200, map[string]any{"profile": string(prof), "preferences": string(prefs), "cv_dir": cvDir, "cv_files": files, "latest_check": latest})
}

// apiCVCheck mirrors `scoutjobs cv log`.
func (v *server) apiCVCheck(w http.ResponseWriter, r *http.Request) {
	var c store.CVCheck
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&c); err != nil {
		apiErr(w, 400, err)
		return
	}
	if c.File == "" {
		apiErr(w, 400, fmt.Errorf("file is required"))
		return
	}
	id, err := v.s.AddCVCheck(c)
	if err != nil {
		apiErr(w, 400, err)
		return
	}
	if j, err := v.s.JobByID(c.JobID); c.JobID != 0 && err == nil {
		_ = apply.StampATS(apply.Dir(v.s, j), filepath.Base(c.File))
	}
	c.ID = id
	writeJSON(w, 201, c)
}

func (v *server) apiCVChecks(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 20
	}
	checks, err := v.s.CVChecks(limit)
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	if checks == nil {
		checks = []store.CVCheck{}
	}
	writeJSON(w, 200, checks)
}

func (v *server) apiJobFor(w http.ResponseWriter, r *http.Request) (store.Job, bool) {
	id, err := pathID(r)
	if err != nil {
		apiErr(w, 400, err)
		return store.Job{}, false
	}
	j, err := v.s.JobByID(id)
	if err != nil {
		apiErr(w, 404, fmt.Errorf("job %d not found", id))
		return store.Job{}, false
	}
	return j, true
}
