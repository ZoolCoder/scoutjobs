---
description: Score new or given jobs 0-100 against profile and preferences, then ask apply/skip and log decisions
argument-hint: [job ids | all]
---

Use the `job-fit` skill from this plugin. Spawn the `fit-scorer` agent for batches of more than 3 jobs so posting text stays out of the main context, then run the apply/skip conversation yourself. For every job the user wants to pursue, continue with the `fit-review` skill so `fit-review.json` holds the evidence and wording behind the score.

Arguments: $ARGUMENTS
