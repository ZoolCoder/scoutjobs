#!/usr/bin/env sh
# Mirrors agents/*.md as skills/<name>/SKILL.md so platforms without subagents (Codex, Cursor,
# Gemini, Copilot) get the same checklists. The body is copied verbatim; only the frontmatter
# changes (tools/model dropped, Agent Skills fields added). Re-run after editing an agent.
set -eu
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
for agent in "$ROOT"/agents/*.md; do
  base="$(basename "$agent" .md)"
  case "$base" in
    draft-reviewer) name=draft-review ;;
    fit-scorer) name=fit-score ;;
    behavioral-story-extractor) name=behavioral-stories ;;
    *-interviewer) name="${base%-interviewer}-interview" ;;
    *) name="$base" ;;
  esac
  desc="$(awk 'NR>1 && /^---$/{exit} /^description:/{sub(/^description: */,""); print}' "$agent")"
  dir="$ROOT/skills/$name"
  mkdir -p "$dir"
  {
    printf -- '---\nname: %s\ndescription: %s\nlicense: MIT\ncompatibility: Needs the scoutjobs binary. Generated from agents/%s.md by scripts/agents-to-skills.sh; edit the agent, not this file.\n---\n\n' "$name" "$desc" "$base"
    printf '# %s\n\nThis is the `%s` subagent as a skill. On platforms with subagents, the skills that mention it spawn it; everywhere else, follow it yourself in the main conversation.\n\n' "$name" "$base"
    awk 'f{print} NR>1 && /^---$/ && !f{f=1}' "$agent"
  } > "$dir/SKILL.md"
  echo "skills/$name/SKILL.md <- agents/$base.md"
done
