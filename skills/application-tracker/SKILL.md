---
name: application-tracker
description: Records application state in ScoutJobs. Marks jobs drafted, applied, interview, offer, rejected, withdrawn or no_response, adds notes and follow-up reminders, and keeps the per-application folder tidy. Use when the user says "I applied", "mark as rejected", "they replied", "track this", "status update", or asks what is pending.
license: MIT
---

# Application tracker

The tracker is the `scoutjobs` CLI. Status vocabulary, in pipeline order:

`new` → `ranked` → `drafted` → `applied` → `interview` → `offer` → `hired`
Side exits: `skipped`, `rejected`, `no_response`, `withdrawn`, `offer_declined`.

## Commands you will use

```bash
scoutjobs job list --open                      # everything still in play
scoutjobs job show <id|company/title>           # one job with history
scoutjobs job set <id> --status applied --note "applied via Greenhouse, used CV v3"
scoutjobs job set <id> --status rejected --note "rejected after screen: wanted more k8s"
scoutjobs job set <id> --deadline 2026-10-31 --salary-min 80000 --salary-max 95000 --currency EUR
scoutjobs interview add <id> --stage screen --at 2026-10-20T14:00 --format video --interviewer "Jane, EM"
scoutjobs stats                                 # funnel, follow-ups due, stale list
scoutjobs email sync [--dry-run]                # IMAP: log replies as reply:received, move to interview/rejected when the wording is clear
```

Resolve jobs by id, by `company/title`, or by a unique company substring. If the CLI says the reference is ambiguous, list and ask.

## Rules

- Every status change gets a `--note` with the fact that caused it. Notes feed the activity log and the weekly review.
- `applied` only when the user confirms they submitted. Drafts ready but not sent are `drafted`.
- When the user reports a rejection or ghosting, ask one question: "Anything you'd do differently?" Put the answer in the note. Do not push.
- When an interview is scheduled, add it with `scoutjobs interview add` (the job flips to `interview` automatically) and suggest `interview-prep`.
- When an offer arrives, set `offer`, record salary fields, and suggest `salary-negotiation-prep` or `offer-comparison-analyzer`.
- Decisions that teach preferences go through `scoutjobs decision log`: withdrawals (`--kind withdraw`), declined offers (`--kind decline_offer`), accepted offers (`--kind accept_offer`). Always include `--reason` in the user's words and `--signals` with the facts.

## Per-application folder

`scoutjobs job show <id> --json` returns `dir`, usually `~/.scoutjobs/applications/<company>_<title>/`. Keep there: `posting.md`, `resume.md` (tailored), `cover-letter.md`, `research.md`, `interview-prep.md`, `stories.md`, `debrief-<date>.md`. Nothing else.

## Follow-ups

`scoutjobs job followup --json` lists every applied job with `days_quiet`, `follow_ups` sent and `next` (`due`, `in Nd`, `done`, `sweep`). An application gets at most 2 follow-ups, each `follow_up_days` (config, default 10) after the last activity. Never chase a `drafted` job: it was not sent.

For each `due` job, offer a follow-up of 60 to 120 words that adds no new claim: the role, still interested, one line on fit already in the application, a question about next steps. The second and last one is shorter and asks for a brief note if the role is filled. Log it only after the user says it went out: `scoutjobs job followup <id>`, which restarts the quiet timer.

`scoutjobs job sweep` lists applied and interviewing jobs quiet past `stale_days` (default 60). Show the list and ask: close all, pick some, or skip. Then `scoutjobs job sweep --yes`, or `scoutjobs job sweep <id> <id> --yes` for a selection. Never sweep without that answer.
