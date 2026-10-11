---
name: learn-preferences
description: Turns the user's logged decisions (skips, applies, withdrawals, declined offers, score overrides) into learned preferences with confidence levels, confirms them with the user, and writes them so job-fit and tailoring skills use them. Use weekly, after a batch of decisions, or when the user says "what have you learned about me", "update my preferences", "why do you keep showing me X".
license: MIT
---

# Learn preferences

This is the feedback loop that makes ScoutJobs get better the longer it is used. It is conservative: it proposes, the user confirms, and nothing stated by the user is ever overwritten by inference.

## Input

```bash
scoutjobs decision summary --json
scoutjobs pref list --json
```

The summary holds: counts per decision kind, `signals_by_kind` (how often each `key=value` appeared in skip vs apply vs withdraw decisions), `recent_reasons` in the user's words, `interview_outcomes`, and `score_overrides` (jobs where the fit score and the user's choice disagreed).

## Infer

For each signal key, compare its distribution across kinds. A candidate preference exists when:
- the same value appears in at least 3 decisions of one kind and rarely in the opposite kind (for example `remote=onsite` in 4 skips, 0 applies → `remote: not onsite`), or
- the user's reasons repeat a theme (read `recent_reasons`; cluster by meaning, not exact words), or
- `score_overrides` show a consistent direction (user skips high-scoring jobs with a shared trait → that trait is a negative the scorer misses; user applies to low-scoring ones → a positive it misses).

Confidence: 0.5 for 3 supporting decisions, 0.7 for 5, 0.9 for 8 or more, minus 0.2 if there is contradicting evidence. Never above 0.95 for learned items.

Do not infer protected or irrelevant attributes (anything about people at the company, nationality, age, gender). Preferences are about the job: stack, remote, location, seniority, company size, industry, salary, on-call, process, role family, language.

## Propose

Queue every candidate for review, with the numbers behind it. Skip a key the user stated (`pref list` shows `stated`); the CLI refuses it anyway.

```bash
scoutjobs pref propose stack_avoid "java, spring" --support 5 --against 0 --confidence 0.7 --evidence "5 skips mention Java; 0 applies"
scoutjobs pref propose company_size "50-500" --support 3 --confidence 0.5 --evidence "3 withdrawals from 1000+ companies: 'too much process'"
```

`--evidence` holds the counts and at most two short quotes from the user's reasons. A value the user rejected before is refused unless `--support` is higher than it was then; check `scoutjobs pref review --all --json` first.

## Confirm

Present a short table: key, proposed value, current value if any, confidence, evidence. Ask the user to accept, edit, or reject each. Full sentences here; this is a question. The user can also answer later on the dashboard's Preferences page, under "Awaiting your review".

Apply only what the user said:

```bash
scoutjobs pref confirm stack_avoid                          # accept: stored as learned
scoutjobs pref confirm company_size --value "under 300"      # their own words: stored as stated, confidence 1
scoutjobs pref reject stack_avoid --reason "skipped those for salary"
```

Unanswered proposals stay pending. Every confirm, edit and reject is in the Activity log, and the CLI re-renders `preferences.md`. Finish with `scoutjobs session log --kind review --topic "learn-preferences" --notes "<n> accepted, <m> rejected, <k> pending"`.

## Also tune the scorer

If `score_overrides` is non-empty, tell the user which dimension of the `job-fit` weighting seems off for them and propose a one-line note to add to `preferences.md` under a `scoring` key, for example `scoutjobs pref set scoring "weight stated remote preference as a hard rule; ignore company size" --source stated`. `job-fit` reads that key.
