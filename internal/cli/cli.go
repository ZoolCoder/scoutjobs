// Package cli is the contract between agents (skills) and the store.
// Every command accepts --json so an agent can parse the result.
package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/zoolcoder/scoutjobs/internal/apply"
	"github.com/zoolcoder/scoutjobs/internal/store"
	"github.com/zoolcoder/scoutjobs/internal/web"
)

const usage = `scoutjobs - job search operations for the person looking for a job

Usage: scoutjobs <command> [subcommand] [flags]

  init                       create ~/.scoutjobs (config, profile, db)
  path                       print the data directory
  version                    print the version
  update [--check] [--yes] [--version vX.Y.Z]   install the latest release over this binary (brew/deb/rpm installs are handed to their manager)
  job add [--from url]|list [--compact]|show|set|skip|rm|import
  search [--query q] [--board greenhouse:slug] [--remote] [--add]
  cv files|log|list|export   resume files in ~/.scoutjobs/cv, CV check results, resume.md to PDF
  email draft|show|send   application e-mail from the cover letter; send needs --yes
  email test|sync         probe the SMTP/IMAP account; log inbox replies to applications
  decision log|summary
  pref set|list|rm|render
  interview add|list|set     --duration 45 --location <meeting link or address>
  linkedin import <zip|dir>  LinkedIn data export: profile for setup, connections for referrals
  contacts <job>|--company x who you know at a company (from the LinkedIn import)
  session log|list
  learn add|list|set
  stats                      funnel, weekly numbers, follow-ups due
  brief [--compact]          everything the daily briefing needs (json)
  export                     full json dump
  serve [--addr 127.0.0.1:7070]   dashboard, calendar feed, JSON API, MCP over HTTP on /mcp (needs api_token)
  mcp                        MCP server on stdio for Claude Desktop, Codex, Cursor (every tool is a CLI command)
  doctor [--online]          check PATH, data dir, config keys, mail secrets, linked skills, dashboard; --online compares with the latest release

Add --json to any read command. SCOUTJOBS_HOME overrides the data dir.
`

func Run(args []string, out io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(out, usage)
		return nil
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "init":
		return runInit(out)
	case "path":
		fmt.Fprintln(out, store.Home())
		return nil
	case "version":
		fmt.Fprintln(out, "scoutjobs", Version)
		return nil
	case "update":
		return runUpdate(rest, out)
	case "mcp":
		return runMCP(os.Stdin, out)
	case "doctor":
		return runDoctor(rest, out)
	}
	s, err := store.Open()
	if err != nil {
		return err
	}
	defer s.Close()
	cfg := LoadConfig(s.Home)
	s.Notify = webhook(cfg.WebhookURL, cfg.WebhookSecret)
	s.Currency = cfg.Currency
	switch cmd {
	case "job":
		if len(rest) > 0 && rest[0] == "import" {
			return runImport(s, rest[1:], out)
		}
		return runJob(s, rest, out)
	case "search":
		return runSearch(s, cfg, rest, out)
	case "cv":
		return runCV(s, rest, out)
	case "email":
		return runEmail(s, cfg, rest, out)
	case "decision":
		return runDecision(s, rest, out)
	case "pref":
		return runPref(s, rest, out)
	case "interview":
		return runInterview(s, rest, out)
	case "session":
		return runSession(s, rest, out)
	case "learn":
		return runLearn(s, rest, out)
	case "linkedin":
		return runLinkedIn(s, rest, out)
	case "contacts":
		return runContacts(s, rest, out)
	case "stats":
		return runStats(s, rest, out)
	case "brief":
		return runBrief(s, rest, out)
	case "export":
		return runExport(s, out)
	case "serve":
		fs := flag.NewFlagSet("serve", flag.ContinueOnError)
		addr := fs.String("addr", LoadConfig(store.Home()).DashboardAddr, "listen address")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		fmt.Fprintf(out, "ScoutJobs dashboard on http://%s  (data: %s)\n", *addr, s.Home)
		return web.Serve(s, *addr, web.Config{FollowUpDays: cfg.FollowUpDays, StaleDays: cfg.StaleDays, WeeklyApplyTarget: cfg.WeeklyApplyTarget, APIToken: cfg.APIToken, Theme: cfg.Theme, EmailFrom: cfg.EmailFrom, SearchQueries: cfg.SearchQueries, SearchBoards: cfg.SearchBoards, SearchRemote: cfg.SearchRemote, Currency: cfg.Currency, Version: Version, MCP: mcpHTTP()})
	}
	return fmt.Errorf("unknown command %q\n%s", cmd, usage)
}

// Version is set by -ldflags at release time.
var Version = "dev"

// parse accepts "<positional...> --flags" and "--flags <positional...>" alike,
// because the flag package stops at the first non-flag argument.
func parse(fs *flag.FlagSet, rest []string) ([]string, error) {
	var pos []string
	for len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		pos = append(pos, rest[0])
		rest = rest[1:]
	}
	if err := fs.Parse(rest); err != nil {
		return nil, err
	}
	return append(pos, fs.Args()...), nil
}

