package web

import (
	"html"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/zoolcoder/scoutjobs/internal/apply"
)

// filePage is the preview and editor of one document, for job folders and the cv folder alike.
type filePage struct {
	Name, Path, Back, BackLabel string
	Kind                        string // md | text | pdf | other
	Body                        string
	HTML                        template.HTML // rendered Markdown
	Size                        int64
	Mod                         string
	Editable, Edit, Saved       bool
	SaveURL, ViewURL, Download  string
}

func previewKind(name string) string {
	switch filepath.Ext(name) {
	case ".md":
		return "md"
	case ".pdf":
		return "pdf"
	case ".txt", ".json", ".eml":
		return "text"
	}
	return "other"
}

// loadPage reads a document for the preview page; ok is false when it is missing or not a document.
func loadPage(dir, name string) (filePage, bool) {
	name = filepath.Base(name)
	p := filePage{Name: name, Path: filepath.Join(dir, name), Kind: previewKind(name)}
	fi, err := os.Stat(p.Path)
	if err != nil || fi.IsDir() || (p.Kind == "other" && !downloadable[filepath.Ext(name)]) {
		return p, false
	}
	p.Size, p.Mod = fi.Size(), fi.ModTime().Format("2006-01-02 15:04")
	if p.Kind == "md" || p.Kind == "text" {
		b, err := os.ReadFile(p.Path)
		if err != nil {
			return p, false
		}
		p.Body = string(b)
		if p.Kind == "md" {
			p.HTML = mdHTML(p.Body)
		}
	}
	return p, true
}

// inline serves a PDF for the preview frame. The dashboard CSP is dropped for this response only:
// the browser's PDF viewer does not run under it, and the file is the user's own export.
func inline(w http.ResponseWriter, r *http.Request, dir, name string) {
	name = filepath.Base(name)
	if filepath.Ext(name) != ".pdf" {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(filepath.Join(dir, name))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Del("Content-Security-Policy")
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", "inline")
	http.ServeContent(w, r, name, fi.ModTime(), f)
}

func (v *server) jobView(w http.ResponseWriter, r *http.Request) {
	if j, ok := v.formJob(w, r); ok {
		inline(w, r, apply.Dir(v.s, j), r.PathValue("name"))
	}
}

func (v *server) cvView(w http.ResponseWriter, r *http.Request) {
	inline(w, r, filepath.Join(v.s.Home, "cv"), r.PathValue("name"))
}

// cvFile previews a file of the cv folder; the main CV and cover letter can be edited here too.
func (v *server) cvFile(w http.ResponseWriter, r *http.Request) {
	dir := filepath.Join(v.s.Home, "cv")
	p, ok := loadPage(dir, r.PathValue("name"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	p.Back, p.BackLabel = "/profile", "Profile"
	p.Editable = p.Name == "resume.md" || p.Name == "cover-letter.md"
	p.Edit = p.Editable && r.URL.Query().Get("edit") != ""
	p.Saved = r.URL.Query().Get("saved") != ""
	p.SaveURL, p.ViewURL, p.Download = "/profile/files/"+p.Name, "/cv/view/"+p.Name, "/cv/"+p.Name
	v.render(w, r, "file.html", "profile", map[string]any{"F": p})
}

func (v *server) saveCVFile(w http.ResponseWriter, r *http.Request) {
	if err := sameOrigin(r); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	name := filepath.Base(r.PathValue("name"))
	if name != "resume.md" && name != "cover-letter.md" {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, apply.MaxFile+4096)
	body := strings.ReplaceAll(r.FormValue("body"), "\r\n", "\n")
	if _, err := apply.WriteFile(filepath.Join(v.s.Home, "cv"), name, []byte(body)); err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, "/profile/files/"+name+"?saved=1", http.StatusSeeOther)
}

var (
	mdBold   = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	mdItalic = regexp.MustCompile(`(^|[^*\w])[*_]([^*_]+)[*_]`)
	mdCode   = regexp.MustCompile("`([^`]+)`")
	mdLink   = regexp.MustCompile(`\[([^\]]+)\]\((https?://[^)\s]+)\)`)
)

// mdHTML renders the Markdown subset the drafts use: headings, bullets, paragraphs, emphasis,
// code and http links. Every line is escaped first, so the result carries no markup from the file.
func mdHTML(src string) template.HTML {
	var b strings.Builder
	inList, para := false, []string{}
	flush := func() {
		if len(para) > 0 {
			b.WriteString("<p>" + strings.Join(para, "<br>") + "</p>\n")
			para = para[:0]
		}
		if inList {
			b.WriteString("</ul>\n")
			inList = false
		}
	}
	for _, raw := range strings.Split(src, "\n") {
		line := strings.TrimRight(raw, " \t")
		t := strings.TrimSpace(line)
		switch {
		case t == "":
			flush()
		case strings.HasPrefix(t, "#"):
			flush()
			n := len(t) - len(strings.TrimLeft(t, "#"))
			if n > 4 {
				n = 4
			}
			lvl := string(rune('0' + n))
			b.WriteString("<h" + lvl + ">" + inlineMD(strings.TrimSpace(t[n:])) + "</h" + lvl + ">\n")
		case t == "---" || t == "***":
			flush()
			b.WriteString("<hr>\n")
		case strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ") || strings.HasPrefix(t, "+ "):
			if len(para) > 0 {
				b.WriteString("<p>" + strings.Join(para, "<br>") + "</p>\n")
				para = para[:0]
			}
			if !inList {
				b.WriteString("<ul>\n")
				inList = true
			}
			b.WriteString("<li>" + inlineMD(t[2:]) + "</li>\n")
		default:
			if inList {
				b.WriteString("</ul>\n")
				inList = false
			}
			para = append(para, inlineMD(t))
		}
	}
	flush()
	return template.HTML(b.String()) //nolint:gosec // built from escaped text only
}

func inlineMD(s string) string {
	s = html.EscapeString(s)
	s = mdLink.ReplaceAllString(s, `<a href="$2" rel="noopener noreferrer" target="_blank">$1</a>`)
	s = mdCode.ReplaceAllString(s, "<code>$1</code>")
	s = mdBold.ReplaceAllString(s, "<b>$1</b>")
	return mdItalic.ReplaceAllString(s, "$1<i>$2</i>")
}
