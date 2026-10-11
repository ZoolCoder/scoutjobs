---
name: interview-debrief
description: Captures what happened in a real interview while it is fresh, records outcome and feedback in ScoutJobs, extracts lessons into the story bank and learning plan, and drafts the thank-you note. Use right after an interview, or when the user says "debrief", "how did it go", "they asked me", "I got rejected after the interview".
license: MIT
---

# Interview debrief

Ten minutes, right after the call. Input: `scoutjobs interview list --all` to find the interview, the application folder's `interview-prep.md`.

## Ask, in this order (one message, short answers)

1. Questions they asked, as close to verbatim as you remember.
2. Which answers felt strong, which felt weak, and why.
3. Anything surprising about the role, team or company.
4. Your gut: want to continue?
5. Next step they mentioned and when.

## Write

- `debrief-<YYYY-MM-DD>.md` in the application folder: the five answers, plus `## Lessons` (3 bullets max).
- Compare asked questions against `## Likely questions` in `interview-prep.md`. Unpredicted questions become new entries in the prep template for that stage; note them under `## Lessons`.
- Weak answers: patch the matching story in `stories/<slug>.md` or add a new one.
- Knowledge gaps: `scoutjobs learn add --skill "<topic>" --priority 1 --notes "asked at <company> <stage>"`.

## Record

```bash
scoutjobs interview set <iid> --outcome pending --feedback "<two-line summary: what went well, what to fix>"
scoutjobs job set <id> --note "debrief: <next step and date>"
scoutjobs session log --kind review --topic "<company> <stage> debrief"
```

When the result arrives later: `--outcome passed` or `--outcome failed`, and `scoutjobs job set <id> --status rejected|offer` as appropriate. For a failed outcome, log the decision signal so learning can see patterns across companies:

```bash
scoutjobs decision log --kind other --job <id> --reason "failed <stage>: <their feedback>" --signals '{"stage":"system_design","gap":"estimation"}'
```

## Thank-you note

Offer a 4-sentence note: thanks, one specific thing from the conversation, one point you want to add or correct, next step. Only draft it; the user sends it.

## If the gut says no

Ask whether to withdraw. If yes: `scoutjobs job set <id> --status withdrawn` and `scoutjobs decision log --kind withdraw --job <id> --reason "<their words>" --signals '{...}'`. This is the most valuable preference data there is; capture the signals carefully.