func sub(rest []string) (string, []string) {
	if len(rest) == 0 {
		return "", nil
	}
	return rest[0], rest[1:]
}

func emit(out io.Writer, asJSON bool, v any, human func()) error {
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	human()
	return nil
}

func table(out io.Writer, header string, rows [][]string) {
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, header)
	for _, r := range rows {
		fmt.Fprintln(tw, strings.Join(r, "\t"))
	}
	tw.Flush()
}

func trunc(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func fit(score int64) string {
	if score < 0 {
		return "-"
	}
	return fmt.Sprint(score)
}

// ---- init ----

const profileTemplate = `# Profile

Filled by the scoutjobs-setup skill. Keep it factual; every claim here can end up in a CV.

## Identity
- Name:
- Location / time zone:
- Work authorization:
- Links: (GitHub, LinkedIn, portfolio)

## Target
- Roles:
- Seniority:
- Remote / hybrid / onsite:
- Locations or countries:
- Salary floor (currency):
- Deal-breakers:

## Experience
(reverse chronological: title, company, dates, 3-5 outcome bullets with numbers)

## Skills
- Strong:
- Working:
- Learning:

## Education & certifications

## Projects

## Languages (spoken)

## Stories (STAR bank index)
(ids and one-line summaries; full stories live in stories/)
`

const preferencesTemplate = `# Preferences

Rendered by ` + "`scoutjobs pref render`" + ` from the preferences table. Edit with ` + "`scoutjobs pref set`" + `, not by hand.
Nothing learned yet. Decisions you make (skip, apply, decline) teach this file.
`

const configTemplate = `# ScoutJobs config. Everything here has a sane default.
follow_up_days: 10      # quiet applications older than this get a follow-up suggestion
stale_days: 60          # quiet applications older than this get a stale sweep
weekly_apply_target: 5  # applications per week the dashboard measures against
fit_apply_floor: 60     # the job-fit skill suggests skipping below this score
currency: EUR           # salaries converted to yearly figures in this currency, original kept in notes
dashboard_addr: 127.0.0.1:7070
theme: light            # dashboard: light | dark | auto (follow system)

# job search defaults for ` + "`scoutjobs search`" + ` (flags override)
search_queries: senior backend engineer, go developer
search_boards:          # company boards: greenhouse:stripe, lever:netlify, ashby:linear, workable:acme, smartrecruiters:Acme, personio:acme, rss:https://...
search_remote: true     # also query Remotive, RemoteOK, Arbeitnow, Jobicy, Himalayas

# sending applications by e-mail (scoutjobs email send). Leave empty to keep it off.
email_from:             # you@example.com; also the SMTP login unless smtp_user is set
smtp_host:              # smtp.gmail.com:587 (STARTTLS) or host:465 (TLS); password comes from $SCOUTJOBS_SMTP_PASSWORD
smtp_user:
imap_host:              # imap.gmail.com:993; lets "scoutjobs email sync" log replies to your applications
imap_user:              # defaults to email_from; password from $SCOUTJOBS_IMAP_PASSWORD (falls back to $SCOUTJOBS_SMTP_PASSWORD)

# automation (n8n, Make, scripts). Leave empty to disable.
api_token:              # when set, /api/* requires "Authorization: Bearer <token>"
webhook_url:            # every event is POSTed here as JSON (job added, status change, note, cv_check, ...)
webhook_secret:         # optional; signs every POST (X-ScoutJobs-Signature, HMAC-SHA256) so the receiver can verify it
`

const cvReadme = `# Your CV and assets

Put your resume files here (PDF, DOCX, MD) plus anything a skill may need: portfolio links,
certificates, a headshot. Skills read this folder; nothing is uploaded anywhere.

- ` + "`resume.md`" + ` or ` + "`resume.pdf`" + `: the master CV. ` + "`resume-tailor`" + ` derives per-job versions into applications/<job>/.
- ` + "`profile.md`" + ` (one level up) is the source of truth for facts; keep the two in sync.
- Run the ` + "`cv-check`" + ` skill ("check my CV") for an ATS and quality score; results show on the dashboard Profile page.
`

func runInit(out io.Writer) error {
	home := store.Home()
	if err := os.MkdirAll(filepath.Join(home, "applications"), 0o700); err != nil {
		return err
	}
	for _, d := range []string{"stories", "reviews", "cv"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o700); err != nil {
			return err
		}
	}
	files := map[string]string{
		"profile.md":     profileTemplate,
		"preferences.md": preferencesTemplate,
		"config.yaml":    configTemplate,
		"cv/README.md":   cvReadme,
	}
	for name, body := range files {
		p := filepath.Join(home, name)
		if _, err := os.Stat(p); err == nil {
			fmt.Fprintln(out, "exists ", p)
			continue
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			return err
		}
		fmt.Fprintln(out, "created", p)
	}
	s, err := store.Open()
	if err != nil {
		return err
	}
	s.Close()
	fmt.Fprintln(out, "db     ", filepath.Join(home, "scoutjobs.db"))
	fmt.Fprintln(out, "\nNext: run the scoutjobs-setup skill in your agent to fill profile.md, then `scoutjobs serve`.")
	return nil
}

