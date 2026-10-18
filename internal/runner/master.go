package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zoolcoder/scoutjobs/skills"
)

// MasterID keys the main documents' run in the Manager; no job has id 0.
const MasterID = 0

// MasterSteps write the main CV and cover letter in cv/, the documents every per-job draft starts from.
var MasterSteps = []Step{
	{"sync-profile", "Profile from your CV", "", "Read the user's own CV file in cv/ (the PDF or DOCX they added, see \"Your CV\" above) and bring profile.md in line with it: add every role, date, employer, degree, certificate, skill, link and quantified result the CV has and profile.md lacks, and correct dates or titles where profile.md is plainly behind the CV. Keep profile.md's structure, keep everything already in it, and add nothing that is not in the CV. Edit profile.md in the data folder in place."},
	{"main-cv", "Main CV", "resume-ats-optimizer", "Write cv/resume.md, the main CV every tailored resume starts from. When the user's own CV file exists (see \"Your CV\" above), it sets the style and format: keep its sections, headings, section order, contact line, date format, bullet style and length, and transcribe that layout into Markdown; improve the wording, not the shape. Without one, use a one-line headline, a short summary, every role with its strongest true and quantified bullets, skills, education. ATS-safe Markdown either way: one `# Name` heading, `##` sections, `-` bullets, no tables or columns. It is not aimed at one posting. When cv/resume.md exists, improve it and keep every fact in it that profile.md does not contradict."},
	{"main-cover", "Main cover letter", "cover-letter-generator", "Write cv/cover-letter.md, the main cover letter every per-job letter starts from: 250-350 words in the user's voice from profile.md, the CV and stories/, with the placeholders [Company], [Role] and [Why this company] where a posting fills them in. No invented facts. Ignore the skill's instructions about the application folder: this letter goes in cv/."},
}

// MasterFile is the file a master step writes, in cv/ (the profile sync writes ../profile.md).
func MasterFile(step string) string {
	switch step {
	case "main-cover":
		return "cover-letter.md"
	case "sync-profile":
		return filepath.Join("..", "profile.md")
	}
	return "resume.md"
}

// MasterDone reports which main documents are up to date, by step name. The profile counts as synced
// once it is newer than the user's own CV file.
func (rn *Runner) MasterDone() map[string]bool {
	out := map[string]bool{}
	for _, s := range MasterSteps {
		out[s.Name], _ = rn.masterDone(s.Name)
	}
	return out
}

func (rn *Runner) masterDone(step string) (bool, string) {
	if step != "sync-profile" {
		return exists(rn.cvDir(), MasterFile(step)), "cv/" + MasterFile(step) + " exists"
	}
	src := SourceCV(rn.cvDir())
	if src == "" {
		return true, "no CV file in cv/ to read"
	}
	cv, err1 := os.Stat(filepath.Join(rn.cvDir(), src))
	prof, err2 := os.Stat(filepath.Join(rn.Store.Home, "profile.md"))
	if err1 == nil && err2 == nil && prof.ModTime().After(cv.ModTime()) {
		return true, "profile.md is newer than cv/" + src
	}
	return false, ""
}

// SourceCV is the user's own CV in cv/: the newest PDF or DOCX they added. ScoutJobs' own outputs,
// resume.pdf exported from resume.md and *.prev.* copies, are not the user's CV.
func SourceCV(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	exported := exists(dir, "resume.md")
	best, bestAt := "", time.Time{}
	for _, e := range entries {
		n := strings.ToLower(e.Name())
		if e.IsDir() || strings.Contains(n, ".prev") || (n == "resume.pdf" && exported) {
			continue
		}
		if !strings.HasSuffix(n, ".pdf") && !strings.HasSuffix(n, ".docx") {
			continue
		}
		if info, err := e.Info(); err == nil && info.ModTime().After(bestAt) {
			best, bestAt = e.Name(), info.ModTime()
		}
	}
	return best
}

func (rn *Runner) cvDir() string { return filepath.Join(rn.Store.Home, "cv") }

