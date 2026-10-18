---
description: Chase quiet applications: draft the follow-ups due (at most two per job), log the ones sent, offer to close stale ones
argument-hint: [job id]
---

Use the Follow-ups section of the `application-tracker` skill from this plugin. Start with `scoutjobs job followup --json`; with a job id, work on that job only. Draft each due follow-up for the user to send from their own mailbox, log it with `scoutjobs job followup <id>` only after they confirm it went out, then show `scoutjobs job sweep` and ask before closing anything.

Arguments: $ARGUMENTS