// ---- job ----

func runJob(s *store.Store, rest []string, out io.Writer) error {
	sc, rest := sub(rest)
	switch sc {
	case "add":
		fs := flag.NewFlagSet("job add", flag.ContinueOnError)
		var j store.Job
		fs.StringVar(&j.Company, "company", "", "company (required)")
		fs.StringVar(&j.Title, "title", "", "job title (required)")
		fs.StringVar(&j.URL, "url", "", "posting url")
		fs.StringVar(&j.Location, "location", "", "location")
		fs.StringVar(&j.Remote, "remote", "", "remote|hybrid|onsite|unknown")
		fs.StringVar(&j.Source, "source", "", "linkedin|greenhouse|lever|ashby|remotive|manual|...")
		fs.StringVar(&j.PostedAt, "posted", "", "YYYY-MM-DD")
		fs.StringVar(&j.Deadline, "deadline", "", "YYYY-MM-DD")
		fs.StringVar(&j.Currency, "currency", "", "salary currency")
		fs.Int64Var(&j.SalaryMin, "salary-min", 0, "")
		fs.Int64Var(&j.SalaryMax, "salary-max", 0, "")
		fs.StringVar(&j.Tags, "tags", "", "comma separated")
		fs.StringVar(&j.Notes, "notes", "", "free text")
		fs.StringVar(&j.Contact, "contact", "", "application e-mail address (recruiter or jobs@)")
		fs.StringVar(&j.Status, "status", "", "initial status (default new)")
		from := fs.String("from", "", "posting URL on a supported ATS (Greenhouse, Lever, Ashby, Workable, SmartRecruiters, Personio, Recruitee, BambooHR): fills the fields you leave empty")
		asJSON := fs.Bool("json", false, "")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if *from != "" {
			if err := fillFromPosting(&j, *from); err != nil {
				return err
			}
		}
		if j.Company == "" {
			return errors.New("--company is required")
		}
		if j.Title == "" {
			return errors.New("--title is required")
		}
		job, created, err := s.UpsertJob(j)
		if err != nil {
			return err
		}
		return emit(out, *asJSON, map[string]any{"created": created, "job": job}, func() {
			verb := "exists "
			if created {
				verb = "added  "
			}
			fmt.Fprintf(out, "%s #%d %s / %s\n", verb, job.ID, job.Company, job.Title)
		})
	case "list":
		fs := flag.NewFlagSet("job list", flag.ContinueOnError)
		var f store.JobFilter
		fs.StringVar(&f.Status, "status", "", "filter by status")
		fs.BoolVar(&f.Open, "open", false, "only open applications")
		fs.IntVar(&f.Limit, "limit", 0, "max rows")
		fs.StringVar(&f.Query, "q", "", "search company/title/tags")
		asJSON := fs.Bool("json", false, "")
		compact := fs.Bool("compact", false, "with --json: id, company, title, status, fit, remote, updated only")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		jobs, err := s.ListJobs(f)
		if err != nil {
			return err
		}
		if *asJSON && *compact {
			rows := make([]map[string]any, 0, len(jobs))
			for _, j := range jobs {
				rows = append(rows, map[string]any{"id": j.ID, "company": j.Company, "title": j.Title, "status": j.Status, "fit": j.FitScore, "veto": j.Veto, "remote": j.Remote, "updated": j.UpdatedAt[:10]})
			}
			return emit(out, true, rows, nil)
		}
		return emit(out, *asJSON, jobs, func() {
			var rows [][]string
			for _, j := range jobs {
				score := fit(j.FitScore)
				if j.Veto != "" {
					score = "veto"
				}
				rows = append(rows, []string{fmt.Sprint(j.ID), j.Status, score, j.Company, trunc(j.Title, 40), j.Remote, j.UpdatedAt[:10]})
			}
			table(out, "ID\tSTATUS\tFIT\tCOMPANY\tTITLE\tREMOTE\tUPDATED", rows)
		})
	case "show":
		fs := flag.NewFlagSet("job show", flag.ContinueOnError)
		asJSON := fs.Bool("json", false, "")
		pos, err := parse(fs, rest)
		if err != nil {
			return err
		}
		if len(pos) == 0 {
			return errors.New("usage: job show <id|company/title>")
		}
		j, err := s.ResolveJob(pos[0])
		if err != nil {
			return err
		}
		events, _ := s.Events(j.ID, 50)
		ivs, _ := s.Interviews(j.ID, false)
		return emit(out, *asJSON, map[string]any{"job": j, "events": events, "interviews": ivs, "dir": appDir(s, j)}, func() {
			fmt.Fprintf(out, "#%d %s / %s\nstatus: %s  fit: %s  remote: %s  source: %s\nurl: %s\nlocation: %s  deadline: %s\ntags: %s\nnotes: %s\nfit notes: %s\ndir: %s\n",
				j.ID, j.Company, j.Title, j.Status, fit(j.FitScore), j.Remote, j.Source, j.URL, j.Location, j.Deadline, j.Tags, j.Notes, j.FitNotes, appDir(s, j))
			if j.Veto != "" {
				fmt.Fprintf(out, "VETO: %s\n", j.Veto)
			}
			if len(ivs) > 0 {
				fmt.Fprintln(out, "\ninterviews:")
				for _, i := range ivs {
					fmt.Fprintf(out, "  #%d %s %s %s prep:%s outcome:%s\n", i.ID, i.Stage, i.At, i.Format, i.PrepStatus, i.Outcome)
				}
			}
			fmt.Fprintln(out, "\nhistory:")
			for _, e := range events {
				fmt.Fprintf(out, "  %s %-18s %s\n", e.At[:10], e.Kind, trunc(e.Note, 70))
			}
		})
	case "set":
		fs := flag.NewFlagSet("job set", flag.ContinueOnError)
		var p store.JobPatch
		strFlag := func(name, help string) *string { return fs.String(name, "\x00", help) }
		status := strFlag("status", strings.Join(store.Statuses, "|"))
		url := strFlag("url", "")
		location := strFlag("location", "")
		remote := strFlag("remote", "")
		currency := strFlag("currency", "")
		source := strFlag("source", "")
		posted := strFlag("posted", "")
		deadline := strFlag("deadline", "")
		fitNotes := strFlag("fit-notes", "why the score is what it is")
		tags := strFlag("tags", "")
		notes := strFlag("notes", "")
		contact := strFlag("contact", "application e-mail address")
		veto := strFlag("veto", "hard eligibility failure, blocks sending; --veto '' clears it")
		score := fs.Int64("score", -2, "fit score 0-100")
		smin := fs.Int64("salary-min", -1, "")
		smax := fs.Int64("salary-max", -1, "")
		note := fs.String("note", "", "append an event note")
		pos, err := parse(fs, rest)
		if err != nil {
			return err
		}
		if len(pos) == 0 {
			return errors.New("usage: job set <id> --status applied [--contact hr@x.com] [--note text]")
		}
		j, err := s.ResolveJob(pos[0])
		if err != nil {
			return err
		}
		pick := func(v *string) *string {
			if *v == "\x00" {
				return nil
			}
			return v
		}
		p.Status, p.URL, p.Location, p.Remote, p.Currency, p.Source, p.PostedAt, p.Deadline, p.FitNotes, p.Tags, p.Notes, p.Contact, p.Veto =
			pick(status), pick(url), pick(location), pick(remote), pick(currency), pick(source), pick(posted), pick(deadline), pick(fitNotes), pick(tags), pick(notes), pick(contact), pick(veto)
		if *score >= -1 {
			p.FitScore = score
		}
		if *smin >= 0 {
			p.SalaryMin = smin
		}
		if *smax >= 0 {
			p.SalaryMax = smax
		}
		if err := s.UpdateJob(j.ID, p); err != nil {
			return err
		}
		if *note != "" {
			_ = s.AddEvent(j.ID, "note", *note)
		}
		fmt.Fprintf(out, "updated #%d %s / %s\n", j.ID, j.Company, j.Title)
		return nil
	case "skip":
		fs := flag.NewFlagSet("job skip", flag.ContinueOnError)
		reason := fs.String("reason", "", "why (required, this is what the learner reads)")
		signals := fs.String("signals", "{}", `json, e.g. {"remote":"onsite","stack":["java"],"salary_below_floor":true}`)
		pos, err := parse(fs, rest)
		if err != nil {
			return err
		}
		if len(pos) == 0 || *reason == "" {
			return errors.New("usage: job skip <id> --reason '...' [--signals '{...}']")
		}
		j, err := s.ResolveJob(pos[0])
		if err != nil {
			return err
		}
		st := "skipped"
		if err := s.UpdateJob(j.ID, store.JobPatch{Status: &st}); err != nil {
			return err
		}
		if err := s.AddDecision(j.ID, "skip", *reason, *signals); err != nil {
			return err
		}
		fmt.Fprintf(out, "skipped #%d %s / %s\n", j.ID, j.Company, j.Title)
		return nil
	case "fit":
		return runJobFit(s, rest, out)
	case "review":
		return runJobReview(s, rest, out)
	case "check":
		return runJobCheck(s, rest, out)
	case "rm":
		if len(rest) == 0 {
			return errors.New("usage: job rm <id>")
		}
		j, err := s.ResolveJob(rest[0])
		if err != nil {
			return err
		}
		if err := s.DeleteJob(j.ID); err != nil {
			return err
		}
		fmt.Fprintf(out, "removed #%d\n", j.ID)
		return nil
	}
	return errors.New("usage: job add|list|show|set|fit|check|review|skip|rm")
}

