---
name: job-fit
description: Scores tracked job postings 0-100 against the user's profile and preferences, explains the score, and records it with `scoutjobs job set --score`. Suggests apply or skip, logs the user's decision so the learner can improve. Use when the user says "score these", "rank jobs", "is this job a fit", or after job-search adds postings.
license: MIT
---

# Job fit

Input: jobs with status `new` (`scoutjobs job list --status new --json --compact`, then `scoutjobs job show <id> --json` per job), `profile.md`, `preferences.md`, and each job's `posting.md` (path from `scoutjobs job show <id> --json`, field `dir`). Output: a score, a short rationale, a status change to `ranked`, and a logged decision for anything the user skips.

## Score (0-100)

| Dimension | Weight | Read from |
|---|---|---|
| Hard skills match (must-haves present in profile Strong/Working) | 35 | posting vs profile Skills |
| Seniority and scope match | 15 | title, years asked, profile Target |
| Stated preferences (remote, location, salary floor, avoid list) | 25 | preferences.md source=stated |
| Learned preferences | 15 | preferences.md source=learned, weighted by confidence |
| Growth and interest (stack_love, learning goals, industries) | 10 | profile Learning, prefs |

Hard rules, applied before scoring:
- A stated preference with confidence 1 violated outright (for example onsite-only when `remote: remote only`): cap at 30 and say which rule.
- Missing work authorization with no sponsorship mentioned: cap at 40.
- Missing information is neutral, never a penalty. Say what is missing.

## Write it back

```bash
scoutjobs job set <id> --score 78 --fit-notes "Go+Postgres strong; no k8s (asked); hybrid Berlin ok; salary not listed" --status ranked
```

Keep `--fit-notes` under 200 characters: the three facts that moved the score most.

## Recommend, then ask

Read `fit_apply_floor` from `config.yaml` (default 60). Present a table: id, company, title, score, verdict (apply / maybe / skip), one-line why. Then ask the user which to apply to and which to skip. Do not apply or skip on their behalf.

For every skip, record the reason and the signals. Signals are the facts that drove it, as a JSON object with short lowercase keys; arrays are fine:

```bash
scoutjobs job skip 12 --reason "Java-only shop, I want Go" --signals '{"stack":["java","spring"],"remote":"hybrid","seniority":"senior"}'
```

For every apply decision on a job scoring under 50, or skip on a job scoring 70 or more, also log an override so the learner sees where the scorer disagrees with the user:

```bash
scoutjobs decision log --kind override_score --job 12 --reason "<user's words>" --signals '{"fit_score":82,"user":"skip"}'
```

Jobs the user wants to pursue stay `ranked`; `resume-tailor` and `application-tracker` take it from there.

## Signal keys to use consistently

`stack` (array), `remote`, `location`, `seniority`, `company_size`, `industry`, `salary_below_floor` (true/false), `on_call`, `visa`, `role_family`, `language` (spoken). Consistent keys make `scoutjobs decision summary` countable.