// PlanMaster resolves the agent for the main documents. Without force, a document that exists is kept.
func (rn *Runner) PlanMaster(req Request) (*Run, Agent, error) {
	want := map[string]bool{}
	for _, n := range req.Steps {
		want[n] = true
	}
	run := newRun(fmt.Sprintf("main-%d", time.Now().UnixMilli()), MasterID, req.DryRun)
	need := false
	for _, s := range MasterSteps {
		if len(want) > 0 && !want[s.Name] {
			continue
		}
		st := StepState{Step: s, State: Pending}
		if ok, why := rn.masterDone(s.Name); ok && (!req.Force || s.Name == "sync-profile" && SourceCV(rn.cvDir()) == "") {
			st.State, st.Detail = Skipped, why
		} else {
			need = true
		}
		run.Steps = append(run.Steps, st)
	}
	if len(run.Steps) == 0 {
		return nil, Agent{}, errors.New("no steps selected")
	}
	if !need {
		return run, Agent{}, nil
	}
	ag, err := rn.pick(req.Agent)
	if err != nil {
		return nil, ag, err
	}
	run.Agent = ag.Name
	return run, ag, nil
}

// ExecMaster writes the main documents in one agent session. The previous version of a document it
// replaces is kept next to it as <name>.prev.md.
func (rn *Runner) ExecMaster(ctx context.Context, run *Run, ag Agent, echo io.Writer) {
	finish(ctx, run, echo, func(ctx context.Context) error {
		var steps []Step
		var idx []int
		for i, s := range run.Steps {
			if s.State == Skipped {
				run.logf("skip %s: %s", s.Name, s.Detail)
				continue
			}
			steps, idx = append(steps, s.Step), append(idx, i)
		}
		if len(steps) == 0 || run.DryRun {
			return nil
		}
		dir := rn.cvDir()
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		for _, s := range steps {
			name := MasterFile(s.Name)
			if b, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
				_ = os.WriteFile(filepath.Join(dir, strings.TrimSuffix(name, ".md")+".prev.md"), b, 0o600)
			}
		}
		for _, i := range idx {
			run.set(i, Running, "")
		}
		run.logf("%s: %s", ag.Name, labels(steps))
		since := time.Now().Add(-time.Second)
		if err := rn.agent(ctx, run, ag, dir, MasterPrompt(rn.Store.Home, rn.Exe, steps)); err != nil {
			for _, i := range idx {
				run.set(i, Failed, ag.Name+" stopped")
			}
			return err
		}
		for _, i := range idx {
			name := MasterFile(run.Steps[i].Name)
			st, err := os.Stat(filepath.Join(dir, name))
			if err != nil || st.ModTime().Before(since) {
				if run.Steps[i].Name == "sync-profile" { // nothing to add is a valid outcome
					run.set(i, Done, "profile.md already matches your CV")
					_ = os.Chtimes(filepath.Join(dir, name), time.Now(), time.Now())
					continue
				}
				run.set(i, Failed, ag.Name+" finished without writing cv/"+name)
				return fmt.Errorf("%s: no new cv/%s", run.Steps[i].Name, name)
			}
			run.set(i, Done, filepath.Clean("cv/"+name))
		}
		return nil
	})
}

// MasterPrompt is what the agent reads for the main documents.
func MasterPrompt(home, exe string, steps []Step) string {
	var b strings.Builder
	b.WriteString("You are preparing the user's main CV and cover letter for ScoutJobs, unattended. No one will answer questions: decide, write the files, and stop.\n\n")
	fmt.Fprintf(&b, "Data folder (profile.md, preferences.md, stories/): %s\nCV folder: %s\nWrite only the files the steps name.\n\n", home, filepath.Join(home, "cv"))
	if src := SourceCV(filepath.Join(home, "cv")); src != "" {
		fmt.Fprintf(&b, "Your CV: cv/%s is the user's own CV. Read it in full; its facts are the user's and its layout is the format to keep.\n\n", src)
	}
	fmt.Fprintf(&b, "Every `scoutjobs ...` command in the skills is available as a tool of the scoutjobs MCP server, or as the CLI at %s.\n\n", exe)
	b.WriteString("Rules:\n- Never send e-mail, never contact anyone, never touch Gmail.\n- Use only facts from profile.md, the CV files and the stories; never invent experience, numbers or employers.\n- profile.md is the source of truth when sources disagree.\n- Do only the steps below, in order.\n\n")
	for n, s := range steps {
		fmt.Fprintf(&b, "Step %d, %s: %s\n", n+1, strings.ToLower(s.Label), s.Task)
	}
	for _, s := range steps {
		if s.Skill != "" {
			fmt.Fprintf(&b, "\n\n===== skill %s =====\n\n%s", s.Skill, skills.Unattended(s.Skill))
		}
	}
	return b.String()
}