func appDir(s *store.Store, j store.Job) string { return apply.Dir(s, j) }

// ---- decision ----

func runDecision(s *store.Store, rest []string, out io.Writer) error {
	sc, rest := sub(rest)
	switch sc {
	case "log":
		fs := flag.NewFlagSet("decision log", flag.ContinueOnError)
		kind := fs.String("kind", "", "skip|apply|withdraw|accept_offer|decline_offer|override_score|other")
		reason := fs.String("reason", "", "why, in the user's words")
		signals := fs.String("signals", "{}", "json object of facts about the job that drove this")
		job := fs.String("job", "", "job id or company/title (optional)")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if *kind == "" {
			return errors.New("--kind required")
		}
		var jobID int64
		if *job != "" {
			j, err := s.ResolveJob(*job)
			if err != nil {
				return err
			}
			jobID = j.ID
		}
		if !json.Valid([]byte(*signals)) {
			return errors.New("--signals must be valid json")
		}
		if err := s.AddDecision(jobID, *kind, *reason, *signals); err != nil {
			return err
		}
		fmt.Fprintln(out, "logged", *kind)
		return nil
	case "summary":
		fs := flag.NewFlagSet("decision summary", flag.ContinueOnError)
		limit := fs.Int("limit", 200, "")
		asJSON := fs.Bool("json", false, "")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		ds, err := s.DecisionSummary(*limit)
		if err != nil {
			return err
		}
		return emit(out, *asJSON, ds, func() {
			for k, n := range ds.Counts {
				fmt.Fprintf(out, "%-14s %d\n", k, n)
				for sig, c := range ds.Signals[k] {
					fmt.Fprintf(out, "    %-40s %d\n", sig, c)
				}
			}
			if len(ds.ScoreGap) > 0 {
				fmt.Fprintln(out, "\nscorer disagreed with you on:")
				for _, g := range ds.ScoreGap {
					fmt.Fprintf(out, "  #%d %s / %s fit=%d you=%s\n", g.JobID, g.Company, g.Title, g.FitScore, g.Kind)
				}
			}
		})
	case "list":
		decs, err := s.Decisions(50)
		if err != nil {
			return err
		}
		var rows [][]string
		for _, d := range decs {
			rows = append(rows, []string{d.At[:10], d.Kind, trunc(d.Company+" / "+d.Title, 40), trunc(d.Reason, 60)})
		}
		table(out, "DATE\tKIND\tJOB\tREASON", rows)
		return nil
	}
	return errors.New("usage: decision log|summary|list")
}

