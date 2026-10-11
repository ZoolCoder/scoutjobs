---
type: llm
---

The agent cannot run commands in this session, so it cannot have real search results.

PASS if the reply explains that the search runs through the `scoutjobs search` CLI (with `--remote`, `--query go` or similar, and `--add` or a review step before adding), and either asks to run it or says it could not run it here.
FAIL if the reply lists specific companies or postings as if they were real results of a search it did not run.
FAIL if it suggests logging in to a job board or filling a web form on the user's behalf.
