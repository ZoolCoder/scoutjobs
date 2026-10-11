---
description: Prepare one application end to end (fit review, resume, cover letter, review, PDF, gates, e-mail draft) without sending
argument-hint: <job id or company> [steps]
---

Run `scoutjobs job run <id> --dry-run` and show the plan. Then run `scoutjobs job run <id>` (add `--steps a,b` when the user named steps), which drives a separate agent session through the AI steps and does the rest itself. Report each step's result. On a failure, quote the reason and name the fix: the skill to rerun, or the gate to resolve, then `scoutjobs job run <id>` again resumes where it stopped. Nothing is sent; offer `/scoutjobs:send <id>` only once every gate passes and the user has read the drafts.

Inside this agent you can also do the steps yourself instead: `fit-review`, `resume-tailor`, `cover-letter-generator`, `draft-review`, `scoutjobs cv export <id>`, `cv-check --job <id>`, `scoutjobs job check <id>`, `scoutjobs email draft <id>`.

Arguments: $ARGUMENTS
