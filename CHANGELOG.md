# Changelog

All notable changes to ScoutJobs. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow
[Semantic Versioning](https://semver.org/). Binaries for every tag are on the
[releases page](https://github.com/ZoolCoder/scoutjobs/releases).

## [Unreleased]

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

[Unreleased]: https://github.com/ZoolCoder/scoutjobs/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/ZoolCoder/scoutjobs/releases/tag/v0.1.0