// ---- pref ----

func runPref(s *store.Store, rest []string, out io.Writer) error {
	sc, rest := sub(rest)
	switch sc {
	case "set":
		fs := flag.NewFlagSet("pref set", flag.ContinueOnError)
		conf := fs.Float64("confidence", 0.5, "0-1")
		evidence := fs.String("evidence", "", "what taught this")
		source := fs.String("source", "learned", "stated|learned")
		pos, err := parse(fs, rest)
		if err != nil {
			return err
		}
		if len(pos) < 2 {
			return errors.New("usage: pref set <key> <value> [--confidence 0.8 --evidence '...' --source stated]")
		}
		p := store.Preference{Key: pos[0], Value: strings.Join(pos[1:], " "), Confidence: *conf, Evidence: *evidence, Source: *source}
		if err := s.SetPreference(p); err != nil {
			return err
		}
		fmt.Fprintf(out, "set %s = %s (%.2f, %s)\n", p.Key, p.Value, p.Confidence, p.Source)
		return renderPrefs(s)
	case "rm":
		if len(rest) == 0 {
			return errors.New("usage: pref rm <key>")
		}
		if err := s.DeletePreference(rest[0]); err != nil {
			return err
		}
		return renderPrefs(s)
	case "list":
		fs := flag.NewFlagSet("pref list", flag.ContinueOnError)
		asJSON := fs.Bool("json", false, "")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		prefs, err := s.Preferences()
		if err != nil {
			return err
		}
		return emit(out, *asJSON, prefs, func() {
			var rows [][]string
			for _, p := range prefs {
				rows = append(rows, []string{p.Key, trunc(p.Value, 40), fmt.Sprintf("%.2f", p.Confidence), p.Source, trunc(p.Evidence, 50)})
			}
			table(out, "KEY\tVALUE\tCONF\tSOURCE\tEVIDENCE", rows)
		})
	case "render":
		if err := renderPrefs(s); err != nil {
			return err
		}
		fmt.Fprintln(out, "wrote", filepath.Join(s.Home, "preferences.md"))
		return nil
	}
	return errors.New("usage: pref set|list|rm|render")
}

