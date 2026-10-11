# Changelog

All notable changes to ScoutJobs. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow
[Semantic Versioning](https://semver.org/). Binaries for every tag are on the
[releases page](https://github.com/ZoolCoder/scoutjobs/releases).

## [Unreleased]

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

[Unreleased]: https://github.com/ZoolCoder/scoutjobs/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/ZoolCoder/scoutjobs/releases/tag/v0.2.0
[0.1.0]: https://github.com/ZoolCoder/scoutjobs/releases/tag/v0.1.0
