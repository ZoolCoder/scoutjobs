package runner

import (
	"context"
	"fmt"
	"sync"

	"github.com/zoolcoder/scoutjobs/internal/apply"
	"github.com/zoolcoder/scoutjobs/internal/store"
)

// Manager runs preparations in the background for the dashboard and the API, one per job at a time.
type Manager struct {
	*Runner
	mu      sync.Mutex
	runs    map[string]*Run
	latest  map[int64]*Run
	cancels map[string]context.CancelFunc
	// Queue: jobs prepared one at a time
	queue    []queued
	queued   map[int64]bool
	draining bool
}

type queued struct {
	job store.Job
	req Request
}

func NewManager(r *Runner) *Manager {
	return &Manager{Runner: r, runs: map[string]*Run{}, latest: map[int64]*Run{}, cancels: map[string]context.CancelFunc{}}
}

// Start plans the run and, unless it is a dry run, starts it. A job with a run in progress is refused.
func (m *Manager) Start(j store.Job, req Request) (*Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r := m.latest[j.ID]; r != nil && r.Snapshot().State == Running {
		return r, fmt.Errorf("job #%d already has a run in progress (%s)", j.ID, r.ID)
	}
	run, ag, err := m.Plan(j, req)
	if err != nil {
		return nil, err
	}
	m.runs[run.ID], m.latest[j.ID] = run, run
	ctx, cancel := context.WithCancel(context.Background())
	m.cancels[run.ID] = cancel
	go func() {
		defer cancel()
		m.Exec(ctx, run, ag, j, nil)
	}()
	return run, nil
}

// Get returns a run by id.
func (m *Manager) Get(id string) (*Run, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[id]
	return r, ok
}

// Latest returns the newest run of a job.
func (m *Manager) Latest(jobID int64) *Run {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.latest[jobID]
}

// Cancel stops a run; the agent process is killed and the steps not reached stay undone.
func (m *Manager) Cancel(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.cancels[id]
	if ok {
		c()
	}
	return ok
}

// StartMaster starts the run that writes the main CV and cover letter; it is kept under MasterID.
func (m *Manager) StartMaster(req Request) (*Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r := m.latest[MasterID]; r != nil && r.Snapshot().State == Running {
		return r, fmt.Errorf("the main CV is already being written (%s)", r.ID)
	}
	run, ag, err := m.PlanMaster(req)
	if err != nil {
		return nil, err
	}
	m.runs[run.ID], m.latest[MasterID] = run, run
	ctx, cancel := context.WithCancel(context.Background())
	m.cancels[run.ID] = cancel
	go func() {
		defer cancel()
		m.ExecMaster(ctx, run, ag, nil)
	}()
	return run, nil
}

// Candidates are the open jobs worth a draft: new or ranked, no veto, fit score at or above floor,
// and no tailored resume or cover letter yet.
func (m *Manager) Candidates(floor int) ([]store.Job, error) {
	jobs, err := m.Store.ListJobs(store.JobFilter{Open: true})
	if err != nil {
		return nil, err
	}
	var out []store.Job
	for _, j := range jobs {
		if (j.Status != "new" && j.Status != "ranked") || j.Veto != "" || j.FitScore < int64(floor) {
			continue
		}
		dir := apply.Dir(m.Store, j)
		if exists(dir, "resume.md") && exists(dir, "cover-letter.md") {
			continue
		}
		out = append(out, j)
	}
	return out, nil
}

// Queue prepares the jobs one after another in the background, so only one agent runs at a time.
// Jobs already queued or running are left out; it returns how many were added.
func (m *Manager) Queue(jobs []store.Job, req Request) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.queued == nil {
		m.queued = map[int64]bool{}
	}
	n := 0
	for _, j := range jobs {
		if m.queued[j.ID] {
			continue
		}
		if r := m.latest[j.ID]; r != nil && r.Snapshot().State == Running {
			continue
		}
		m.queued[j.ID] = true
		m.queue = append(m.queue, queued{j, req})
		n++
	}
	if n > 0 && !m.draining {
		m.draining = true
		go m.drain()
	}
	return n
}

// Waiting returns the ids of the jobs queued but not started yet.
func (m *Manager) Waiting() []int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]int64, 0, len(m.queue))
	for _, q := range m.queue {
		out = append(out, q.job.ID)
	}
	return out
}

func (m *Manager) drain() {
	for {
		m.mu.Lock()
		if len(m.queue) == 0 {
			m.draining = false
			m.mu.Unlock()
			return
		}
		q := m.queue[0]
		m.queue = m.queue[1:]
		delete(m.queued, q.job.ID)
		m.mu.Unlock()
		run, err := m.Start(q.job, q.req)
		if err != nil {
			_ = m.Store.AddEvent(q.job.ID, "run:failed", "queued preparation: "+err.Error())
			continue
		}
		run.Wait()
	}
}

// Busy counts the jobs being prepared now plus those waiting in the queue, for the navigation.
func (m *Manager) Busy() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := len(m.queue)
	for _, r := range m.latest {
		if r.Snapshot().State == Running {
			n++
		}
	}
	return n
}