// renderPrefs writes preferences.md, the file skills load before scoring or tailoring.
func renderPrefs(s *store.Store) error {
	prefs, err := s.Preferences()
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("# Preferences\n\nRendered by `scoutjobs pref render`. Edit with `scoutjobs pref set <key> <value>`.\n")
	b.WriteString("Stated = the user said so. Learned = inferred from decisions; confidence shows how sure.\n\n")
	section := func(title, source string) {
		var any bool
		for _, p := range prefs {
			if p.Source != source {
				continue
			}
			if !any {
				b.WriteString("## " + title + "\n\n")
				any = true
			}
			fmt.Fprintf(&b, "- **%s**: %s", p.Key, p.Value)
			if source == "learned" {
				fmt.Fprintf(&b, " _(confidence %.0f%%)_", p.Confidence*100)
			}
			if p.Evidence != "" {
				fmt.Fprintf(&b, " — %s", p.Evidence)
			}
			b.WriteString("\n")
		}
		if any {
			b.WriteString("\n")
		}
	}
	section("Stated", "stated")
	section("Learned", "learned")
	if len(prefs) == 0 {
		b.WriteString("Nothing learned yet. Decisions you make (skip, apply, decline) teach this file.\n")
	}
	return os.WriteFile(filepath.Join(s.Home, "preferences.md"), []byte(b.String()), 0o600)
}

// ---- interview ----

func runInterview(s *store.Store, rest []string, out io.Writer) error {
	sc, rest := sub(rest)
	switch sc {
	case "add":
		fs := flag.NewFlagSet("interview add", flag.ContinueOnError)
		var i store.Interview
		fs.StringVar(&i.Stage, "stage", "", "screen|technical|system_design|behavioral|case|onsite|final|other (required)")
		fs.StringVar(&i.At, "at", "", "YYYY-MM-DD or YYYY-MM-DDTHH:MM")
		fs.StringVar(&i.Format, "format", "", "phone|video|onsite")
		fs.StringVar(&i.Interviewer, "interviewer", "", "names / titles")
		fs.Int64Var(&i.Duration, "duration", 60, "minutes")
		fs.StringVar(&i.Location, "location", "", "meeting link or address")
		pos, err := parse(fs, rest)
		if err != nil {
			return err
		}
		if len(pos) == 0 || i.Stage == "" {
			return errors.New("usage: interview add <job> --stage technical --at 2026-10-20T14:00")
		}
		j, err := s.ResolveJob(pos[0])
		if err != nil {
			return err
		}
		i.JobID = j.ID
		id, err := s.AddInterview(i)
		if err != nil {
			return err
		}
		if j.Status == "applied" || j.Status == "drafted" || j.Status == "new" || j.Status == "ranked" {
			st := "interview"
			_ = s.UpdateJob(j.ID, store.JobPatch{Status: &st})
		}
		fmt.Fprintf(out, "interview #%d added for #%d %s\n", id, j.ID, j.Company)
		return nil
	case "list":
		fs := flag.NewFlagSet("interview list", flag.ContinueOnError)
		all := fs.Bool("all", false, "include past")
		asJSON := fs.Bool("json", false, "")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		ivs, err := s.Interviews(0, !*all)
		if err != nil {
			return err
		}
		return emit(out, *asJSON, ivs, func() {
			var rows [][]string
			for _, i := range ivs {
				rows = append(rows, []string{fmt.Sprint(i.ID), i.At, fmt.Sprintf("%dm", i.Duration), i.Company, i.Stage, i.Format, i.PrepStatus, i.Outcome})
			}
			table(out, "ID\tWHEN\tLENGTH\tCOMPANY\tSTAGE\tFORMAT\tPREP\tOUTCOME", rows)
		})
	case "set":
		fs := flag.NewFlagSet("interview set", flag.ContinueOnError)
		str := func(n, h string) *string { return fs.String(n, "\x00", h) }
		at, format, who, prep, outcome, feedback, stage := str("at", ""), str("format", ""), str("interviewer", ""), str("prep-status", "todo|in_progress|ready"), str("outcome", "passed|failed|pending|cancelled"), str("feedback", "what they said / what you learned"), str("stage", "")
		location := str("location", "meeting link or address")
		duration := fs.Int64("duration", 0, "minutes")
		pos, err := parse(fs, rest)
		if err != nil {
			return err
		}
		if len(pos) == 0 {
			return errors.New("usage: interview set <id> --outcome passed --feedback '...'")
		}
		var id int64
		if _, err := fmt.Sscan(pos[0], &id); err != nil {
			return err
		}
		pick := func(v *string) *string {
			if *v == "\x00" {
				return nil
			}
			return v
		}
		p := store.InterviewPatch{At: pick(at), Format: pick(format), Interviewer: pick(who), PrepStatus: pick(prep), Outcome: pick(outcome), Feedback: pick(feedback), Stage: pick(stage), Location: pick(location), Duration: duration}
		if err := s.UpdateInterview(id, p); err != nil {
			return err
		}
		fmt.Fprintf(out, "updated interview #%d\n", id)
		return nil
	}
	return errors.New("usage: interview add|list|set")
}

