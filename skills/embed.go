// Package skills embeds the skill texts the job runner hands to an agent CLI, so Codex or any other
// agent follows the same instructions as the Claude plugin, from a binary installed without this repo.
package skills

import (
	"embed"
	"strings"
)

//go:embed fit-review/SKILL.md resume-tailor/SKILL.md cover-letter-generator/SKILL.md draft-review/SKILL.md cv-check/SKILL.md
//go:embed resume-ats-optimizer/SKILL.md interview-prep/SKILL.md mock-interview/SKILL.md salary-negotiation-prep/SKILL.md
var FS embed.FS

// Text returns one skill's SKILL.md.
func Text(name string) string {
	b, _ := FS.ReadFile(name + "/SKILL.md")
	return string(b)
}

// chatOnly are the sections that serve a conversation with the user, not an unattended run: when to
// pick the skill, what it can do, and the report it prints in chat instead of writing files.
var chatOnly = map[string]bool{"When to Use This Skill": true, "Core Capabilities": true, "Output Format": true,
	"Analysis Output Format": true, "Tailoring Templates": true, "Version Management": true, "Success Metrics": true}

// Unattended is the skill as an unattended run reads it: no frontmatter and no chat-only sections,
// so every preparation prompt carries only instructions it can follow.
func Unattended(name string) string {
	text := Text(name)
	if rest, ok := strings.CutPrefix(text, "---\n"); ok {
		if _, body, ok := strings.Cut(rest, "\n---\n"); ok {
			text = body
		}
	}
	var out []string
	skip, fence := false, false
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "```") {
			fence = !fence
		} else if !fence && strings.HasPrefix(line, "## ") {
			skip = chatOnly[strings.TrimSpace(line[3:])]
		} else if !fence && strings.HasPrefix(line, "# ") {
			skip = false
		}
		if !skip {
			out = append(out, line)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n")) + "\n"
}
