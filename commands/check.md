---
description: Run the application quality gates for one job and report every unresolved failure
argument-hint: <job id or company>
---

Run `scoutjobs job check <id>` for the given job and show the result gate by gate. For each `FAIL`, name the skill that fixes it (`fit-review`, `resume-tailor`, `cover-letter-generator`, `draft-review`) and offer to run it. Never propose `--skip-checks` yourself; only the user decides that.

Arguments: $ARGUMENTS
