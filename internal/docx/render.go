package docx

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Office is the LibreOffice binary that renders Word files, or "" when none is installed.
// SCOUTJOBS_SOFFICE names it explicitly; "off" never renders.
func Office() string {
	switch p := os.Getenv("SCOUTJOBS_SOFFICE"); p {
	case "off":
		return ""
	case "":
	default:
		return p
	}
	for _, n := range []string{"soffice", "libreoffice"} {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	for _, p := range []string{"/Applications/LibreOffice.app/Contents/MacOS/soffice"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// Render converts a Word file to a PDF in outDir and returns its path. LibreOffice runs with a
// throwaway profile, so an open LibreOffice window does not block it.
func Render(ctx context.Context, office, src, outDir string) (string, error) {
	profile, err := os.MkdirTemp("", "scoutjobs-soffice-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(profile) }()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, office, "-env:UserInstallation=file://"+filepath.ToSlash(profile), "--headless", "--norestore",
		"--convert-to", "pdf", "--outdir", outDir, src)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("render %s: %v: %s", filepath.Base(src), err, strings.TrimSpace(string(out)))
	}
	pdf := filepath.Join(outDir, strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))+".pdf")
	if _, err := os.Stat(pdf); err != nil {
		return "", fmt.Errorf("render %s: no PDF written", filepath.Base(src))
	}
	return pdf, nil
}

var (
	pageCount  = regexp.MustCompile(`/Type\s*/Pages\b[^>]*?/Count\s+(\d+)|/Count\s+(\d+)[^>]*?/Type\s*/Pages\b`)
	pageObject = regexp.MustCompile(`/Type\s*/Page\b`)
)

// Pages counts the pages of a PDF: the page tree's total, else the page objects.
func Pages(pdf []byte) int {
	n := 0
	for _, m := range pageCount.FindAllSubmatch(pdf, -1) {
		c := m[1]
		if len(c) == 0 {
			c = m[2]
		}
		if v, _ := strconv.Atoi(string(c)); v > n {
			n = v
		}
	}
	if n == 0 {
		n = len(pageObject.FindAll(pdf, -1))
	}
	return n
}

// PDFText extracts a PDF's text layer with pdftotext; ok is false when pdftotext is not installed.
func PDFText(ctx context.Context, path string) (text string, ok bool, err error) {
	bin, lookErr := exec.LookPath("pdftotext")
	if lookErr != nil {
		return "", false, nil
	}
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, "-enc", "UTF-8", path, "-")
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", true, fmt.Errorf("pdftotext %s: %w", filepath.Base(path), err)
	}
	return out.String(), true, nil
}

// Missing lists the words of want that the text does not contain, for a text-layer comparison.
func Missing(want []string, text string) []string {
	have := map[string]bool{}
	for _, w := range words(text) {
		have[w] = true
	}
	var out []string
	for _, w := range words(strings.Join(want, " ")) {
		if !have[w] {
			out = append(out, w)
		}
	}
	return out
}

func words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r <= 127
	})
}
