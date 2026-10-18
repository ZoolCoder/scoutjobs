# Changelog

All notable changes to ScoutJobs. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow
[Semantic Versioning](https://semver.org/). Binaries for every tag are on the
[releases page](https://github.com/ZoolCoder/scoutjobs/releases).

## [Unreleased]

## [0.12.0] - 2026-10-18

### Added
- Board `arbeitsagentur:de|<city>` for the Bundesagentur für Arbeit;
  `country set DE` adds it.
- Postings get `speaks:` tags for the languages they require. A required
  language your profile lacks costs 15 fit points.
- `doctor` warns about skills not linked into an agent and about a search
  with no country or location focus.

### Changed
- Searches stay in the profile's locations, else the config country.
  `--location any` searches everywhere.
- Postings with a location the search cannot place rank below known fits
  instead of being dropped.
- Board duplicates merge across company suffixes (Acme GmbH and Acme).
- The dashboard restarts when `config.yaml` changes and refreshes when
  `profile.md` changes. A restart waits until no agent run is going.
- `make lint` runs golangci-lint only when it is installed.
- README: a pitch up front, a hero image, a tour GIF and fresh screenshots
  from a demo workspace.

## [0.11.0] - 2026-10-18

### Added
- `scoutjobs country list | show | set <code or name>`: one answer sets up
  the search. It writes `country`, `currency` and the country's local job
  boards to `config.yaml`, and proposes `locations`, `languages`, `timezone`
  and `cv_norms` for you to confirm with `pref review` or on the Preferences
  page. It never suggests a salary. The table has 49 countries and costs no
  tokens; the setup skill now starts with it.
- Danish boards `jobindex:dk`, `jobbank:dk`, `jobdanmark:dk` and the
  freehire.me feed.
- `scoutjobs cv verify` checks an exported PDF the way an ATS reads it: page
  count, text layer, e-mail, required phrases, date-range dashes.
- `scoutjobs profile expand` lists skills in `cv/`, `linkedin/` and your
  public GitHub repositories that `profile.md` lacks; `--add` writes only the
  ones you name.
- `job rank` scores jobs locally against `profile.md`, free and best fit
  first.
- `job followup`, `job sweep` and `job verify`: at most two follow-ups per
  application, quiet ones closed as no response after `stale_days`, and
  postings closed on their ATS skipped. The dashboard marks follow-ups sent,
  sweeps stale jobs and flags deadlines.
- Search checks that postings are still live and honours `robots.txt`.
- `email_sync: off | manual | auto` makes reading mail optional.
- The dashboard refreshes live when the CLI or an agent changes data.
- MCP tools `country_show`, `country_set`, `cv_verify` and `profile_expand`;
  `mcp` exposes only the tools the enabled features need.

### Changed
- Unattended runs scope their tools, stop at `agent_budget_usd`, reuse the
  prompt cache and run scoring on `agent_cheap_model`; skills return compact
  JSON. Agent token use drops sharply.
- Search ranks postings that break a must (wrong place, below your salary
  floor) after every posting that does not.
- With the agent mailbox, reading mail stays manual: each sync spends tokens.

### Fixed
- Danish board matches are kept when the short feed snippet does not show
  the query.
- Inbox links are hidden when e-mail is off.

## [0.10.0] - 2026-10-13

### Added
- `scoutjobs google setup | check | reconnect | verify` (`gmail` still works).
  `google check` names where the client ID and secret come from, which source
  wins and what is missing, without printing the secret.
- After each connect, and with `google verify` or *Verify*, ScoutJobs checks
  the account, Gmail and Calendar and reports each one apart.
- *Your own Google client* card on Connections: setup steps, *Verify*,
  *Reconnect Google* and *Disconnect*.
- While the Google app is in Testing, the CLI, Inbox and Connections list the
  Branding and Audience steps to publish it, and say that publishing ends the
  7-day expiry but not the unverified-app warning.
- Manual: *Your own Google client* page, a PDF of the whole manual, a
  Markdown copy of each page and `llms.txt`.

### Changed
- Google failures are sorted by kind (client, API disabled, missing
  permission, Testing expiry, revoked, denied, rate limit, Google error), each
  with its fix; retryable ones say so.
- *Reconnect Google* needs no Disconnect first; the saved connection is
  replaced only when the new sign-in succeeds.
- The manual uses the ZoolCoder docs UI (zc-supplemental-ui v0.2.1).

### Security
- IMAP and SMTP require TLS 1.2 or later.
- The dashboard's theme cookie is HttpOnly.
- GitHub Actions are pinned by commit SHA; Dependabot waits 7 days.

## [0.9.0] - 2026-10-12

### Added
- Ready to send score: the job page rates each application from 0 to 100.
  New checks trace every number in the drafts to your profile, CV, stories or
  the posting, warn about a generic cover letter, a job that breaks your
  must-haves, and another application to the same company in 90 days.
- *Mark applied* is refused while a check fails (the API answers `409`);
  *I applied anyway* records why as `check:override`. The score is logged on
  every submission.
- Forecast: the chance of an answer and an interview for a job, from your
  own applications by source and fit, with a likely range; on Reviews, the
  interviews to expect in the next 30 days. `GET /api/forecast`. Starts after
  15 applications.
- *What works* groups applications by their quality score too.
- Company file per company: AI research from public sources, and notes you
  paste from review sites yourself. ScoutJobs never logs in to or scrapes
  Glassdoor or similar sites. The prep pack reads the research.
- Question bank per company, filled from pasted reports, debriefs and the
  prep pack: tick what you rehearsed and rate your answers; three rehearsed
  questions tick the *stories* prep item.
- Debrief on past interviews: questions asked, what went well, what to work
  on, saved on the interview, the question bank and the company notes.
- Written mock interview: five questions from the bank, your typed answers,
  and an optional AI score per answer logged as a practice session.
- Offer panel on jobs at the offer stage: the assistant writes
  `offer-prep.md` with what to ask for and how.

### Changed
- *What works* tables sit two per row so every column shows.

## [0.8.0] - 2026-10-12

### Added
- Daily searches: press *Run daily* on a saved search and it runs once a day,
  adding up to 20 good matches to the pipeline. The overview says how many
  came in.
- Automation panel on Preferences, run every hour by `scoutjobs serve` or by
  the new `scoutjobs daily [--json]` from cron: daily searches, follow-up and
  thank-you drafts for quiet applications and past interviews, and interview
  prep packs. Nothing is ever sent.
- *Build prep pack* on an interview: the AI assistant writes the company
  research, the stories to tell and a prep sheet, and ticks the prep items it
  covered.
- Apply queue: tick several jobs in the pipeline's list view and press
  *Prepare selected with AI*; a strip on the pipeline shows what runs and what
  waits.
- *Keywords to add* on the job page: requirements the tailored resume does
  not word like the posting yet, each with the profile line that backs it.
  *Fix with AI* adds only the backed wordings.
- Story bank: the new *Stories* page lists your interview stories and the
  jobs each one answers; a job's Interviews tab maps every must-have to a
  story or flags it.
- *What works* on Reviews: answer, interview and offer rates by source, fit
  band and month applied, and `GET /api/funnel`.
- Command palette: `Ctrl K` / `⌘K` jumps to any page, job or action; `g`
  then a letter goes to a page and `?` lists every shortcut.
- Write and edit stories on the dashboard: *New story* asks for the STAR
  parts and saves them in the template the interview skills read.
- Prep pack status on the calendar event and the Interviews page: ready,
  being written, or a *Rebuild* button.
- Scores weigh what works: a source that answers you much more or less than
  average moves a job's fit by 5 points, with the reason shown.
- *What works* shows how many days each source usually takes to answer, and
  *Today* flags an application quiet for twice that as likely lost.
- *Today* lists drafted thank-you notes to review.
- Take a job out of the apply queue before it starts.
- Browser notifications, off by default: turn them on under Preferences →
  Automation to hear about new jobs, drafts and finished AI runs.
- `scoutjobs stats --funnel [--json]`, and the `daily` and `funnel` MCP tools.

### Fixed
- The calendar's interview card stopped drawing after *Reschedule*: the
  prep checklist and outcome form were missing.
- A prep pack ticked the prep items of an interview that was already over
  but had no outcome yet, instead of the one still to come.

## [0.7.0] - 2026-10-12

### Added
- New dashboard look: a violet brand, an overview that leads with the next
  best move and a *Today* list of what to do, a pipeline that fits every stage
  on screen, and a sidebar that folds to icons.
- Connections page: pick the AI assistant, connect mail and calendar and the
  job sources in one place, each with a *Test connection* button.
- Calendar sync through the AI assistant: interviews go to your calendar and
  your other events come in. Choose how far ahead it looks.
- Mail sync shows a progress bar in a dialog that can be cancelled; what was
  already found stays.
- Every page asks before it acts and shows a loading bar; the activity
  timeline and job events are written in plain language.
- Accessible tooltips on every hint: they show on hover and keyboard focus,
  close with Esc, and screen readers announce them. The folded sidebar names
  each icon.
- The overview asks how a past interview went, with a link to log the outcome.
- Read mail through your AI agent: on the Inbox page pick Claude Code or
  Codex, and it reads replies with the connectors you signed in to (Gmail and
  Google Calendar, Microsoft 365, any mail MCP server), logs them, adds
  interviews and calendar events, and files drafts. No Google Cloud project or
  password needed; nothing is sent. `scoutjobs email sync --via agent` does
  the same in a terminal. The Inbox's *Connect your email & calendar* panel offers
  it first, with the own Google client, IMAP (iCloud and others) and other MCP
  apps as the alternatives.
- Search finds the jobs that fit: with a filled `profile.md`, results are
  ranked by a match score with the reasons (target role, skills, level, work
  mode, place, avoided stacks), and *Hide jobs that break a must* drops the
  wrong place, a ruled-out engagement type or pay below the floor. The search
  page suggests searches built from the profile; `scoutjobs search` gains
  `--location`, `--hide-mismatches`, `--suggest` and `--rank`.
- Location filter: search by city, country or region (`Berlin`, `Germany`,
  `Europe`, `EMEA`); remote jobs match when open to it. The pipeline shows
  where each job is and filters by location and tag.
- More tags: every posting gets its skills, level, employment type, regions
  and visa stance from the title, tags and description, stored with the job on
  add. The search results have a tag bar to narrow the rows.
- Connect Google: one button for mail and calendar. The Inbox page walks
  through the Google Cloud setup with a link per step, watches Downloads for
  the client JSON and offers to import it, and after the sign-in says which
  boxes were left unticked on Google's screen.

### Changed
- The job page leads with the next step and folds the details; search and the
  interviews, learning, preferences and profile pages are simpler.
- Dates read "today", "yesterday" or "13 Oct" instead of `2026-10-13`. The
  weekly chart is labelled by week.
- Better contrast for muted text, underlined links in text, larger click
  targets, and filter chips that say what they do.
- Calendar and mail sync start at once and are capped in time.
- The boards of a search are queried in parallel.
- Gmail sync reads every folder except Sent, Drafts and the promotions and
  social tabs, so archived or filtered replies are found.

### Fixed
- The overview no longer cuts panels off on laptop screens; it scrolls.
- Interviews already past no longer show as upcoming.
- On Mondays the weekly applications chart counts the day's applications in
  the new week.
- Adding an interview at a time that already has one skips the duplicate.
- A page reloads when a sync ends on it, and mail sync passes the interview
  length on.
- Jobs added from a search keep the posting: its text is saved to
  `posting.md`, and Prepare application fetches a missing one from the job's
  URL (Remotive links included) before the agent starts, so the fit and draft
  reviews check the drafts against the posting, not the board's tags.
- Gmail replies to a draft sent from Gmail match the job again: Gmail gives
  the sent message a new Message-ID, so the draft's thread is recorded and a
  reply in it is matched. Mail from an applicant tracking system that names
  the company of exactly one job you applied to is matched too.
- Follow-up and thank-you drafts in Gmail answer the job's thread
  (`In-Reply-To`), and draft and message links open the connected account,
  not the first account signed in to the browser.
- A connection Google ended (Testing-mode apps expire after 7 days, revoked
  access) shows as such, with how to fix it, instead of a connected inbox
  that fails every sync. A Testing-mode app is warned about before it ends.
- Connecting with mail unticked on Google's consent screen no longer leaves
  the account unnamed, and reading or drafting says which permission is
  missing. The token is written atomically.

## [0.6.0] - 2026-10-11

### Added
- Document provenance (#32): the draft review, each PDF export and the ATS
  score record the SHA-256 of the files they covered in `provenance.json`.
  A changed file makes the step stale: Prepare application runs it again, the
  checklist and the last run show it, and `GET /api/jobs/{id}` returns the
  files and hashes. Replacing the exported PDF with your own final CV reopens
  the review and the ATS check, and a run never overwrites that file.
- `cv_source` and `cover_template` in config.yaml: the CV and cover letter
  every tailored document starts from. A PDF or Word source is never replaced
  by a PDF rendered from Markdown.
- Cover letters from a Word template (#36): with a `.docx` `cover_template`
  holding `{{slot}}` placeholders, the agent writes only the slot values and
  `scoutjobs cv cover <id>` fills a copy of the template, keeping its styles,
  page setup, headers and run formatting, and writes cover-letter.docx, the
  same words as cover-letter.md, and with LibreOffice cover-letter.pdf. It
  checks required slots and lengths, the employer, unstated recipients or
  addresses, leftover placeholders, design drift, the page count against the
  template and the PDF text layer. The dashboard shows the template check, and
  a `cover_template` gate blocks sending while it fails or is out of date.

### Fixed
- The job page records the agent results a stopped run left behind, so the
  run panel shows the steps as they stand now (#32).
- `install.sh` without Go installs the release of the checked-out tag (or
  `SCOUTJOBS_VERSION`) instead of the latest one, verifies its sha256, keeps
  the previous binary as `scoutjobs.prev`, and names the Go version `go.mod`
  needs. `scoutjobs update` refuses a release without `checksums.txt` and
  explains a tag whose binaries are not published yet (#35).
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

[Unreleased]: https://github.com/ZoolCoder/scoutjobs/compare/v0.11.0...HEAD
[0.11.0]: https://github.com/ZoolCoder/scoutjobs/compare/v0.10.0...v0.11.0
[0.10.0]: https://github.com/ZoolCoder/scoutjobs/compare/v0.9.0...v0.10.0
[0.9.0]: https://github.com/ZoolCoder/scoutjobs/compare/v0.8.0...v0.9.0
[0.8.0]: https://github.com/ZoolCoder/scoutjobs/compare/v0.7.0...v0.8.0
[0.7.0]: https://github.com/ZoolCoder/scoutjobs/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/ZoolCoder/scoutjobs/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/ZoolCoder/scoutjobs/releases/tag/v0.5.0
[0.4.0]: https://github.com/ZoolCoder/scoutjobs/releases/tag/v0.4.0
[0.3.2]: https://github.com/ZoolCoder/scoutjobs/releases/tag/v0.3.2
[0.3.1]: https://github.com/ZoolCoder/scoutjobs/releases/tag/v0.3.1
[0.3.0]: https://github.com/ZoolCoder/scoutjobs/releases/tag/v0.3.0
[0.2.0]: https://github.com/ZoolCoder/scoutjobs/releases/tag/v0.2.0
[0.1.0]: https://github.com/ZoolCoder/scoutjobs/releases/tag/v0.1.0
