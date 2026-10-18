package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/zoolcoder/scoutjobs/internal/store"
)

// `scoutjobs mcp` is a stdio MCP server: newline-delimited JSON-RPC 2.0 on stdin/stdout. Every tool
// is a CLI command run in-process with --json, so an MCP client sees exactly what the skills see.
// Sending e-mail, the dashboard and the server itself are not exposed.

const mcpProtocol = "2025-06-18"

type mcpProp struct {
	Name, Type, Desc string // Type: string | integer | number | boolean | array
	Flag             string // CLI flag; "" means positional, in table order
	Required         bool
}

type mcpTool struct {
	Name, Desc string
	Cmd        []string
	Props      []mcpProp
	JSON       bool // append --json
}

func p(name, typ, desc string, flag string) mcpProp { return mcpProp{name, typ, desc, flag, false} }
func req(name, typ, desc string, flag string) mcpProp {
	return mcpProp{name, typ, desc, flag, true}
}

var jobRef = req("job", "string", "job id, or company/title", "")

var mcpTools = []mcpTool{
	{"brief", "Today's picture: applied this week vs target, follow-ups due, stale jobs, upcoming interviews, new and drafted jobs.", []string{"brief", "--compact"}, nil, false},
	{"stats", "Funnel, weekly numbers, response rate, follow-ups due.", []string{"stats"}, nil, true},
	{"funnel", "What works: for the jobs applied to, how many got an answer, an interview and an offer, grouped by source, by fit band and by month applied.", []string{"stats", "--funnel"}, nil, true},
	{"daily", "Run the daily chores now: saved searches marked daily that are due, follow-up and thank-you drafts when that switch is on, interview prep packs when that switch is on. Idempotent; nothing is sent.", []string{"daily"}, nil, true},
	{"doctor", "Install health: PATH, data dir, config keys, mail secrets, linked skills, dashboard.", []string{"doctor"}, nil, true},
	{"job_list", "Tracked jobs, newest first.", []string{"job", "list", "--compact"}, []mcpProp{
		p("status", "string", "filter by status (new, ranked, drafted, applied, interview, offer, hired, rejected, skipped, withdrawn, no_response)", "status"),
		p("q", "string", "search company, title, tags", "q"),
		p("open", "boolean", "only open applications", "open"),
		p("limit", "integer", "max rows", "limit")}, true},
	{"job_show", "One job with its events, interviews and application folder.", []string{"job", "show"}, []mcpProp{jobRef}, true},
	{"job_add", "Track a posting; same company and title upserts. from=<ATS posting URL> fills the fields left out.", []string{"job", "add"}, []mcpProp{
		p("from", "string", "posting URL on a supported ATS", "from"), p("company", "string", "required unless from is given", "company"), p("title", "string", "required unless from is given", "title"), p("url", "string", "", "url"),
		p("location", "string", "", "location"), p("remote", "string", "remote|hybrid|onsite|unknown", "remote"),
		p("source", "string", "", "source"), p("posted", "string", "YYYY-MM-DD", "posted"),
		p("deadline", "string", "YYYY-MM-DD", "deadline"), p("currency", "string", "", "currency"),
		p("salary_min", "integer", "", "salary-min"), p("salary_max", "integer", "", "salary-max"),
		p("tags", "string", "comma separated", "tags"), p("notes", "string", "", "notes"),
		p("contact", "string", "application e-mail address", "contact"), p("status", "string", "initial status, default new", "status")}, true},
	{"job_set", "Update fields on a job or append a note. Status changes are logged as events.", []string{"job", "set"}, []mcpProp{jobRef,
		p("status", "string", "", "status"), p("score", "integer", "fit score 0-100", "score"), p("fit_notes", "string", "why the score is what it is", "fit-notes"),
		p("url", "string", "", "url"), p("location", "string", "", "location"), p("remote", "string", "", "remote"), p("source", "string", "", "source"),
		p("posted", "string", "", "posted"), p("deadline", "string", "", "deadline"), p("currency", "string", "", "currency"),
		p("salary_min", "integer", "", "salary-min"), p("salary_max", "integer", "", "salary-max"), p("tags", "string", "", "tags"),
		p("notes", "string", "replace the notes", "notes"), p("contact", "string", "", "contact"), p("veto", "string", "hard eligibility failure that blocks sending; empty clears it", "veto"), p("note", "string", "append an event note", "note")}, false},
	{"job_fit", "Store the evidence-backed fit review: one row per material requirement (requirement, kind must|nice, evidence, strength direct|adjacent|partial|gap, confidence, wording, risk), vetoes outside the score, score 0-100, notes. Writes fit-review.json and sets score, notes and veto on the job.", []string{"job", "fit"}, []mcpProp{jobRef,
		req("review", "string", `json: {"requirements":[{"requirement":"...","kind":"must","evidence":"profile.md: ...","strength":"direct","confidence":"high","wording":"...","risk":"..."}],"vetoes":[{"rule":"...","reason":"..."}],"score":72,"notes":"..."}`, "review")}, true},
	{"job_rank", "Score new jobs against the profile locally: free, no posting text in context. Sets fit (quick: notes) and moves new jobs to ranked; agent scores are kept. Run before any agent fit scoring and deep-score only the top ones.", []string{"job", "rank"}, []mcpProp{
		p("all", "boolean", "rescore every open job an agent has not scored", "all")}, false},
	{"job_check", "Run the application quality gates (documents, fit review, coverage, grounding, duplicates, ATS text layer, filenames). Fails while a gate fails.", []string{"job", "check"}, []mcpProp{jobRef}, true},
	{"job_review", "Log the draft-reviewer verdict for the grounding gate: send when every claim is backed by the profile, fix otherwise.", []string{"job", "review"}, []mcpProp{jobRef,
		req("verdict", "string", "send | fix", "verdict"), p("notes", "string", "what was checked, or what must change", "notes")}, false},
	{"job_followup", "List applied jobs with follow-ups sent (at most 2) and what is next: due, in Nd, sweep, done. With job, log a follow-up the user confirmed they sent; nothing is sent from here.", []string{"job", "followup"}, []mcpProp{
		p("job", "string", "job id, or company/title; only after the user confirms sending", ""), p("note", "string", "what was sent", "note")}, true},
	{"job_sweep", "List applied and interviewing jobs quiet past stale_days. yes=true marks them no_response; ask the user first (all, pick ids, or skip).", []string{"job", "sweep"}, []mcpProp{
		p("ids", "array", "close only these job ids", ""), p("yes", "boolean", "close them", "yes")}, true},
	{"job_verify", "Check that new and ranked postings are still open on their ATS (free, no posting text). Closed ones are skipped after 3 failed checks over 2 days. Run before scoring a batch.", []string{"job", "verify"}, []mcpProp{
		p("ids", "array", "check only these job ids", "")}, true},
	{"job_skip", "Skip a job with the reason the preference learner reads.", []string{"job", "skip"}, []mcpProp{jobRef,
		req("reason", "string", "why, in the user's words", "reason"), p("signals", "string", `json object, e.g. {"remote":"onsite","stack":["java"]}`, "signals")}, false},
	{"search", "Search company ATS boards and remote boards. Postings come best match first with derived skills, level, regions and visa. add=true tracks them as new.", []string{"search", "--compact"}, []mcpProp{
		p("query", "array", "keyword queries; every word of a query must appear", "query"), p("board", "array", "source:<slug> (greenhouse, lever, ashby, workable, smartrecruiters, personio, recruitee, bamboohr), rss:<url>, or a careers page URL", "board"),
		p("remote", "boolean", "also query the remote boards", "remote"), p("limit", "integer", "max postings", "limit"), p("add", "boolean", "add matches to the tracker", "add"),
		p("location", "string", "comma separated cities, countries or regions; remote jobs match when open to them", "location"),
		p("hide_mismatches", "boolean", "drop postings that break a must in the profile (place, engagement, salary floor)", "hide-mismatches")}, true},
	{"cv_files", "Resume files in the cv folder.", []string{"cv", "files"}, nil, true},
	{"cv_log", "Log a CV check result.", []string{"cv", "log"}, []mcpProp{req("file", "string", "file name inside cv/", "file"),
		p("score", "integer", "overall 0-100", "score"), p("ats", "integer", "ATS score 0-100", "ats"), p("summary", "string", "one line verdict", "summary"),
		p("gaps", "string", "comma separated fixes", "gaps"), p("job", "string", "job id when checked against a posting", "job")}, false},
	{"cv_list", "CV check history.", []string{"cv", "list"}, []mcpProp{p("limit", "integer", "", "limit")}, true},
	{"cv_export", "Render resume.md (master cv/ or a job's folder) to PDF next to it.", []string{"cv", "export"}, []mcpProp{
		p("job", "string", "job id; omit for the master resume", ""), p("file", "string", "markdown file, default resume.md", "file")}, true},
	{"cv_verify", "Check a PDF CV the way an ATS reads it: page count, text layer, e-mail, required phrases, date-range dashes. Runs locally; read its result instead of the PDF.", []string{"cv", "verify"}, []mcpProp{
		p("job", "string", "job id, or a PDF path; omit for cv/resume.pdf", ""), p("file", "string", "PDF in the folder, default resume.pdf", "file"),
		p("pages", "integer", "most pages allowed, default 2", "pages"), p("contains", "array", "phrases the text must hold", "contains")}, true},
	{"profile_expand", "Skills found in cv/, linkedin/ and the user's public GitHub repos that profile.md does not list yet, with where each showed up. Runs locally. Pass add only with skills the user confirmed.", []string{"profile", "expand"}, []mcpProp{
		p("github", "string", "GitHub user; default the link in profile.md", "github"), p("add", "string", "comma separated confirmed skills to append to Working", "add")}, true},
	{"country_show", "What a country implies for the search: currency, local job boards, posting languages, time zone, CV norms, work-rights note. Changes nothing.", []string{"country", "show"}, []mcpProp{
		req("country", "string", "ISO code or name, e.g. DK or Denmark", "")}, true},
	{"country_set", "Set the user's country: writes country, currency and local boards to config.yaml and proposes locations, languages, timezone and cv_norms for the user to confirm with pref review. Never suggests salary.", []string{"country", "set"}, []mcpProp{
		req("country", "string", "ISO code or name, e.g. DK or Denmark", "")}, true},
	{"cv_cover", "Fill the Word cover letter template (cover_template in config.yaml) with cover-letter.slots.json: writes cover-letter.docx, cover-letter.md and, with LibreOffice, cover-letter.pdf, and reports template and page problems.", []string{"cv", "cover"}, []mcpProp{jobRef,
		p("template", "string", "Word template; default cover_template", "template")}, true},
	{"email_draft", "Build the application e-mail (email.json and email.eml) from the cover letter. Nothing is sent.", []string{"email", "draft"}, []mcpProp{jobRef,
		p("to", "string", "recipient; default the job's contact", "to"), p("subject", "string", "", "subject"),
		p("body", "string", "file in the application folder used as body", "body"), p("attach", "array", "extra files to attach", "attach")}, true},
	{"email_show", "Show the e-mail draft of a job.", []string{"email", "show"}, []mcpProp{jobRef}, true},
	{"email_sync", "Read the inbox locally (Gmail API or IMAP), log replies matched to applications (rejections and invites move the status) and list unmatched ones under review. Try before a mail connector: no message bodies in context. Fails when the user chose the AI agent for mail; then use email_log.", []string{"email", "sync"}, []mcpProp{
		p("since", "string", "14d, 3w, or YYYY-MM-DD", "since"), p("limit", "integer", "", "limit"), p("dry_run", "boolean", "show matches, change nothing", "dry-run"),
		p("via", "string", "gmail | imap | auto", "via")}, true},
	{"email_log", "Record a mail message read through a connector against its job. Same message_id is ignored.", []string{"email", "log"}, []mcpProp{jobRef,
		req("subject", "string", "", "subject"), p("from", "string", "sender address", "from"), p("snippet", "string", "one or two lines, no more", "snippet"),
		p("date", "string", "RFC3339 or YYYY-MM-DD", "date"), p("thread", "string", "Gmail thread id", "thread"), p("message_id", "string", "Message-ID or connector message id", "message-id"),
		p("kind", "string", "rejected|interview|offer|request|ack", "kind"), p("dir", "string", "in (received, default) or out (sent)", "dir"),
		p("status", "string", "move the job too, only after the user confirmed", "status")}, true},
	{"gmail_status", "Whether the Gmail API is connected and as which account.", []string{"gmail", "status"}, nil, true},
	{"gmail_draft", "Upload the job's application e-mail (from email_draft) to the user's Gmail as a draft with attachments. Nothing is sent; the user sends from Gmail.", []string{"gmail", "draft"}, []mcpProp{jobRef}, true},
	{"decision_log", "Record a decision the user made (skip, apply, withdraw, override_score, ...).", []string{"decision", "log"}, []mcpProp{
		req("kind", "string", "skip|apply|withdraw|accept_offer|decline_offer|override_score|other", "kind"), req("reason", "string", "", "reason"),
		p("signals", "string", "json object", "signals"), p("job", "string", "job id or company/title", "job")}, false},
	{"decision_summary", "Aggregated decisions: what the user skips and why.", []string{"decision", "summary"}, []mcpProp{p("limit", "integer", "", "limit")}, true},
	{"pref_list", "Stated and learned preferences.", []string{"pref", "list"}, nil, true},
	{"pref_set", "Set a preference.", []string{"pref", "set"}, []mcpProp{req("key", "string", "", ""), req("value", "string", "", ""),
		p("confidence", "number", "0-1", "confidence"), p("evidence", "string", "what taught this", "evidence"), p("source", "string", "stated|learned", "source")}, false},
	{"pref_rm", "Remove a preference.", []string{"pref", "rm"}, []mcpProp{req("key", "string", "", "")}, false},
	{"pref_propose", "Queue a learned preference for the user to review; refuses stated keys and rejected values without new evidence.", []string{"pref", "propose"}, []mcpProp{req("key", "string", "", ""), req("value", "string", "", ""),
		p("support", "integer", "decisions that support it", "support"), p("against", "integer", "decisions that contradict it", "against"),
		p("confidence", "number", "0-0.95", "confidence"), p("evidence", "string", "counts and the strongest quotes", "evidence")}, false},
	{"pref_review", "Learned preferences awaiting review; all=true adds rejected ones.", []string{"pref", "review"}, []mcpProp{p("all", "boolean", "include rejected", "all")}, true},
	{"pref_confirm", "Confirm a proposed preference, only after the user said yes; value stores the user's own wording as stated.", []string{"pref", "confirm"}, []mcpProp{req("key", "string", "", ""), p("value", "string", "user's wording", "value")}, false},
	{"pref_reject", "Reject a proposed preference, only after the user said no.", []string{"pref", "reject"}, []mcpProp{req("key", "string", "", ""), p("reason", "string", "user's reason", "reason")}, false},
	{"interview_list", "Interviews, upcoming unless all=true.", []string{"interview", "list"}, []mcpProp{p("all", "boolean", "include past", "all")}, true},
	{"interview_add", "Schedule an interview; an earlier-stage job moves to interview.", []string{"interview", "add"}, []mcpProp{jobRef,
		req("stage", "string", "screen|technical|system_design|behavioral|case|onsite|final|other", "stage"), p("at", "string", "YYYY-MM-DD or YYYY-MM-DDTHH:MM", "at"),
		p("format", "string", "phone|video|onsite", "format"), p("interviewer", "string", "", "interviewer"),
		p("duration", "integer", "minutes, default 60", "duration"), p("location", "string", "meeting link or address", "location")}, false},
	{"calendar_sync", "Sync interviews with the user's Google Calendar both ways: scheduled interviews are added there (no guests, nobody is invited), moves and deletions there come back, and events naming a tracked company become interviews.", []string{"calendar", "sync"}, nil, true},
	{"calendar_save", "Save the events you listed from the user's calendar so ScoutJobs shows them; replaces the last list. Pass titles, times and locations only, never descriptions or guests.", []string{"calendar", "save"}, []mcpProp{
		req("events", "string", `JSON array of {"id","title","start","end","all_day","location"}; times as YYYY-MM-DDTHH:MM in the user's time`, "events")}, false},
	{"interview_set", "Update an interview: outcome, feedback, prep status, time.", []string{"interview", "set"}, []mcpProp{req("id", "integer", "interview id", ""),
		p("at", "string", "", "at"), p("format", "string", "", "format"), p("interviewer", "string", "", "interviewer"), p("prep_status", "string", "todo|in_progress|ready", "prep-status"),
		p("outcome", "string", "passed|failed|pending|cancelled", "outcome"), p("feedback", "string", "", "feedback"), p("stage", "string", "", "stage"),
		p("duration", "integer", "minutes", "duration"), p("location", "string", "meeting link or address", "location")}, false},
	{"contacts", "Referral finder: people in the user's imported LinkedIn network who work at the job's company (or at company).", []string{"contacts"}, []mcpProp{
		p("job", "string", "job id, or company/title", ""), p("company", "string", "company name instead of a job", "company")}, true},
	{"linkedin_import", "Import a LinkedIn data export (zip or unpacked folder on this machine): profile to linkedin/profile.md, connections for the referral finder.", []string{"linkedin", "import"}, []mcpProp{
		req("path", "string", "path to the export zip or folder", "")}, true},
	{"session_log", "Log a practice session.", []string{"session", "log"}, []mcpProp{req("kind", "string", "mock_behavioral|mock_sysdesign|mock_coding|drill|study|review", "kind"),
		p("topic", "string", "", "topic"), p("score", "integer", "0-100 if graded", "score"), p("notes", "string", "", "notes")}, false},
	{"session_list", "Practice sessions, newest first.", []string{"session", "list"}, []mcpProp{p("limit", "integer", "", "limit")}, true},
	{"learn_list", "Learning items ranked by priority.", []string{"learn", "list"}, nil, true},
	{"learn_add", "Add or update a skill to learn.", []string{"learn", "add"}, []mcpProp{req("skill", "string", "", "skill"), p("priority", "integer", "1 high, 2 medium, 3 low", "priority"),
		p("resource", "string", "course, book, repo url", "resource"), p("demand", "integer", "how many tracked jobs asked for it", "demand"), p("notes", "string", "", "notes")}, false},
	{"learn_set", "Update a learning item; hours are added.", []string{"learn", "set"}, []mcpProp{req("id", "integer", "learning item id", ""),
		p("status", "string", "planned|in_progress|done", "status"), p("hours", "number", "hours to add", "hours"), p("resource", "string", "", "resource"),
		p("notes", "string", "", "notes"), p("priority", "integer", "", "priority"), p("demand", "integer", "", "demand")}, false},
	{"run", "Run any scoutjobs CLI command by its arguments, e.g. [\"job\",\"list\",\"--json\"]. Refused: email send, email test, serve, mcp, update.", []string{}, []mcpProp{
		req("args", "array", "command line arguments without the program name", "")}, false},
}

