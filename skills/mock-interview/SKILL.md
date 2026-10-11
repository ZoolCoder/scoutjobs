---
name: mock-interview
description: Runs a timed mock interview (behavioral, coding, or system design) with a realistic interviewer persona, then scores it and logs a practice session in ScoutJobs. Use when the user says "mock interview", "practice", "quiz me", "drill", or after interview-prep.
license: MIT
metadata:
  source: https://github.com/kirilxd/swe-interview-coach
  note: interviewer personas and problem library vendored under MIT; orchestration rewritten for ScoutJobs
---

# Mock interview

Three kinds. Pick from the user's words or ask once: `behavioral`, `coding`, `system_design`. Optional target: a job id (loads that application's `interview-prep.md` and `stories.md`) or a topic from the library.

Personas live in the plugin's `agents/` directory: `behavioral-interviewer.md`, `coding-interviewer.md`, `sysdesign-interviewer.md`. Where the platform can spawn subagents (Claude Code), spawn the persona with the inputs it lists. Elsewhere, read the file and embody it in the main session.

Problem library: `references/library/coding/*.md` (14 problems with hint ladders and reference solutions) and `references/library/sysdesign/*.md` (8 prompts). Never reveal a reference solution before the candidate yields.

## Flow

1. **Set up** (one message): kind, target company or topic, time box (behavioral 30 min, coding 35 min, system design 45 min), and for coding the language and a scratch file path under `$(scoutjobs path)/applications/<key>/scratch/` or the scratchpad.
2. **Interview**: the persona runs it. Stay in character. The candidate ends it by saying "done" or "yield".
3. **Debrief** (you, not the persona). Score 0-100 with the rubric below. Three strengths, three fixes, each tied to a moment in the transcript. For behavioral, name which STAR part was weak per answer. For coding, state complexity reached vs optimal and whether tests passed. For system design, list requirements missed and tradeoffs never mentioned.
4. **Log**:

```bash
scoutjobs session log --kind mock_behavioral --topic "<company or theme>" --score 72 --notes "strong on conflict story; results vague; rambled on Q3"
```

Kinds: `mock_behavioral`, `mock_coding`, `mock_sysdesign`. Add `--notes` that a future you can act on.

5. **Carry forward**: if a story was weak, edit `stories/<slug>.md` with the fix. If a coding pattern failed, `scoutjobs learn add --skill "<pattern>" --priority 1 --notes "failed in mock <date>"`.

## Rubric

| Kind | 40 | 30 | 30 |
|---|---|---|---|
| behavioral | STAR completeness and specificity | results with numbers, ownership clear | concision, handled follow-ups |
| coding | correctness (ran and passed) | approach: clarified, planned, complexity | communication under pressure |
| system_design | requirements and estimation | design coherence and tradeoffs | depth on the deep-dive, curveball handling |

Short drills are fine: "quiz me on 5 behavioral questions" uses the same log kind with `--topic drill`.
