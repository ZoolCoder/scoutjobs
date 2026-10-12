package web

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/zoolcoder/scoutjobs/internal/apply"
	"github.com/zoolcoder/scoutjobs/internal/runner"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

// rehearsedForStories is how many rehearsed questions tick the "stories" prep item.
const rehearsedForStories = 3

// companyPanel is the Interviews tab's view of what ScoutJobs knows about the company.
type companyPanel struct {
	Name      string
	Intel     bool // companies/<key>/intel.md
	Notes     bool // companies/<key>/notes.md
	Questions []store.Question
	Rehearsed int
	Mocks     []apply.Mock
	Kinds     []string
}

func (v *server) company(j store.Job) companyPanel {
	qs, _ := v.s.Questions(j.Company)
	done, _ := store.Rehearsed(qs)
	return companyPanel{Name: j.Company, Questions: qs, Rehearsed: done, Mocks: apply.Mocks(apply.Dir(v.s, j)), Kinds: store.QuestionKinds,
		Intel: apply.CompanyFile(v.s, j.Company, apply.CompanyIntel) != "", Notes: apply.CompanyFile(v.s, j.Company, apply.CompanyNotes) != ""}
}

// postJob is formJob for the forms of this file: same-origin posts only.
func (v *server) postJob(w http.ResponseWriter, r *http.Request) (store.Job, bool) {
	if sameOrigin(r) != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return store.Job{}, false
	}
	return v.formJob(w, r)
}

func interviewsTab(id int64, anchor, key, msg string) string {
	u := fmt.Sprintf("/jobs/%d?tab=interviews", id)
	if msg != "" {
		u += "&" + key + "=" + url.QueryEscape(msg)
	}
	return u + "#" + anchor
}

// pasteIntel keeps text the user copied from a review site on the company and files its questions.
func (v *server) pasteIntel(w http.ResponseWriter, r *http.Request) {
	j, ok := v.postJob(w, r)
	if !ok {
		return
	}
	n, err := apply.SavePaste(v.s, j, apply.Paste{Source: r.FormValue("source"), URL: r.FormValue("url"), Text: r.FormValue("text")})
	if err != nil {
		http.Redirect(w, r, interviewsTab(j.ID, "company", "err", err.Error()), http.StatusSeeOther)
		return
	}
	_ = v.s.AddEvent(j.ID, "intel:paste", fmt.Sprintf("%s, %d questions", strings.TrimSpace(r.FormValue("source")), n))
	msg := fmt.Sprintf("Saved on %s. %d new %s in the question bank.", j.Company, n, plural(n, "question", "questions"))
	http.Redirect(w, r, interviewsTab(j.ID, "company", "msg", msg), http.StatusSeeOther)
}

// companyFile shows the company notes or research, read-only.
func (v *server) companyFile(w http.ResponseWriter, r *http.Request) {
	j, ok := v.formJob(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	if name != apply.CompanyNotes && name != apply.CompanyIntel {
		http.NotFound(w, r)
		return
	}
	p, found := loadPage(apply.CompanyDir(v.s, j.Company), name)
	if !found {
		http.NotFound(w, r)
		return
	}
	p.Back, p.BackLabel = interviewsTab(j.ID, "company", "", ""), j.Company+" · "+j.Title
	v.render(w, r, "file.html", "pipeline", map[string]any{"F": p})
}

func (v *server) addQuestion(w http.ResponseWriter, r *http.Request) {
	j, ok := v.postJob(w, r)
	if !ok {
		return
	}
	if _, err := v.s.AddQuestion(j, store.Question{Text: r.FormValue("text"), Kind: r.FormValue("kind"), Source: "manual"}); err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, interviewsTab(j.ID, "questions", "", ""), http.StatusSeeOther)
}

// rateQuestion is the rehearsed box and the 1-5 rating of a question in the bank.
func (v *server) rateQuestion(w http.ResponseWriter, r *http.Request) {
	j, ok := v.postJob(w, r)
	if !ok {
		return
	}
	q, err := v.question(r, j)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if r.FormValue("delete") != "" {
		_ = v.s.DeleteQuestion(q.ID)
	} else {
		rating, _ := strconv.Atoi(r.FormValue("rating"))
		_ = v.s.RateQuestion(q.ID, r.FormValue("rehearsed") != "" || rating > 0, rating)
		v.tickRehearsed(j)
	}
	http.Redirect(w, r, interviewsTab(j.ID, "questions", "", ""), http.StatusSeeOther)
}

// question is the bank question in the path, when it belongs to the job's company.
func (v *server) question(r *http.Request, j store.Job) (store.Question, error) {
	id, err := strconv.ParseInt(r.PathValue("qid"), 10, 64)
	if err != nil {
		return store.Question{}, err
	}
	q, err := v.s.QuestionByID(id)
	if err == nil && q.Company != store.CompanyKey(j.Company) {
		err = fmt.Errorf("question %d is not about %s", id, j.Company)
	}
	return q, err
}

// tickRehearsed marks the stories prep item of the next interview once enough answers are rehearsed.
func (v *server) tickRehearsed(j store.Job) {
	qs, _ := v.s.Questions(j.Company)
	if done, _ := store.Rehearsed(qs); done >= rehearsedForStories {
		v.tickPrepItem(j.ID, "stories")
	}
}

func (v *server) tickPrepItem(jobID int64, key string) {
	ivs, _ := v.s.Interviews(jobID, true)
	for _, iv := range ivs {
		if iv.Outcome != "" || iv.PrepDone(key) {
			continue
		}
		done := strings.TrimPrefix(iv.Prep+","+key, ",")
		_ = v.s.UpdateInterview(iv.ID, store.InterviewPatch{Prep: &done})
		return
	}
}

