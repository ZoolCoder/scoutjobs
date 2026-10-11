---
description: Write or rewrite the cover letter for one job so it answers the posting, then review it
argument-hint: <job id or company>
---

Use the `cover-letter-generator` skill from this plugin for the given job. Read the saved `posting.md`, `resume.md` if it exists, `profile.md` and the stories folder. If a `cover-letter.md` already exists, rewrite it: keep what matches the posting, cut what does not, and say what changed. Then spawn the `draft-reviewer` agent on the result.

Arguments: $ARGUMENTS
