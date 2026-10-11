---
description: Capture one STAR story into the story bank from a CV or a short interview
argument-hint: [experience to capture]
---

Use the `interview-prep` skill from this plugin. Only the story-bank part: spawn the `behavioral-story-extractor` agent (cv-grounded if profile.md has experience), confirm the draft with the user, save it to `$(scoutjobs path)/stories/<slug>.md`, and add a line to the Stories section of profile.md.

Arguments: $ARGUMENTS