// debrief keeps what happened in an interview: the questions go to the bank, the notes to the company.
func (v *server) debrief(w http.ResponseWriter, r *http.Request) {
	if sameOrigin(r) != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	iv, ok := v.interview(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	j, err := v.s.JobByID(iv.JobID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	field := func(k string) string { return strings.TrimSpace(strings.ReplaceAll(r.FormValue(k), "\r\n", "\n")) }
	asked, well, weak := field("asked"), field("went_well"), field("weak")
	if err := v.s.UpdateInterview(id, store.InterviewPatch{Asked: &asked, WentWell: &well, Weak: &weak}); err != nil {
		fail(w, err)
		return
	}
	n := 0
	for _, line := range strings.Split(asked, "\n") {
		if ok, _ := v.s.AddQuestion(j, store.Question{Text: strings.TrimLeft(line, "-*• "), Source: "debrief"}); ok {
			n++
		}
	}
	var note strings.Builder
	for _, part := range [][2]string{{"Asked", asked}, {"Went well", well}, {"To work on", weak}} {
		if part[1] != "" {
			fmt.Fprintf(&note, "**%s**\n\n%s\n\n", part[0], part[1])
		}
	}
	if note.Len() > 0 {
		_ = apply.AddCompanyNote(v.s, j.Company, "Debrief, "+iv.Stage+" interview", note.String())
	}
	_ = v.s.AddEvent(j.ID, "debrief", fmt.Sprintf("%s interview, %d new %s", iv.Stage, n, plural(n, "question", "questions")))
	http.Redirect(w, r, back(r, interviewsTab(j.ID, "questions", "", "")), http.StatusSeeOther)
}

func (v *server) interview(id int64) (store.Interview, bool) {
	ivs, _ := v.s.Interviews(0, false)
	for _, iv := range ivs {
		if iv.ID == id {
			return iv, true
		}
	}
	return store.Interview{}, false
}

// startMock picks questions from the bank and opens the written mock interview.
func (v *server) startMock(w http.ResponseWriter, r *http.Request) {
	j, ok := v.postJob(w, r)
	if !ok {
		return
	}
	m, err := apply.StartMock(v.s, j)
	if err != nil {
		http.Redirect(w, r, interviewsTab(j.ID, "questions", "err", err.Error()), http.StatusSeeOther)
		return
	}
	_ = v.s.AddEvent(j.ID, "mock:start", strconv.Itoa(m.N))
	http.Redirect(w, r, fmt.Sprintf("/jobs/%d/mock/%d", j.ID, m.N), http.StatusSeeOther)
}

func mockNumber(r *http.Request) int {
	n, _ := strconv.Atoi(r.PathValue("n"))
	return n
}

func (v *server) mockPage(w http.ResponseWriter, r *http.Request) {
	j, ok := v.formJob(w, r)
	if !ok {
		return
	}
	dir := apply.Dir(v.s, j)
	m, err := apply.LoadMock(dir, mockNumber(r))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var feedback any
	if m.Feedback {
		if p, ok := loadPage(dir, apply.MockFeedbackName(m.N)); ok {
			feedback = p.HTML
		}
	}
	scoring := false
	if v.cfg.Runner != nil {
		if run := v.cfg.Runner.Latest(j.ID); run != nil {
			scoring = run.Snapshot().State == runner.Running
		}
	}
	v.render(w, r, "mock.html", "pipeline", map[string]any{"Job": j, "M": m, "Feedback": feedback, "Scoring": scoring,
		"CanScore": v.cfg.Runner != nil, "Msg": r.URL.Query().Get("msg"), "Err": r.URL.Query().Get("err")})
}

// saveMock keeps the typed answers; "Score with AI" also hands them to the assistant.
func (v *server) saveMock(w http.ResponseWriter, r *http.Request) {
	j, ok := v.postJob(w, r)
	if !ok {
		return
	}
	n := mockNumber(r)
	dir := apply.Dir(v.s, j)
	m, err := apply.LoadMock(dir, n)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	answers := map[int64]string{}
	for _, q := range m.Questions {
		answers[q.ID] = r.FormValue(fmt.Sprintf("a%d", q.ID))
	}
	self := fmt.Sprintf("/jobs/%d/mock/%d", j.ID, n)
	if err := apply.SaveAnswers(dir, n, answers); err != nil {
		http.Redirect(w, r, self+"?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	msg := "Answers saved."
	if r.FormValue("score") != "" {
		if v.cfg.Runner == nil {
			msg = "No AI assistant is set up to score answers."
		} else if m, _ = apply.LoadMock(dir, n); m.Answered() == 0 {
			msg = "Write at least one answer first."
		} else if _, err := v.cfg.Runner.Start(j, runner.Request{Steps: []string{"mock"}, Force: true,
			Extra: map[string]string{"mock": fmt.Sprintf("The mock file is %s in the application folder; N is %d.", apply.MockName(n), n)}}); err != nil {
			msg = err.Error()
		} else {
			msg = "Your assistant is scoring the answers. This page shows the feedback when it is done."
		}
	}
	http.Redirect(w, r, self+"?msg="+url.QueryEscape(msg), http.StatusSeeOther)
}

// companyFor is the company panel, only on the Interviews tab that shows it.
func (v *server) companyFor(j store.Job, tab string) *companyPanel {
	if tab != "interviews" {
		return nil
	}
	c := v.company(j)
	return &c
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
