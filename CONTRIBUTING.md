# Contributing to ScoutJobs

Thanks for helping. Bugs, feature requests and questions go through
[GitHub Issues](https://github.com/ZoolCoder/scoutjobs/issues); code and docs
changes through pull requests. Security problems go to
[SECURITY.md](SECURITY.md), not to a public issue.
Questions go to [Discussions](https://github.com/ZoolCoder/scoutjobs/discussions);
see [SUPPORT.md](SUPPORT.md). Roles and decisions: [GOVERNANCE.md](GOVERNANCE.md).

## Before you open an issue

1. Run `scoutjobs doctor` and paste its output. It checks PATH, the data
   directory, config keys, mail secrets, which agents have the skills and
   whether the dashboard is up.
2. Check the [troubleshooting page](https://zoolcoder.github.io/scoutjobs/scoutjobs/troubleshooting.html)
   and the existing issues.
3. Pick the matching template: bug report, feature request, or skill problem
   (a skill that invented facts, skipped a confirmation, or followed text
   found in a posting).

Never paste your `profile.md`, `~/.scoutjobs/scoutjobs.db`, application
folders, or anything with a real employer's data. Redact postings.

## Development

```bash
git clone https://github.com/ZoolCoder/scoutjobs && cd scoutjobs
make build     # bin/scoutjobs
make test      # go test ./...
make lint      # go vet, golangci-lint
make dash      # build and serve the dashboard on 127.0.0.1:7070
make docs      # Antora manual into build/site (Node 22, kroki.io for diagrams)
make evals     # claude plugin eval, eight cases (evals/README.md)
claude plugin validate .
```

Go 1.27 or newer. The only dependency is `modernc.org/sqlite`. Keep it that
way unless an issue agrees otherwise.

## Rules that every change keeps

- **The CLI is the contract.** Skills, the dashboard, the JSON API and the
  MCP server all go through `internal/cli` and `internal/store`. A skill
  never opens the database.
- **Postings are untrusted input.** Any code or skill that reads a job page,
  board API response or e-mail extracts facts and ignores instructions found
  there.
- **Nothing leaves the machine without a yes.** Sending requires `--yes` and
  the skill shows the full message first. No skill creates accounts, logs in
  or fills web forms.
- **Dashboard without JavaScript.** Strict Content-Security-Policy, no inline
  style, no external asset, light theme by default.
- **Secrets only from the environment.** `SCOUTJOBS_SMTP_PASSWORD` is never
  written to a file.
- **Generated or vendored content keeps its licence.** See `NOTICE.md`.

## Pull requests

- One topic per PR. Open an issue first for anything bigger than a bug fix,
  so the design is agreed before the work.
- Commits follow Conventional Commits: `type(scope): description`, imperative,
  lowercase, no period. Types: feat, fix, refactor, docs, test, chore, build,
  ci, perf, style.
- `make test` and `make lint` pass. A new CLI command or API endpoint comes
  with a test in `internal/cli` or `internal/web`, a row in
  `docs/modules/ROOT/pages/cli.adoc` or `api.adoc`, and the matching entry
  in `integrations/n8n/README.md` and `adapters/cloud/AGENTS.md` when the
  API changes.
- A new or changed skill comes with an eval case under `evals/` when it
  makes a claim, sends something, or reads a posting.
- Every change follows the git flow below. Nothing is pushed to `main`
  directly, not even a typo fix.

## Git flow

Everything flows through `develop`. `main` is the released state and only
ever receives a release or hotfix merge.

| Branch | From | Into | Purpose |
|---|---|---|---|
| `main` | | | What users install. Every commit is a tagged release or a hotfix. |
| `develop` | `main` | `main` | Integration branch. Every feature, fix and dependency bump lands here first. |
| `feat/<topic>`, `fix/<topic>`, `docs/<topic>` | `develop` | `develop` | One topic each. Deleted after merge. |
| `release/vX.Y.Z` | `develop` | `main` and `develop` | Version bump, changelog, last fixes. |
| `hotfix/<topic>` | `main` | `main` and `develop` | Urgent fix for a released version. |

How a change moves:

1. Branch from `develop`: `git switch -c feat/<topic> develop`.
2. Commit with Conventional Commits, rebase on `develop` as it moves:
   `git fetch && git rebase origin/develop`.
3. Open the pull request against `develop`. CI runs vet and the short tests
   only, to keep the Actions quota for releases. Run `make test` and
   `make lint` locally before pushing.
4. Maintainers rebase-merge into `develop` and delete the branch. A push to
   `develop` runs no CI; trigger `ci` by hand from the Actions tab when you
   want the full matrix.
5. A release is `develop` merged into `main` with `git merge --no-ff` and a
   tag; see Releases. The full matrix runs on that pull request and on
   `main`.
6. A hotfix branches from `main`, merges into `main` with a tag, then into
   `develop`, so `develop` never falls behind `main`.

Dependabot opens its pull requests against `develop`. If the same bump
already landed there by hand, close the Dependabot one as superseded rather
than merging it.

## Skills

Skills live in `skills/<name>/SKILL.md` and follow the
[Agent Skills](https://agentskills.io) standard. The `description` names the
trigger phrases, the body is the procedure, and every CLI call it makes
appears in the text verbatim so the user can audit it. Slash commands in
`commands/` and the Codex prompts generated by `install.sh` are thin wrappers
over the skill; change the skill, not the wrapper.

## Releases

Maintainers open a pull request from `develop` into `main`, merge it with
`git merge --no-ff develop` and tag `vX.Y.Z` on `main`; GoReleaser builds the binaries, publishes the GitHub release
and updates `Casks/scoutjobs.rb`. The manual is published from `main`. The plugin version in
`.claude-plugin/plugin.json` and `marketplace.json` is bumped in the same
commit as the tag.