// mcpDenied keeps the server from sending mail, blocking on a listener, or nesting itself.
func mcpDenied(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "serve", "mcp", "update":
		return true
	case "daily":
		// it starts agent runs, which must not nest
		return os.Getenv("SCOUTJOBS_RUNNER") != ""
	case "gmail", "google":
		// an unattended job run gets no Google access at all
		return os.Getenv("SCOUTJOBS_RUNNER") != "" || len(args) > 1 && slices.Contains([]string{"setup", "connect", "reconnect", "disconnect"}, args[1])
	case "calendar":
		return os.Getenv("SCOUTJOBS_RUNNER") != "" && (len(args) < 2 || args[1] != "save")
	case "job":
		return len(args) > 1 && args[1] == "run"
	case "email":
		return len(args) > 1 && (args[1] == "send" || args[1] == "test")
	}
	return false
}

func (t mcpTool) schema() map[string]any {
	props := map[string]any{}
	var required []string
	for _, pr := range t.Props {
		s := map[string]any{"type": pr.Type}
		if pr.Desc != "" {
			s["description"] = pr.Desc
		}
		if pr.Type == "array" {
			s["items"] = map[string]any{"type": "string"}
		}
		props[pr.Name] = s
		if pr.Required {
			required = append(required, pr.Name)
		}
	}
	schema := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// args turns the tool input into a command line: positionals in table order, then flags.
func (t mcpTool) args(in map[string]any) ([]string, error) {
	if t.Name == "run" {
		return anyStrings(in["args"]), nil
	}
	args := append([]string{}, t.Cmd...)
	var flags []string
	for _, pr := range t.Props {
		v, ok := in[pr.Name]
		if !ok || v == nil {
			if pr.Required {
				return nil, fmt.Errorf("%s is required", pr.Name)
			}
			continue
		}
		switch pr.Type {
		case "boolean":
			if b, _ := v.(bool); b {
				flags = append(flags, "--"+pr.Flag)
			}
		case "array":
			for _, s := range anyStrings(v) {
				if pr.Flag == "" {
					args = append(args, s)
				} else {
					flags = append(flags, "--"+pr.Flag, s)
				}
			}
		default:
			s := scalar(v)
			if pr.Flag == "" {
				args = append(args, s)
			} else {
				flags = append(flags, "--"+pr.Flag, s)
			}
		}
	}
	args = append(args, flags...)
	if t.JSON {
		args = append(args, "--json")
	}
	return args, nil
}

func scalar(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprint(int64(x))
		}
		return fmt.Sprint(x)
	case bool:
		return fmt.Sprint(x)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func anyStrings(v any) []string {
	var out []string
	if list, ok := v.([]any); ok {
		for _, e := range list {
			out = append(out, scalar(e))
		}
	}
	return out
}

// ---- JSON-RPC ----

type rpcRequest struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func runMCP(in io.Reader, out io.Writer) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	enc := json.NewEncoder(out)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			_ = enc.Encode(rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32700, "parse error"}})
			continue
		}
		resp, reply := mcpHandle(req)
		if reply {
			if err := enc.Encode(resp); err != nil {
				return err
			}
		}
	}
	return sc.Err()
}

