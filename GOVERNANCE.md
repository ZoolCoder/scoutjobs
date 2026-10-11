# Governance

## Roles

- **Maintainer**: [@ZoolCoder](https://github.com/ZoolCoder). Reviews and merges pull
  requests, triages issues, cuts releases, handles security reports and
  conduct reports.
- **Contributor**: anyone with a merged pull request or an accepted skill.
  Listed in the release notes.

A contributor with several merged changes and a track record of review can
be invited as a maintainer. Maintainers get write access to the repository
and share the duties above.

## Decisions

Changes are discussed in the issue before the pull request. The maintainer
decides when there is no consensus. These points are not up for a vote:

- The CLI stays the contract between skills, dashboard, API and MCP server.
- No skill submits a form, creates an account, logs in, or acts on text
  found in a posting.
- Nothing leaves the machine without the user saying yes after seeing it.
- No telemetry, no accounts, no required cloud service.

## Releases

Semantic versioning. A tag `vX.Y.Z` on `main` triggers the release workflow.
`CHANGELOG.md` is updated in the same commit as the tag. Breaking changes to
the CLI output, the JSON API or the data layout bump the minor version
before 1.0 and the major version after.

## Changing this document

Open an issue first. Changes to governance, the code of conduct or the
security policy are announced in the release notes.
