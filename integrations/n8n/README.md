# ScoutJobs + n8n (or Make, Zapier, cron, anything with HTTP)

Four ways in, all local and opt-in:

1. **Pull**: the dashboard (`scoutjobs serve`) exposes a JSON API. Poll it on a schedule.
2. **Push**: set `webhook_url` in `~/.scoutjobs/config.yaml` and every event (job added, scored, status change, note, interview, cv_check) is POSTed as JSON the moment the CLI or dashboard records it.
3. **MCP**: `POST /mcp` speaks MCP over Streamable HTTP with the same tools as `scoutjobs mcp`, for n8n's MCP Client Tool node in an AI Agent workflow (see below).
4. **Calendar**: `GET /calendar.ics` is an iCalendar feed of interviews, deadlines and follow-ups.

## Setup

```yaml
# ~/.scoutjobs/config.yaml
api_token: change-me-long-random        # /api/* then needs "Authorization: Bearer change-me-long-random"
webhook_url: http://127.0.0.1:5678/webhook/scoutjobs   # your n8n Webhook node (test or production URL)
webhook_secret: another-long-random         # optional: every POST gets X-ScoutJobs-Signature (HMAC-SHA256 over "<X-ScoutJobs-Timestamp>.<body>") to verify in a Code node
```

Restart `scoutjobs serve`. The dashboard stays on `127.0.0.1`; if n8n runs in Docker, reach it via `host.docker.internal:7070` from the container, or run `scoutjobs serve --addr 0.0.0.0:7070` only on a trusted network with `api_token` set.

## API

| Method | Path | Body / query | Returns |
|---|---|---|---|
| GET | `/api/brief` | | today: applied this week vs target, follow-ups due, stale, upcoming interviews, new and drafted jobs |
| GET | `/api/stats` | | funnel and rates (same as `scoutjobs stats --json`) |
| GET | `/api/jobs` | `?status=new&q=stripe&open=1&limit=20` | job rows |
| POST | `/api/jobs` | `{"company","title","url","location","remote","source","posted_at","tags","salary_min","salary_max","currency","notes"}` | 201 created / 200 existing (same company and normalised title, or same posting URL) |
| GET | `/api/jobs/{id}` | | job, events, interviews |
| POST | `/api/jobs/{id}/status` | `{"status":"applied","reason":"..."}` | updated job; skip/withdraw/decline also log a decision |
| POST | `/api/jobs/{id}/note` | `{"note":"..."}` | 201 |
| GET | `/api/jobs/{id}/files` | | application folder (`files`: name, size, modified) and the kit (resume, cover, ats, draft, sent, done of 4) |
| GET | `/api/jobs/{id}/files/{name}` | | `{"name","size","body"}` for `.md`, `.txt`, `.json`, `.eml` |
| PUT | `/api/jobs/{id}/files/{name}` | raw text or `{"body":"..."}` | 201 new / 200 replaced; `.md`, `.txt`, `.json` only, 1 MB |
| POST | `/api/jobs/{id}/export` | `{"file":"resume.md"}` (optional) | 201 `{"file","bytes"}`: resume.pdf written next to the Markdown |
| POST | `/api/jobs/{id}/email/draft` | `{"to","subject","body","attach":["x.pdf"]}` (all optional) | 201, the message and the local send command; needs `cover-letter.md` |
| GET | `/api/profile` | | `profile`, `preferences`, `cv_dir`, `cv_files`, `latest_check` |
| POST | `/api/cv/checks` | `{"file","score","ats_score","summary","gaps","job_id"}` | 201 |
| GET | `/api/cv/checks` | `?limit=20` | check history |
| GET | `/api/events` | `?limit=50` | recent activity |
| POST | `/api/jobs/{id}/skip` | `{"reason":"...","signals":{...}}` | `job skip`: status skipped plus the decision the learner reads |
| POST | `/api/search` | `{"queries":["go"],"boards":["greenhouse:acme"],"remote":true,"limit":40,"add":true}` | `scoutjobs search`: `postings`, `added`, `errors`; omitted queries and boards fall back to `config.yaml` |
| GET, POST | `/api/interviews` | `?job=3&all=1`; POST `{"job_id","stage","at","format","interviewer","duration","location"}` | interviews (upcoming unless `all`); POST 201 and the job moves to `interview` when earlier |
| PATCH | `/api/interviews/{id}` | `{"at","format","interviewer","prep_status","outcome","feedback","stage","duration","location"}`, any subset | updated interview |
| GET | `/api/jobs/{id}/contacts` | | `{"company","contacts"}`: people from the LinkedIn import who work there |
| GET, POST | `/api/sessions` | `?limit=30`; POST `{"kind","topic","score","notes"}` | practice sessions, newest first; POST 201 |
| GET, POST | `/api/learning` | POST `{"skill","priority","resource","demand","notes"}` (upsert by skill) | learning items; POST 201 |
| PATCH | `/api/learning/{id}` | `{"status","resource","notes","hours","priority","demand"}`, `hours` is added | updated item |
| GET | `/api/preferences` | | stated and learned preferences |
| PUT, DELETE | `/api/preferences/{key}` | PUT `{"value","confidence","evidence","source"}` | the preference; DELETE 204 |
| GET, POST | `/api/decisions` | `?limit=50`; POST `{"job_id","kind","reason","signals"}` | decisions, newest first; POST 201 |

