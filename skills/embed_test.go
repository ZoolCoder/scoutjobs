package skills

import (
	"strings"
	"testing"
)

func TestUnattendedDropsChatOnlySections(t *testing.T) {
	for _, name := range []string{"fit-review", "resume-tailor", "cover-letter-generator", "draft-review", "cv-check",
		"resume-ats-optimizer", "interview-prep", "mock-interview", "salary-negotiation-prep"} {
		full, lean := Text(name), Unattended(name)
		if full == "" {
			t.Errorf("%s is not embedded", name)
			continue
		}
		if strings.HasPrefix(lean, "---") || strings.Contains(lean, "## When to Use This Skill") || strings.Contains(lean, "\n## Output Format") {
			t.Errorf("%s keeps chat-only text", name)
		}
		if strings.Contains(full, "## ScoutJobs integration") && !strings.Contains(lean, "## ScoutJobs integration") {
			t.Errorf("%s lost the section after a chat-only one", name)
		}
		if strings.Count(lean, "```")%2 != 0 {
			t.Errorf("%s has an unclosed code fence", name)
		}
	}
	if n, m := len(Text("resume-tailor")), len(Unattended("resume-tailor")); m > n*3/4 {
		t.Errorf("resume-tailor only shrank from %d to %d bytes", n, m)
	}
}
