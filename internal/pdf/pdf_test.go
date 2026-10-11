package pdf

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"
)

const sample = `# Jane Doe
Berlin · jane@example.com · [github](https://github.com/jane)

## Experience
### Senior Go Engineer, Acme (2021–2024)
- Built the **billing** platform handling 2M events/day
- Cut p99 latency by 40% with a cache rewrite

## Skills
Go, Postgres, Kubernetes
`

func TestRender(t *testing.T) {
	out := Render(sample, "Jane Doe", time.Unix(0, 0))
	s := string(out)
	if !bytes.HasPrefix(out, []byte("%PDF-1.4")) || !strings.HasSuffix(s, "%%EOF\n") {
		t.Fatal("not a pdf")
	}
	for _, want := range []string{"/Helvetica-Bold", "/WinAnsiEncoding", "(Jane Doe) Tj", "(EXPERIENCE) Tj", "\\225 Built the billing platform", `github \(https://github.com/jane\)`, "2021\\226", "/Count 1"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(s, "**") {
		t.Error("markdown emphasis leaked")
	}
	// xref offsets must point at "N 0 obj"
	for _, m := range regexp.MustCompile(`(?m)^(\d{10}) 00000 n`).FindAllStringSubmatch(s, -1) {
		var off int
		for _, c := range m[1] {
			off = off*10 + int(c-'0')
		}
		if !regexp.MustCompile(`^\d+ 0 obj`).MatchString(s[off:]) {
			t.Errorf("xref offset %d does not start an object: %q", off, s[off:min(off+12, len(s))])
		}
	}
}

func TestWrapAndPages(t *testing.T) {
	long := strings.Repeat("- lorem ipsum dolor sit amet consectetur adipiscing elit sed do eiusmod tempor\n", 120)
	s := string(Render(long, "x", time.Now()))
	if !strings.Contains(s, "/Count 3") && !strings.Contains(s, "/Count 2") {
		t.Errorf("expected several pages")
	}
	lines := wrap("a b c d e f g h i j k l m n o p q r s t u v w x y z", 10.5, false, 60, "")
	if len(lines) < 3 {
		t.Errorf("no wrapping: %v", lines)
	}
	if got := plain("**bold** and _it_ and `code` [a](https://a.b)"); got != "bold and it and code a (https://a.b)" {
		t.Error(got)
	}
	if escape("(x) \\ é") != `\(x\) \\ \351` {
		t.Error(escape("(x) \\ é"))
	}
}
