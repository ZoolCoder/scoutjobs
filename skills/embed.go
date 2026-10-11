// Package skills embeds the skill texts the job runner hands to an agent CLI, so Codex or any other
// agent follows the same instructions as the Claude plugin, from a binary installed without this repo.
package skills

import "embed"

//go:embed fit-review/SKILL.md resume-tailor/SKILL.md cover-letter-generator/SKILL.md draft-review/SKILL.md cv-check/SKILL.md
var FS embed.FS

// Text returns one skill's SKILL.md.
func Text(name string) string {
	b, _ := FS.ReadFile(name + "/SKILL.md")
	return string(b)
}
