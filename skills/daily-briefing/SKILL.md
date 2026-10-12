---
name: daily-briefing
description: Gives the user a focused daily plan for their job search in under a minute - interviews today, follow-ups due, drafts waiting, new jobs to score, one learning task - based on ScoutJobs data. Use when the user says "good morning", "what should I do today", "briefing", "status", or starts a session with no specific ask.
license: MIT
---

# Daily briefing

One command gives everything:

```bash
scoutjobs email sync --json 2>/dev/null || true   # when imap_host is set: log replies first so the brief knows who answered
scoutjobs daily --json      # due daily searches and follow-up drafts; idempotent, sends nothing
scoutjobs brief --compact   # full payload: `scoutjobs brief` (only when a section needs notes or urls)
```

`daily` reports `added` (jobs the daily searches tracked) and `follow_ups` (drafts waiting on the job's Emails tab): mention both in **Do next** when not empty, as "review the follow-up to X", never as sent.

`brief` returns `stats`, `upcoming_interviews`, `new_jobs`, `ranked_jobs`, `drafted_jobs`, `learning`, `preferences`, `recent_activity`, and `config` as JSON.

## Output (keep under 15 lines)

1. **Today**: interviews in the next 48 hours with prep status. Anything not `ready` is the top task.
2. **Pipeline**: open count, applied this week vs `weekly_apply_target`, response rate.
3. **Do next**, max 4 items, in this priority: prep an interview → send a due follow-up (`stats.due_follow_ups`) → finish a draft (`drafted_jobs`) → score new jobs (`new_jobs`) → search if fewer than 3 ranked jobs are waiting.
4. **Learn**: one `in_progress` learning item with a 30-minute suggestion.
5. **Streak**: `activity_streak_days`. One line, no cheerleading.

Offer to start the first item. Do not list things that are empty.

If `profile.md` is still the template, skip the briefing and run `scoutjobs-setup`.
