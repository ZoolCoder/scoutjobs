---
description: Rewrite the tailored resume for one job against its posting, keeping every claim traceable to profile.md
argument-hint: <job id or company>
---

Use the `resume-tailor` skill from this plugin for the given job only (no cover letter). If `resume.md` already exists in the application folder, rewrite it: keep what the posting asks for, cut what it does not, add missing keywords only when `profile.md` backs them, and list what changed. Then spawn the `draft-reviewer` agent on the result.

Arguments: $ARGUMENTS