// mcpHandle answers one message; notifications (no id) produce no reply.
func mcpHandle(req rpcRequest) (rpcResponse, bool) {
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	if len(req.ID) == 0 || string(req.ID) == "null" {
		return resp, false // notification: notifications/initialized, notifications/cancelled
	}
	switch req.Method {
	case "initialize":
		resp.Result = map[string]any{
			"protocolVersion": mcpProtocol,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "scoutjobs", "version": Version},
			"instructions":    "ScoutJobs tracks a job search: postings, fit scores, drafts, applications, interviews, learning, preferences. Every tool is a scoutjobs CLI command. Sending e-mail is not available here; ask the user to run `scoutjobs email send <id> --yes`.",
		}
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		var list []map[string]any
		mailOff := mcpMailOff()
		for _, t := range mcpTools {
			if !mcpOffered(t.Name) || mailOff && slices.Contains(mcpMailTools, t.Name) {
				continue
			}
			tool := map[string]any{"name": t.Name, "description": t.Desc, "inputSchema": t.schema()}
			if t.readOnly() {
				tool["annotations"] = map[string]any{"readOnlyHint": true}
			}
			list = append(list, tool)
		}
		resp.Result = map[string]any{"tools": list}
	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			resp.Error = &rpcError{-32602, "invalid params"}
			break
		}
		text, err := mcpCall(params.Name, params.Arguments)
		if errors.Is(err, errNoTool) {
			resp.Error = &rpcError{-32602, err.Error()}
			break
		}
		result := map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}
		if err != nil {
			result["content"] = []map[string]any{{"type": "text", "text": strings.TrimSpace(text + "\nerror: " + err.Error())}}
			result["isError"] = true
		}
		resp.Result = result
	default:
		resp.Error = &rpcError{-32601, "method not found: " + req.Method}
	}
	return resp, true
}

