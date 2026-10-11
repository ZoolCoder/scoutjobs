---
name: upskill
description: Finds skill gaps between the user's profile and the jobs they track (especially ones they skipped, lost, or scored low on), ranks gaps by market demand, builds a learning plan with concrete resources, and tracks progress in ScoutJobs. Use when the user says "what should I learn", "skill gaps", "learning plan", "upskill", or weekly.
license: MIT
metadata:
  inspired-by: https://github.com/MadsLorentzen/ai-job-search (MIT); reimplemented on the scoutjobs tracker
---

# Upskill

Input: `scoutjobs job list --json` (all statuses), the posting text per job (`posting.md` in each application folder), `profile.md` Skills section, `scoutjobs learn list --json`, and recent mock results from `scoutjobs session list --json`.

## 1. Demand

For every tracked job, extract required and nice-to-have skills from the posting. Normalize names (k8s = Kubernetes, GCP = Google Cloud). Count, per skill: in how many jobs it appears, how many of those were `rejected`, `skipped` with a stack signal, or scored under 60 by `job-fit`. That is the demand signal.

## 2. Gap

Compare to the profile: Strong = no gap, Working = small gap, Learning or absent = gap. Also add anything `mock-interview` or `interview-debrief` logged with `--priority 1`.

## 3. Rank

Priority 1: gap present in 3 or more target jobs, or caused a rejection. Priority 2: in 2 jobs or nice-to-have in many. Priority 3: everything else. Respect preferences: a skill tied to a `stack_avoid` value is not a gap, it is a filter; say so.

Show a heatmap-style table: skill, demand count, your level, priority.

## 4. Plan

For the top 3 to 5 gaps, propose one resource each (official docs, a well-known course, a book, or a repo to build), an estimated hour budget, and a concrete proof of learning the user can put in their profile (a small project, a certification, a blog post). Search the web for current resources; prefer free and official.

Record:

```bash
scoutjobs learn add --skill Kubernetes --priority 1 --demand 7 --resource "https://kubernetes.io/docs/tutorials/" --notes "goal: deploy the scoutjobs dashboard to a local kind cluster"
```

## 5. Track

When the user reports study time: `scoutjobs learn set <id> --hours 1.5`. When done: `--status done`, then update the profile's Skills section (move the skill to Working or Strong) and tell the user to re-run `job-fit` on open jobs since the scores will change.

Weekly: `scoutjobs learn list` is on the dashboard at `/learning`; the `weekly-review` skill reads it.