Sending e-mail is not on the API: `scoutjobs email send <id> --yes` runs on the machine that owns the SMTP password.

Webhook payload:

```json
{"event":"status","job_id":12,"company":"Stripe","title":"Backend Engineer","note":"ranked -> applied","at":"2026-10-11T09:30:00Z"}
```

## Example workflows

- `daily-brief.json`: every morning at 08:00, GET `/api/brief`, format a message, send it to Telegram/Slack/email (swap the last node).
- `daily-search.json`: weekdays at 07:30, POST `/api/search` with `add: true` (queries and boards from `config.yaml`), message the new postings when there are any.
- `high-fit-alert.json`: a webhook that fires on `scored` events with a fit of 80 or more, fetches the job and sends company, title, link and the dashboard address.
- `follow-up-nudge.json`: weekdays at 09:00, reads `follow_ups_due` and `stale` from `/api/brief` and sends one reminder list when either is non-empty.
- `gmail-to-note.json`: a Gmail trigger polls every 15 minutes for recruiter mail, matches the sender domain or company name to an open job, and adds a note to it. It only reads mail. The `inbox-triage` skill does the same with judgement and proposes status changes.
- `webhook-to-calendar.json`: receive ScoutJobs events; when `event` is `interview`, create a calendar entry; when `event` is `status` and the note ends in `applied`, start a 10-day follow-up timer.

Only one workflow can own `webhook_url`. To run `high-fit-alert` and `webhook-to-calendar` together, point `webhook_url` at one Webhook node and route by `event` with a Switch node, as `webhook-to-calendar.json` does.

Import via n8n → Workflows → Import from file, then set your token in the HTTP Request node credentials (Header Auth, name `Authorization`, value `Bearer <api_token>`).

## MCP from n8n

Set `api_token`, run `scoutjobs serve`, then in an AI Agent workflow add an **MCP Client Tool** node:

- Endpoint: `http://127.0.0.1:7070/mcp` (from Docker: `http://host.docker.internal:7070/mcp` with `serve --addr 0.0.0.0:7070`)
- Transport: HTTP Streamable
- Authentication: Bearer, the `api_token`

The agent then sees every `scoutjobs` tool (`job_list`, `brief`, `search`, `interview_add`, ...). Every request gets one JSON reply; the server opens no event stream. Tools run one at a time. Sending e-mail is not among them. A client that cannot set headers may pass `?token=<api_token>` instead; that puts the token in logs, so prefer the header. Without `api_token`, `/mcp` answers 403.

## Calendar feed

The dashboard's Calendar page shows the feed address. With `api_token` set it carries `?key=<hex>`, derived from the token. Add `&download=1` to get a file for a one-off import. Calendar apps on the same machine (Apple Calendar, Outlook) can subscribe to it directly. Google Calendar fetches feeds from Google's servers, so it needs a public address (run `serve` behind a tunnel) or use the `calendar-sync` skill with the Google Calendar connector instead.

Postings that arrive from other automations (an RSS node, a mail parser) go in through `POST /api/jobs` or `scoutjobs job import` with JSON lines on stdin.
