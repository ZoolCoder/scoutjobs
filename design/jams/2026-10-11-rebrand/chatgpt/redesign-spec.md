# ChatGPT redesign spec (2026-10-11)

Source: ChatGPT chat "Design system critique and layouts", prompted with the light-mode Overview screenshot and the CSP constraints (no JS, no inline styles, no external assets, one stylesheet, light default).

## Tokens

Light (default): bg #F5F7F8, surface #FFFFFF, surface-subtle #EDF2F4, border #DCE4E8, text #172126, text-muted #63727B, brand #34F5C5, accent #0F766E, accent-subtle #E2F7F1, success #16794B, warning #946000, danger #B42335, info #315CCB.

Dark: bg #0B1114, surface #121B20, surface-subtle #19252A, border #2B393E, text #E8F0F2, text-muted #A9B8BE, brand #34F5C5, accent #5EEAD4, accent-subtle #163D37, success #62D6A1, warning #F3C66A, danger #FF7C87, info #8BAAFF.

Mint = brand mark and meaningful progress only. Deep teal = links, buttons, active nav. Dark ink on mint fills, white on teal.

Type (system stack): page title 24/32 650, section 15/22 600, card title 14/20 600, body 14/21, compact 13/18, label 11/16 600 uppercase .08em, KPI 28/34 650 tabular. Sentence case headings; uppercase only for small labels.

Spacing 4px scale. Radius 6 / 10 / 14 / pill. Elevation: 1px border, shadow 0 1px 2px rgba(18,32,38,.04) in light; surface contrast in dark.

## Components

Sidebar 224-240px, 7 destinations, active = pale teal surface + teal text + accent marker, theme switch + local-workspace indicator at bottom. Top bar 60-64px: title + one-line subtitle, date at right. KPI card >=100px, label / value / hint, max 1-2 highlighted. Panel: 1px border, 14px radius, 20px padding, header row with title, count, one action. Table 44-48px rows. Tags neutral, badges carry state (gray planned, teal active, green done, amber waiting, red error), always with text. Score ring 48-56px lists, 64px detail, conic-gradient, number centered. Meter 6px rounded track. Kanban column 240-260px min, header status + count, explicit empty state, horizontal scroll. Form controls 40-44px, 10px radius, visible focus. Empty state: icon, title, one sentence, one action.

## Overview critique

1. Eight equal KPI cards -> four primary (open jobs, applied this week vs target, upcoming interviews, response rate); streak / practice move to Learning.
2. Three equal columns -> give "next best move" and upcoming interviews the primary region; pipeline health and learning secondary.
3. Not actionable -> one recommended action with reason and a link.
4. Typography uniformly small and uppercase -> sentence-case section titles, stronger values.
5. Mint vs teal roles unclear -> teal structure, mint progress.
6. Local-first invisible -> persistent "local workspace" indicator in the shell.

Overview must answer: where does my search stand, what needs attention next, what can I do today.

## Overview layout (top to bottom)

1. Header: "Overview", subtitle "Your job search, at a glance.", date, "data stays on this device".
2. Next best move: full width, one action (strongest draft or CV issue), fit score, reason, link.
3. Four KPIs.
4. Two columns: Pipeline health (funnel meters, all statuses reachable) | Coming up (2-3 interviews with date, company, stage, prep state).
5. Two secondary panels: Learning progress (headline metric, one observation, link) | CV health (score ring, status, one finding, link).
6. Full-width low-emphasis: What ScoutJobs is learning (2-3 preferences with confidence, link).

Responsive: 2-col operational area >=1200, stack <1000, 16px gutters + stacked KPIs on mobile.

## Other pages

- Pipeline: header with total active + add action; toolbar search + filters; 8 ordered columns; card = company, title, score ring, work mode, next date, status; hired/skipped secondary; distinguish "no jobs in stage" from "no match".
- Job detail: breadcrumb, company, title, status badge, 64px score ring; primary column (65%) fit explanation, notes, interviews, history; secondary (35%) status form, dates, source, deadline, next action.
- Interviews: next-interview panel, upcoming list, past section, forms to set prep and outcome.
- Learning: top metrics (hours, sessions), prioritized skill gaps tied to demand, practice log with scores, add-hours form.
- Preferences: explanation line, table preference / confidence meter + number / evidence / source, decision signals grouped by kind, scorer disagreements with links.
- Profile & CV: profile status panel, CV files table, CV checks with one prominent ATS ring + findings, preferences in use; ATS score distinct from fit score.
- Activity: filter bar, newest-first stream grouped by day, event badge, entity link, note.

## Build order

Shell + tokens, Overview, Pipeline + Job, Interviews + Profile, Learning + Preferences, Activity + accessibility pass (focus, empty states, dark mode).
