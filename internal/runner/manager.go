package runner

import (
	"context"
	"fmt"
	"sync"

	"github.com/zoolcoder/scoutjobs/internal/store"
)

// Manager runs preparations in the background for the dashboard and the API, one per job at a time.
type Manager struct {
	*Runner
	mu      sync.Mutex
	runs    map[string]*Run
	latest  map[int64]*Run
	cancels map[string]context.CancelFunc
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