// ---- session ----

func runSession(s *store.Store, rest []string, out io.Writer) error {
	sc, rest := sub(rest)
	switch sc {
	case "log":
		fs := flag.NewFlagSet("session log", flag.ContinueOnError)
		var k store.Session
		fs.StringVar(&k.Kind, "kind", "", "mock_behavioral|mock_sysdesign|mock_coding|drill|study|review (required)")
		fs.StringVar(&k.Topic, "topic", "", "company, pattern, or subject")
		fs.Int64Var(&k.Score, "score", -1, "0-100 if graded")
		fs.StringVar(&k.Notes, "notes", "", "what went well / what to fix")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if k.Kind == "" {
			return errors.New("--kind required")
		}
		if err := s.AddSession(k); err != nil {
			return err
		}
		fmt.Fprintln(out, "logged", k.Kind, k.Topic)
		return nil
	case "list":
		fs := flag.NewFlagSet("session list", flag.ContinueOnError)
		asJSON := fs.Bool("json", false, "")
		limit := fs.Int("limit", 30, "")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		ks, err := s.Sessions(*limit)
		if err != nil {
			return err
		}
		return emit(out, *asJSON, ks, func() {
			var rows [][]string
			for _, k := range ks {
				rows = append(rows, []string{k.At[:10], k.Kind, trunc(k.Topic, 30), fit(k.Score), trunc(k.Notes, 60)})
			}
			table(out, "DATE\tKIND\tTOPIC\tSCORE\tNOTES", rows)
		})
	}
	return errors.New("usage: session log|list")
}

// ---- learn ----

func runLearn(s *store.Store, rest []string, out io.Writer) error {
	sc, rest := sub(rest)
	switch sc {
	case "add":
		fs := flag.NewFlagSet("learn add", flag.ContinueOnError)
		var l store.Learning
		fs.StringVar(&l.Skill, "skill", "", "skill name (required)")
		fs.Int64Var(&l.Priority, "priority", 2, "1 high, 2 medium, 3 low")
		fs.StringVar(&l.Resource, "resource", "", "course / book / repo url")
		fs.Int64Var(&l.Demand, "demand", 0, "how many tracked jobs asked for it")
		fs.StringVar(&l.Notes, "notes", "", "")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if l.Skill == "" {
			return errors.New("--skill required")
		}
		if err := s.UpsertLearning(l); err != nil {
			return err
		}
		fmt.Fprintln(out, "tracked", l.Skill)
		return nil
	case "list":
		fs := flag.NewFlagSet("learn list", flag.ContinueOnError)
		asJSON := fs.Bool("json", false, "")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		items, err := s.LearningItems()
		if err != nil {
			return err
		}
		return emit(out, *asJSON, items, func() {
			var rows [][]string
			for _, l := range items {
				rows = append(rows, []string{fmt.Sprint(l.ID), l.Status, fmt.Sprint(l.Priority), l.Skill, fmt.Sprintf("%.1f", l.Hours), fmt.Sprint(l.Demand), trunc(l.Resource, 40)})
			}
			table(out, "ID\tSTATUS\tPRI\tSKILL\tHOURS\tDEMAND\tRESOURCE", rows)
		})
	case "set":
		fs := flag.NewFlagSet("learn set", flag.ContinueOnError)
		status := fs.String("status", "\x00", "planned|in_progress|done")
		resource := fs.String("resource", "\x00", "")
		notes := fs.String("notes", "\x00", "")
		hours := fs.Float64("hours", 0, "hours to ADD")
		prio := fs.Int64("priority", 0, "")
		demand := fs.Int64("demand", -1, "")
		pos, err := parse(fs, rest)
		if err != nil {
			return err
		}
		if len(pos) == 0 {
			return errors.New("usage: learn set <id> --hours 1.5 --status in_progress")
		}
		var id int64
		if _, err := fmt.Sscan(pos[0], &id); err != nil {
			return err
		}
		var p store.LearningPatch
		if *status != "\x00" {
			p.Status = status
		}
		if *resource != "\x00" {
			p.Resource = resource
		}
		if *notes != "\x00" {
			p.Notes = notes
		}
		if *hours > 0 {
			p.Hours = hours
		}
		if *prio > 0 {
			p.Priority = prio
		}
		if *demand >= 0 {
			p.Demand = demand
		}
		if err := s.UpdateLearning(id, p); err != nil {
			return err
		}
		fmt.Fprintf(out, "updated learning #%d\n", id)
		return nil
	}
	return errors.New("usage: learn add|list|set")
}

// ---- stats / brief / export ----

