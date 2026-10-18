---
name: cv-check
description: Scores the user's CV for ATS readiness and content quality, lists concrete gaps against their target roles (or one tracked job), and logs the result with `scoutjobs cv log` so it shows on the dashboard Profile page. Use when the user says "check my CV", "review my resume", "is my resume ATS friendly", "score my CV against this job", or after scoutjobs-setup when a CV file exists.
license: MIT
compatibility: Needs the scoutjobs binary. Reads files in ~/.scoutjobs/cv/ (PDF, DOCX, MD, TXT).
---

# CV check

Input: a CV file from `$(scoutjobs path)/cv/`, `profile.md`, `preferences.md`, optionally one tracked job. Output: two scores (0-100), a short verdict, a gap list, logged with `scoutjobs cv log`.

## 1. Find the CV

```bash
scoutjobs cv files --json
```

- Empty: tell the user to drop `resume.pdf` / `resume.docx` / `resume.md` into the printed `dir` (the folder has a README explaining what goes there). Stop.
- Only `resume.md`: `scoutjobs cv export` renders it as a single-column PDF in the same folder; offer it when the user needs a file to upload.
- Several files: ask which one, unless the user named it.
- Read the file. PDF and DOCX: use the platform's document reading; if it cannot be read, ask for a Markdown or text export. Never upload the file anywhere.
- A PDF: run `scoutjobs cv verify <file.pdf> --json` first. It checks the page count, the text layer an ATS reads, the e-mail address and date-range dashes on the machine, for no tokens. Use its findings for the layout, contact and hygiene items below instead of judging them yourself.

Also read `profile.md` (facts) and `preferences.md` (target roles, seniority, locations). With `--job <id>` or "against the Stripe job", read `scoutjobs job show <id> --json` and the saved `posting.md` in its `dir`.

## 2. ATS score (0-100)

Use the checklist in the `resume-ats-optimizer` skill (same plugin) as the rubric. Score from these, each with a short finding:

- Parseable layout: single column, no tables/text boxes/headers-footers for key data, standard section titles (Experience, Education, Skills), real text not images. (30)
- Contact and identity block complete and plain: name, email, phone, location, LinkedIn/GitHub URLs as text. (10)
- Keyword coverage: skills, tools and role titles that the target roles (or the given job) ask for appear verbatim, including acronyms and spelled-out forms. List the missing ones. (35)
- Dates and titles consistent, reverse chronological, no unexplained gaps larger than 6 months without a note. (15)
- File hygiene: sensible file name (`firstname-lastname-role.pdf`), under 2 pages for under 10 years, under 3 otherwise. (10)

## 3. Quality score (0-100)

- Impact: bullets start with a strong verb and carry a number or outcome. Count bullets with and without metrics. (35)
- Relevance: the top third of page one matches the target role; irrelevant early roles are compressed. (25)
- Truth: every claim exists in `profile.md`. Flag anything not in the profile as "unverified" and ask, never delete silently. (20)
- Clarity: no buzzword lists without evidence, no first person, consistent tense, no typos. (20)

## 4. Gaps

Up to 8 items, most valuable first, each one actionable in one line: "add 'Kubernetes' to Skills and the Acme bullet", "quantify the migration bullet", "move Education below Experience". Mark the ones that `resume-tailor` fixes automatically per job versus structural ones the user fixes once in the master CV.

## 5. Log and report

```bash
scoutjobs cv log --file resume.pdf --score 72 --ats 64 --summary "Solid impact bullets; layout uses a two-column table ATS parsers drop" --gaps "single column,add Kubernetes,quantify 3 bullets" [--job 12]
```

Show the user: both scores, the verdict, the gap list, then the next step: fix the structural gaps in the master CV, then run `resume-tailor` per job. Suggest `scoutjobs session log --kind review --topic "cv check"`.

Do not rewrite the CV here; `resume-tailor` does per-job rewrites and keeps every claim traceable to `profile.md`.
