---
name: weekly-review
description: Weekly retrospective on the job search - funnel movement, response rates, what worked, stale applications to close, decisions to learn from, learning progress - and a plan for next week. Runs learn-preferences as part of it. Use on Fridays or Mondays, or when the user says "weekly review", "how is my search going", "retro".
license: MIT
---

# Weekly review

Input:

```bash
scoutjobs stats --json
scoutjobs stats --funnel --json   # what works: answer, interview and offer counts by source, fit band, month applied
scoutjobs job list --json --compact
scoutjobs decision summary --json
scoutjobs session list --limit 50 --json
scoutjobs learn list --json
```

## Sections (write them to `$(scoutjobs path)/reviews/<YYYY-WW>.md` and show the user)

1. **Numbers**: applied this week vs target, interviews held, offers, response rate, average fit of applied jobs. Compare with last week's review file if it exists.
2. **Funnel health**: where jobs got stuck. Many `ranked` not `drafted` = application friction, suggest batching. Many `applied` with no response and avg fit under 65 = aim higher on fit, lower on volume. Interviews failing at one stage = point `mock-interview` there.
   **What works**: from the funnel, name the source and the fit band with the best answer rate (only groups with 3+ applications) and the one with the worst, with numbers; `days` is the median days to an answer. Suggest moving next week's applications toward the better one.
3. **Close out**: show `scoutjobs job sweep` (applied and interviewing jobs quiet past `stale_days`) and ask: close all, pick some, or skip; then `scoutjobs job sweep --yes` or `scoutjobs job sweep <id>... --yes`. List due follow-ups from `scoutjobs job followup --json` (`next` is `due`; each job gets at most 2).
4. **Learn**: run the `learn-preferences` skill now (it confirms with the user).
5. **Skills**: learning hours this week, items done, `upskill` suggestion if a gap caused a rejection.
6. **Next week**: three commitments, each with a number (for example "apply to 4 jobs with fit 70+", "two mock system design sessions", "3 hours Kubernetes").

Log: `scoutjobs session log --kind review --topic "weekly review <YYYY-WW>" --notes "<the three commitments>"`.

Tone: factual, short. The user reads this on a Friday.