func runStats(s *store.Store, rest []string, out io.Writer) error {
	fs := flag.NewFlagSet("stats", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	cfg := LoadConfig(s.Home)
	st, err := s.Stats(cfg.FollowUpDays, cfg.StaleDays)
	if err != nil {
		return err
	}
	return emit(out, *asJSON, st, func() {
		fmt.Fprintf(out, "jobs: %d total, %d open\n", st.Total, st.Open)
		for _, k := range store.Statuses {
			if n := st.ByStatus[k]; n > 0 {
				fmt.Fprintf(out, "  %-15s %d\n", k, n)
			}
		}
		fmt.Fprintf(out, "applied this week: %d / target %d   response rate: %.0f%%   avg fit of applied: %.0f\n", st.AppliedWeek, cfg.WeeklyApplyTarget, st.ResponseRate*100, st.AvgFit)
		fmt.Fprintf(out, "interviews this week: %d   practice sessions: %d   learning hours: %.1f   streak: %d days\n", st.InterviewsWeek, st.PracticeWeek, st.LearningHours, st.Streak)
		if len(st.DueFollowUps) > 0 {
			fmt.Fprintf(out, "\nfollow-ups due (%d+ days quiet):\n", cfg.FollowUpDays)
			for _, j := range st.DueFollowUps {
				fmt.Fprintf(out, "  #%d %s / %s (since %s)\n", j.ID, j.Company, j.Title, j.UpdatedAt[:10])
			}
		}
		if len(st.Stale) > 0 {
			fmt.Fprintf(out, "\nstale (%d+ days, consider no_response):\n", cfg.StaleDays)
			for _, j := range st.Stale {
				fmt.Fprintf(out, "  #%d %s / %s (since %s)\n", j.ID, j.Company, j.Title, j.UpdatedAt[:10])
			}
		}
	})
}

func runBrief(s *store.Store, rest []string, out io.Writer) error {
	fs := flag.NewFlagSet("brief", flag.ContinueOnError)
	compact := fs.Bool("compact", false, "smaller payload for agents: fewer rows, no notes/urls/evidence")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	cfg := LoadConfig(s.Home)
	st, err := s.Stats(cfg.FollowUpDays, cfg.StaleDays)
	if err != nil {
		return err
	}
	n := 20
	if *compact {
		n = 8
	}
	upcoming, _ := s.Interviews(0, true)
	fresh, _ := s.ListJobs(store.JobFilter{Status: "new", Limit: n})
	ranked, _ := s.ListJobs(store.JobFilter{Status: "ranked", Limit: n / 2})
	drafted, _ := s.ListJobs(store.JobFilter{Status: "drafted", Limit: n / 2})
	learning, _ := s.LearningItems()
	prefs, _ := s.Preferences()
	recent, _ := s.Events(0, n-5)
	if !*compact {
		return emit(out, true, map[string]any{
			"config": cfg, "stats": st, "upcoming_interviews": upcoming, "new_jobs": fresh, "ranked_jobs": ranked,
			"drafted_jobs": drafted, "learning": learning, "preferences": prefs, "recent_activity": recent,
		}, nil)
	}
	slim := func(jobs []store.Job) []map[string]any {
		out := make([]map[string]any, 0, len(jobs))
		for _, j := range jobs {
			out = append(out, map[string]any{"id": j.ID, "company": j.Company, "title": j.Title, "fit": j.FitScore, "remote": j.Remote, "updated": j.UpdatedAt[:10]})
		}
		return out
	}
	prefMap := map[string]string{}
	for _, p := range prefs {
		prefMap[p.Key] = p.Value
	}
	var learn []string
	for _, l := range learning {
		if l.Status == "in_progress" {
			learn = append(learn, l.Skill)
		}
	}
	var ivs []map[string]any
	for _, i := range upcoming {
		ivs = append(ivs, map[string]any{"job_id": i.JobID, "company": i.Company, "stage": i.Stage, "at": i.At, "prep": i.PrepStatus})
	}
	var acts []string
	for _, e := range recent {
		acts = append(acts, e.At[:10]+" "+e.Kind+" "+e.Company+" "+trunc(e.Note, 40))
	}
	return emit(out, true, map[string]any{
		"target_week": cfg.WeeklyApplyTarget, "open": st.Open, "applied_week": st.AppliedWeek, "response_rate": st.ResponseRate, "streak": st.Streak,
		"follow_ups_due": slim(st.DueFollowUps), "stale": slim(st.Stale), "interviews": ivs, "new": slim(fresh), "ranked": slim(ranked), "drafted": slim(drafted),
		"learning_in_progress": learn, "preferences": prefMap, "recent": acts,
	}, nil)
}

func runExport(s *store.Store, out io.Writer) error {
	jobs, _ := s.ListJobs(store.JobFilter{})
	events, _ := s.Events(0, 0)
	decs, _ := s.Decisions(0)
	prefs, _ := s.Preferences()
	ivs, _ := s.Interviews(0, false)
	sess, _ := s.Sessions(0)
	learn, _ := s.LearningItems()
	return emit(out, true, map[string]any{"jobs": jobs, "events": events, "decisions": decs, "preferences": prefs, "interviews": ivs, "sessions": sess, "learning": learn}, nil)
}