var errNoTool = errors.New("unknown tool")

func mcpCall(name string, in map[string]any) (string, error) {
	for _, t := range mcpTools {
		if t.Name != name || !mcpOffered(name) {
			continue
		}
		args, err := t.args(in)
		if err != nil {
			return "", err
		}
		if mcpDenied(args) {
			return "", fmt.Errorf("%s is not available over MCP; ask the user to run it in a terminal", strings.Join(args, " "))
		}
		var buf mcpOutput
		err = Run(args, &buf)
		return capOutput(strings.TrimSpace(buf.String())), err
	}
	return "", fmt.Errorf("%w: %s", errNoTool, name)
}

// mcpOffered reports whether this server offers a tool. SCOUTJOBS_MCP_TOOLS, a comma separated list,
// narrows the set: an unattended session gets the few tools its steps call, because every listed
// tool's schema is sent with every request.
func mcpOffered(name string) bool {
	only := os.Getenv("SCOUTJOBS_MCP_TOOLS")
	return only == "" || slices.Contains(strings.Split(only, ","), name)
}

// mcpMailTools read the mailbox; with email_sync off their schemas would only cost every request tokens.
var mcpMailTools = []string{"email_sync", "email_log", "email_show", "gmail_status", "gmail_draft"}

func mcpMailOff() bool {
	s, err := store.Open()
	if err != nil {
		return false
	}
	defer s.Close()
	return emailMode(s, LoadConfig(s.Home)) == "off"
}

// mcpReadOnly are the tools that change nothing, so a client may run them without asking.
var mcpReadOnly = []string{"brief", "stats", "funnel", "doctor", "job_list", "job_show", "cv_files", "cv_list", "cv_verify", "country_show",
	"email_show", "gmail_status", "decision_summary", "pref_list", "pref_review", "interview_list", "contacts", "session_list", "learn_list"}

func (t mcpTool) readOnly() bool { return slices.Contains(mcpReadOnly, t.Name) }

// mcpMaxOutput bounds one tool result: a long listing would otherwise fill the client's context.
const mcpMaxOutput = 40_000

func capOutput(s string) string {
	if len(s) <= mcpMaxOutput {
		return s
	}
	return s[:mcpMaxOutput] + fmt.Sprintf("\n… cut at %d of %d bytes; narrow the query (status, q, limit) for the rest", mcpMaxOutput, len(s))
}

// mcpOutput collects a tool's output and tells commands that an AI client called them.
type mcpOutput struct{ bytes.Buffer }
