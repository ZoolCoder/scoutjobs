# How ScoutJobs learns from you

ScoutJobs does not train a model. It keeps a ledger of your decisions and turns repeated patterns into explicit, editable preferences that every skill reads.

```
decision (skip / apply / withdraw / decline_offer / override_score)
   reason: your words          signals: {"remote":"onsite","stack":["java"],...}
          │
          ▼  scoutjobs decision summary
counts per kind, signal frequencies per kind, recent reasons, score overrides
          │
          ▼  learn-preferences skill (weekly, confirms with you)
preferences table: key, value, confidence, evidence, source=stated|learned
          │
          ▼  scoutjobs pref render
~/.scoutjobs/preferences.md   ← read by job-fit, job-search, resume-tailor, interview-prep
```

Rules that keep it honest:

- **Stated beats learned.** Anything you say directly is stored with `source: stated` and confidence 1. Inference never overwrites it.
- **Confidence is earned.** 3 supporting decisions → 0.5, 5 → 0.7, 8+ → 0.9, never above 0.95 for learned items. Contradicting evidence subtracts.
- **You confirm.** The skill proposes a table; you accept, edit or reject each row. Rejected proposals are not stored.
- **Only job attributes.** Stack, remote, location, seniority, company size, industry, salary, on-call, process, role family, language. Nothing about people.
- **The scorer can be corrected.** When your choices disagree with the fit score, `score_overrides` surfaces it and you can add a `scoring` preference that changes how `job-fit` weighs things.

Signals are free-form JSON but skills use a shared vocabulary so counts add up: `stack`, `remote`, `location`, `seniority`, `company_size`, `industry`, `salary_below_floor`, `on_call`, `visa`, `role_family`, `language`.

Everything is inspectable: `/preferences` on the dashboard shows the preferences and the raw signal counts behind them, and `scoutjobs export` dumps the whole ledger.
