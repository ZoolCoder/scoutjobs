# ChatGPT jam, 2026-10-11: what was used

Chat: "Redesign ScoutJobs Dashboard" (chatgpt.com, chat 6acac6ed).

## Logo

Chosen: ChatGPT "Neural Lens". A magnifier whose lens holds a four-point spark; mint #34F5C5 on dark, deep teal #0F766E on light. Rounds 1-4 in `images/03-06` were rejected (briefcase-ish, too busy, gradients). The transparent PNG lives in `../../../logo/icon-transparent.png`; the dashboard uses the inline SVG `mark` in `internal/web/templates/layout.html` and `mark.svg` / `mark-light.svg` as favicon and README header.

## Tokens and shell

`redesign-spec.md` is the spec ChatGPT wrote from the first critique: light default, mint only for the brand mark and meaningful progress, teal for links, buttons and active nav, 224px sidebar with theme switch and "local workspace" indicator, panels with 14px radius and a header row (title, count, one action).

## Pages, one mockup each

| Page | Mockup | Built |
|---|---|---|
| Overview | `images/07-page-overview.jpg` | `shots/01-overview.jpg` (before), next best move + 4 KPIs + pipeline health + coming up |
| Pipeline | `images/08-page-pipeline.jpg` | `shots/11-pipeline-new.jpg`, whole card opens the job |
| Job detail | `images/09-page-job.jpg` | `shots/04-job-new.jpg`, `shots/12-job-kit-dark.jpg` (get-selected checklist added after the jam) |
| Interviews | `images/10-page-interviews.jpg` | `shots/05-interviews-new.jpg` |
| Learning | `images/11-page-learning.jpg` | `shots/06-learning-new.jpg` |
| Preferences | `images/12-page-preferences.jpg` | `shots/07-preferences-new.jpg`, `shots/10-preferences-dark.jpg` |
| Profile & CV | `images/13-page-profile.jpg` | `shots/08-profile-new.jpg` |
| Activity | `images/14-page-activity.jpg` | `shots/09-activity-new.jpg` |

## Deviations

- No JavaScript, no inline styles, no external fonts: mockup gradients and charts became `<meter>`, conic-gradient gauges and CSS bars.
- Mockup page title "Profile & CV" is "Profile" in the header; the nav item says "Profile & CV".
- Kanban cards keep the status select; the mockup had a drag handle.
