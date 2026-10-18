---
name: scoutjobs-setup
description: First-run onboarding for ScoutJobs. Builds the user's profile.md from their CV, LinkedIn text or a short interview, records stated preferences, checks the scoutjobs CLI, and explains the daily loop. Use when the user says "set up scoutjobs", "onboard me", "start my job search", or when ~/.scoutjobs/profile.md is missing or still the empty template.
license: MIT
compatibility: Needs the scoutjobs binary on PATH (run `make install` in the repo) or SCOUTJOBS_HOME set.
---

# ScoutJobs setup

Goal: in one conversation, leave the user with a filled `profile.md`, a handful of stated preferences, and a running dashboard. Everything stays on their machine.

## 1. Check the CLI

```bash
scoutjobs path || echo "MISSING"
```

- `MISSING` or command not found: tell the user to run `make install` in the scoutjobs repo (or `go install ./cmd/scoutjobs`). Stop until it works.
- Otherwise run `scoutjobs init`. It is idempotent and prints which files it created.

## 1b. Country first

Ask one question: which country they search from (and live in, if different). Then run `scoutjobs country set <code or name> --json`. It costs no tokens beyond its output:

- It writes `country`, `currency` and the country's local job boards to `config.yaml`.
- It proposes `locations`, `languages`, `timezone` and `cv_norms` as pending preferences. Show them to the user as a short list. Confirm each one they accept with `scoutjobs pref confirm <key>`, or change it with `pref set ... --source stated`. Reject the rest with `pref reject <key>`.
- Its `ask` list holds what the country cannot answer: salary floor (in the new currency), work mode, roles, work rights. Ask those in step 2. Never suggest a salary figure.

An unknown country fails with a hint; `scoutjobs country list` shows the table. A country in the location filter but not the table (e.g. Kenya) gets no boards or currency, and the remote boards still apply.

## 2. Build the profile

Read `$(scoutjobs path)/profile.md`. If it still has empty fields, collect the facts. Ask for the easiest source first:

1. A CV file (PDF, DOCX, MD). Ask the user to copy it into `$(scoutjobs path)/cv/` (that folder is where every skill looks for the master CV, portfolio links and certificates; it has a README). Read it and extract.
2. The LinkedIn data export (LinkedIn: Settings > Data privacy > Get a copy of your data; the "larger archive" includes connections). Run `scoutjobs linkedin import <export.zip>`, then read `$(scoutjobs path)/linkedin/profile.md`: headline, summary, positions, education, skills, certifications. It also loads connections, so `scoutjobs contacts <job>` later finds people at a company. A CV in Google Drive works too, through the `google-drive` skill.
3. LinkedIn "About" and experience text pasted in.
4. A short interview: five questions max per turn. Roles held, strongest skills with years, three results they are proud of (with numbers), target role and seniority, location and remote stance, salary floor, deal-breakers, work authorization.

Write the profile in the template's sections. Rules:
- Only facts the user gave. Never invent employers, dates, numbers or certifications. If a bullet has no number, ask for one once, then leave it qualitative.
- Mark strength levels honestly (Strong = could interview on it today, Working = used in production, Learning = reading or side project).
- Keep it under 200 lines. Everything here may end up in a CV.

Show the user the draft, apply corrections, then write it.

Later, to grow an existing profile, run `scoutjobs profile expand --json`: it lists skills found in `cv/`, `linkedin/` and the user's public GitHub repositories that the profile lacks, with where each showed up, without reading those sources into the conversation. Ask which are true, then `scoutjobs profile expand --add <skill,skill>`. Never add one the user did not confirm.

## 3. Record stated preferences

Preferences drive scoring and tailoring. Record the ones the user said out loud with `--source stated`:

```bash
scoutjobs pref set remote "remote or hybrid, max 2 days onsite" --source stated --confidence 1 --evidence "setup interview"
scoutjobs pref set roles "backend, platform" --source stated --confidence 1
scoutjobs pref set salary_floor "85000 EUR" --source stated --confidence 1
scoutjobs pref set locations "Germany, Netherlands, remote EU" --source stated --confidence 1
scoutjobs pref set avoid "consultancies, on-call heavy" --source stated --confidence 0.8
```

Use short stable keys: `remote`, `roles`, `seniority`, `locations`, `salary_floor`, `stack_love`, `stack_avoid`, `company_size`, `industries`, `avoid`, `visa`. Other skills look these keys up.

## 4. Config

Open `$(scoutjobs path)/config.yaml` and ask only about `weekly_apply_target` and `search_queries` (two or three role titles). `currency` and the local boards came from step 1b. If they named target companies, add their ATS slugs to `search_boards`. Leave the rest at defaults unless asked. `api_token` and `webhook_url` are only for automation tools (n8n); mention them once.

## 4b. CV check

If a CV file landed in `cv/`, offer to run the `cv-check` skill now. It gives an ATS and a quality score and logs them to the dashboard Profile page.

## 5. Story bank seed

If the CV has strong achievements, offer to capture two or three STAR stories now using the `behavioral-story-extractor` agent (or ask the questions yourself). Save each to `$(scoutjobs path)/stories/<slug>.md` and list them in the profile's Stories section.

## 6. Hand-off

Tell the user the loop, in this order:
1. `job-search` finds postings (`scoutjobs search --add`, Greenhouse, Lever, Ashby and five remote boards) and adds them as `new`.
2. `job-fit` scores them against the profile and preferences.
3. `resume-tailor` plus `cover-letter-generator` draft the application; `application-tracker` records it.
4. `interview-prep` and `mock-interview` when an interview lands; `interview-debrief` after it.
5. `learn-preferences` once a week turns their decisions into learned preferences.
6. `scoutjobs serve` opens the dashboard at http://127.0.0.1:7070 (Profile & CV page shows profile.md, the cv folder and CV checks; theme switch bottom left).

Log the session: `scoutjobs session log --kind review --topic "setup"`.
