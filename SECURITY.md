# Security policy

ScoutJobs runs on your machine with your job-search data, your SMTP account
and whatever an AI agent reads from job postings. We treat the following as
security issues:

- A way for text in a posting, board response or e-mail to make a skill, the
  CLI, the dashboard or the MCP server take an action the user did not
  confirm (send mail, write outside the application folder, change config).
- The dashboard or API reachable without the bearer token when `api_token`
  is set, or any Content-Security-Policy bypass.
- Secrets (`SCOUTJOBS_SMTP_PASSWORD`, `api_token`) written to disk, logged,
  or sent anywhere but the configured SMTP or IMAP server.
- Path traversal in the files API or the drafts viewer.

## Reporting

Do not open a public issue. Use
[GitHub private vulnerability reporting](https://github.com/ZoolCoder/scoutjobs/security/advisories/new)
or e-mail abdallah.coder@gmail.com with "scoutjobs security" in the subject.
Include the version (`scoutjobs version`), the platform, and steps or a
posting that reproduces it (redacted).

You get an acknowledgement within 7 days and a fix or a decision within 30.
Fixed issues are credited in the release notes unless you prefer not to be.

## Supported versions

The latest minor release. Older tags get no backports.
