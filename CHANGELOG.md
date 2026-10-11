# Changelog

All notable changes to ScoutJobs. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow
[Semantic Versioning](https://semver.org/). Binaries for every tag are on the
[releases page](https://github.com/ZoolCoder/scoutjobs/releases).

## [Unreleased]

### Fixed
- Connecting Google without the OAuth client secret no longer fails at
  Google's token endpoint with a Gmail error (#33). Connect checks the client
  ID and secret first and names what is missing; a reconnect from the Calendar
  page reports as Google Calendar and returns there; a config.yaml client ID
  without a secret uses the imported client's secret; config.yaml edits apply
  without restarting `serve`.

## [0.5.0] - 2026-10-11

### Added
- Google Calendar sync both ways (`calendar sync`; the dashboard syncs every
  few minutes): interviews scheduled in ScoutJobs are added without attendees,
  so nobody is invited; calendar events that name a tracked company become
  interviews; a move or a deletion follows on the other side. Only events
  ScoutJobs created are ever written.
- Main CV and cover letter on the Profile page, written by your agent; every
  tailored resume and cover letter starts from them.
- Documents panel per job: preview (Markdown, PDF, text), edit and download;
  files that do not exist yet are not links.
- Proposed e-mails per job: follow-up, interview thank-you, offer accept,
  negotiate and decline. Saved or drafted in Gmail, never sent.
- Prepare all and auto-prepare: a queue that prepares every open job above
  your score floor, one at a time.
- Live run progress on the job page; finished steps fold under Redo.
- Job checklist: one list on the job page from fit to sent, with what is
  done, what needs a look, what blocks sending and the one next step.
- Grouped navigation (Apply, Interview, You) with live counts of jobs being
  prepared and upcoming interviews.
- `job run --keep-going` and a "Keep going if the review asks for fixes"
  checkbox: a fix verdict is recorded as a warning and the PDF, gates and
  e-mail draft are still prepared. Sending stays blocked.
- Main CV from your own CV: a "Profile from your CV" step reads the PDF or
  DOCX in `cv/`, adds what profile.md lacks (the old one is kept as
  `profile.prev.md`), and the main CV keeps that file's sections, order and
  style. Tailored resumes keep the main CV's layout; a new `format` gate warns
  when they do not.
- Apply with gaps (#30): for an employer portal, acknowledge each must-have
  without evidence (`job acknowledge`, the job page, or
  `POST /api/jobs/{id}/acknowledge`), open the official posting, then mark the
  job applied. The decision is logged; the fit review, the drafts and the
  e-mail send gate are unchanged, and a veto is never acknowledged.

### Fixed
- Prepare application no longer stalls (#29). Files left in a differently
  spelled application folder are merged (conflicts kept and reported, never
  overwritten), the agent's draft review and ATS score are read from
  `review-result.json` and `ats-result.json` instead of relying on it to call
  the CLI, quality gates block sending only and no longer stop the PDF or the
  e-mail draft, and a failure that a later run fixed is shown as history
  with the next step, not as the current error.
- Gmail sync no longer fails with Google's raw 403 quota error (#27). Rate
  limits and transient errors are retried with backoff, message reads are
  paced under the per-user quota, and each message is read once: its metadata
  is cached in `gmail-cache.json` (0600, deleted by `gmail disconnect`), so a
  sync stopped by the quota resumes where it stopped. Replies found before the
  stop are still logged, and the Inbox shows a short "wait a minute" message.
- `doctor` no longer calls `agent` and `agent_command` unknown config keys.

## [0.4.0] - 2026-10-11

### Added
- One-click application prep (#24): the job page's **Prepare application**
  panel, `scoutjobs job run <id>`, `POST /api/jobs/{id}/run` and
  `/scoutjobs:run` run fit review, tailored resume, cover letter, draft
  review, resume PDF, ATS check, quality gates and a local e-mail draft in
  order. It drives Claude Code, Codex or any CLI set as `agent_command`, skips
  finished steps, stops at the first failure with the reason, logs each step
  as an Activity event, and can be cancelled. Nothing is sent.
- Gmail without YAML: `scoutjobs gmail setup` and the Inbox page guide you
  through your own Google client, and inbox triage runs locally first to save
  agent tokens.
- Google Calendar, read-only, with the same sign-in (#22): your events show on
  the Calendar page, and one that names a tracked company can be tracked as an
  interview. `scoutjobs gmail test` checks mail and calendar separately.

### Changed
- Mail sync never reads messages older than your first tracked job.

## [0.3.2] - 2026-10-11

### Added
- Learned preferences wait for review: `learn-preferences` proposes them with
  confidence, supporting and contradicting decisions and evidence, and the
  Preferences page lists them under "Awaiting your review" with confirm, edit
  (saved as stated) and reject. Also `scoutjobs pref propose|review|confirm|reject`,
  the matching MCP tools and `/api/preferences/proposals`. Every outcome is an
  Activity event; a rejected value comes back only with more support (#12).
- `GET /healthz` reports the dashboard's version, pid, binary and data dir.
- `kill -HUP <pid>` restarts the dashboard on the installed binary.

### Changed
- A learned preference no longer overwrites a stated one; the API answers 409.
- `scoutjobs doctor` warns when the running dashboard is on another version or
  data dir, and `scoutjobs update` says whether the dashboard will restart (#17).

### Fixed
- `scoutjobs serve` on a busy port names the dashboard or program holding it
  and suggests a free port, instead of a bare bind error.
- Database open errors name the database path and where the data dir came
  from, with a hint for SQLite 14, 8, 5 and 6, including macOS privacy-protected
  folders such as ~/Documents (#19).

## [0.3.1] - 2026-10-11

### Added
- The dashboard shows the running version in the sidebar and serves it on
  `GET /version`. An open tab offers a reload when the server is newer.

### Changed
- `scoutjobs serve` restarts itself on the new binary after an upgrade (brew,
  `scoutjobs update`, `make install`), on the same address. `--no-restart`
  opts out; on Windows, restart the dashboard by hand.
- `make build` stamps the git version with a `-dev` suffix, so local builds
  report what they are and `scoutjobs update` still leaves them alone.

## [0.3.0] - 2026-10-11

### Added
- Pipeline works like a Jira board: a job opens in a modal on top of the board
  (stepper, quick actions, mail, activity, details) and stays open while you
  move it. Full-screen focus mode (`f`), compact cards, and limits for large
  pipelines: 50 cards per column with "Show all", closed stages collapsed to
  strips, and a paged list view.
- Gmail API: `scoutjobs gmail connect|status|disconnect` with your own OAuth
  desktop client. ScoutJobs reads inbox metadata to match replies and uploads
  drafts; it never sends. IMAP sync keeps working.
- `scoutjobs email log` records a thread an agent found through its Gmail
  connector; the `inbox-triage` skill uses it.
- Inbox page, a Mail section per job, and "Write in Gmail", "Draft in Gmail"
  and "Search Gmail" actions. The overview opens with a Today list.
- Job page tabs: overview, mail, interviews, files, activity.
- Interview prep checklist (six items) on the Interviews page, the calendar
  and the API (`prep` on `PATCH /api/interviews/{id}`), plus a "How did it
  go?" list for past interviews without an outcome.
- Search: saved searches, result filters (remote only, exclude words, posted
  within, minimum salary) and a description preview per result.
- Google-style calendar with event cards, LinkedIn connections import, MCP
  over HTTP, Gmail, Google Calendar and Google Drive connector skills, and n8n
  workflows for search, fit alerts, follow-ups and Gmail.
- Calendar `?new=<day>&job=<id>` preselects the job for a new interview.

## [0.2.0] - 2026-10-11

### Added
- `scoutjobs update`: installs the next release in place (checksum verified;
  Homebrew and package installs are handed to their manager).
- Pipeline board: drag a card onto another stage, or onto the closing stages
  shown under the board while dragging; the add-job form fills itself from a
  posting URL, and `scoutjobs job add --from <url>` does the same on the CLI.
- Search accepts a careers page URL in place of `source:slug`, and adds
  Recruitee, BambooHR and the Hacker News "Who is hiring" thread.
- Evidence-backed fit review: `scoutjobs job fit <id>` validates and stores
  `fit-review.json` (one row per requirement with evidence, strength,
  confidence, defensible wording and risk; vetoes outside the score), sets
  score, notes and the new `veto` field; `fit-review` skill and
  `/scoutjobs:evidence`.
- Application quality gates: `scoutjobs job check <id>` and
  `GET /api/jobs/{id}/check` run documents, fit review, coverage, grounding,
  duplicates, ATS text layer and filename gates; `email send --yes` and
  `cv export <id>` refuse while one fails, `--skip-checks` overrides and logs
  `check:skipped`; `scoutjobs job review <id> --verdict send|fix` records the
  draft-reviewer verdict; `/scoutjobs:check`; the job page shows the gates and
  the evidence table, the board shows a veto badge.
- MCP tools `job_fit`, `job_check`, `job_review`; `job_set` takes `veto`.
- Eval cases `fit-review-evidence`, `draft-review-invented-claim`,
  `apply-check-blocks`.

### Changed
- `resume-tailor` and `cover-letter-generator` write from the agreed wording
  in `fit-review.json`; `apply-by-email` runs `job check` first and reports
  every failed gate instead of sending past it.
- Git flow: `develop` is the integration branch, `main` the released state;
  CI runs the full matrix only for `main`.

### Fixed
- Dark theme contrast, focus rings and readability across the dashboard.

## [0.1.0] - 2026-10-11

### Added
- Agent Skills for the whole loop (search, fit, CV check, tailoring, cover
  letter, apply by e-mail, interviews, debrief, weekly review, learning), the
  same files on Claude Code, Codex, Cursor, Gemini CLI and Copilot; six agents
  also shipped as skills; slash commands `/scoutjobs:*` and Codex prompts
  `/scoutjobs-*`.
- Go CLI `scoutjobs` with a SQLite store under `~/.scoutjobs`
  (`SCOUTJOBS_HOME` overrides), `init`, `doctor` (`--online` compares with the
  latest release), `brief`, `search`, `job`, `cv`, `email`, `stats`, `export`.
- Search over Greenhouse, Lever, Ashby, Workable, SmartRecruiters, Personio,
  any RSS or Atom feed and the remote boards; dedupe by URL and title;
  salaries normalised to yearly figures in the configured `currency`.
- Local dashboard without JavaScript: pipeline, job pages, editable drafts,
  add-job form, board search, interviews, reviews, learning, preferences.
- JSON API with bearer token, signed webhooks (`X-ScoutJobs-Timestamp`,
  HMAC-SHA256 `X-ScoutJobs-Signature`), n8n example workflows, stdio MCP
  server (`scoutjobs mcp`).
- Applications by e-mail only after the message is shown and `--yes` given;
  SMTP password from `SCOUTJOBS_SMTP_PASSWORD`; `email test` and `email sync`
  (IMAP replies into the pipeline); `cv export` to an ATS-safe PDF.
- Eval suite under `evals/` (`make evals`), CI (tests, lint, plugin
  validation, GoReleaser check), Dependabot, Antora manual on GitHub Pages.
- Releases: binaries for macOS, Linux and Windows, Homebrew cask, `deb`,
  `rpm` and `apk` packages, `install.sh`.

[Unreleased]: https://github.com/ZoolCoder/scoutjobs/compare/v0.5.0...HEAD
[0.5.0]: https://github.com/ZoolCoder/scoutjobs/releases/tag/v0.5.0
[0.4.0]: https://github.com/ZoolCoder/scoutjobs/releases/tag/v0.4.0
[0.3.2]: https://github.com/ZoolCoder/scoutjobs/releases/tag/v0.3.2
[0.3.1]: https://github.com/ZoolCoder/scoutjobs/releases/tag/v0.3.1
[0.3.0]: https://github.com/ZoolCoder/scoutjobs/releases/tag/v0.3.0
[0.2.0]: https://github.com/ZoolCoder/scoutjobs/releases/tag/v0.2.0
[0.1.0]: https://github.com/ZoolCoder/scoutjobs/releases/tag/v0.1.0
