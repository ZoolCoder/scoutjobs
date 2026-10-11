## Product
ScoutJobs is an open-source "job-search operating system" for one person: the job seeker. It runs locally (single Go binary, SQLite, localhost dashboard) and plugs into AI coding agents (Claude Code, Codex, Cursor, Gemini CLI, Copilot) through skills. The agent finds and scores jobs, tailors resume and cover letter, preps and mocks interviews, debriefs, negotiates. Every skip/apply/decline the user makes is logged with a reason and turned into learned preferences, so it gets better over time. Nothing leaves the machine; no accounts, no telemetry.

## Users
Software engineers and other tech workers actively job hunting, often while employed, often stressed. They live in a terminal and an editor, run the dashboard in a browser tab next to it, and want calm, dense, honest status: what is due today, where every application stands, what they should practice, what the tool has learned about them. Dark terminal aesthetic is welcome but it must also read well in daylight.

## Stack
Go server rendering HTML templates, one CSS file, no JavaScript framework, no external fonts or assets allowed (strict CSP, offline). System fonts or a self-hosted font only.

## Screens
1. Overview: KPI strip (open jobs, applied this week vs target, interviews, offers, response rate, streak, practice sessions, learning hours), funnel bars, follow-ups due, stale, upcoming interviews, drafts waiting, learning gaps, learned preferences, last decisions, recent activity.
2. Pipeline: kanban board by status (new, ranked, drafted, applied, interview, offer, hired, rejected, no_response, withdrawn, skipped) with a status dropdown on each card and a search box.
3. Job detail: facts, fit score, notes, events timeline, status form.
4. Interviews, Learning, Preferences, Activity: simple tables.

## Current tokens
Background #0f1115, panel #171a21, line #262a33, text #e6e8ec, muted #8b93a1, accent mint #5ad1a5, warn #f2c14e, bad #ef6b73. System UI font 14px, monospace for numbers. Radius 8px. Flat cards. Wordmark is plain text "ScoutJobs". No logo yet.
