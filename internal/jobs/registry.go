package jobs

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

const tailCap = 64 * 1024 // 64 KiB per stream

// Job holds the state of a single detached execution.
type Job struct {
	ID        string
	Host      string
	Command   string
	StartedAt time.Time

	mu              sync.Mutex
	finishedAt      *time.Time
	exitCode        *int
	stdout          []byte
	stderr          []byte
	cancelFn        func()
	cancelRequested bool
}

// Running reports whether the job is still executing.
func (j *Job) Running() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.finishedAt == nil
}

// ExitCode returns the exit code if finished, or nil if still running or signal-killed.
func (j *Job) ExitCode() *int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.exitCode
}

// FinishedAt returns the finish time if done, or nil if still running.
func (j *Job) FinishedAt() *time.Time {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.finishedAt
}

// ElapsedSeconds returns seconds elapsed since the job started.
func (j *Job) ElapsedSeconds() int {
	j.mu.Lock()
	end := j.finishedAt
	j.mu.Unlock()
	if end == nil {
		return int(time.Since(j.StartedAt).Seconds())
	}
	return int(end.Sub(j.StartedAt).Seconds())
}

// StdoutTail returns up to n bytes from the end of the stdout buffer.
func (j *Job) StdoutTail(n int) string {
	j.mu.Lock()
	defer j.mu.Unlock()
	b := j.stdout
	if len(b) > n {
		b = b[len(b)-n:]
	}
	return string(b)
}

// StderrTail returns up to n bytes from the end of the stderr buffer.
func (j *Job) StderrTail(n int) string {
	j.mu.Lock()
	defer j.mu.Unlock()
	b := j.stderr
	if len(b) > n {
		b = b[len(b)-n:]
	}
	return string(b)
}

func appendCapped(buf, data []byte) []byte {
	buf = append(buf, data...)
	if len(buf) > tailCap {
		buf = buf[len(buf)-tailCap:]
	}
	return buf
}

// Registry is a bounded, TTL-evicting store of detached jobs.
type Registry struct {
	mu        sync.Mutex
	jobs      map[string]*Job
	maxJobs   int
	ttl       time.Duration
	stopCh    chan struct{}
	closeOnce sync.Once
}

// NewRegistry creates a Registry with the given job cap and finished-job TTL.
func NewRegistry(maxJobs int, ttl time.Duration) *Registry {
	r := &Registry{
		jobs:    make(map[string]*Job),
		maxJobs: maxJobs,
		ttl:     ttl,
		stopCh:  make(chan struct{}),
	}
	go r.reaperLoop()
	return r
}

// Close stops the background reaper goroutine.
func (r *Registry) Close() {
	r.closeOnce.Do(func() { close(r.stopCh) })
}

func newJobID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "j_" + hex.EncodeToString(b)
}

// TryRegister creates a new running job and returns its ID.
// Returns an error if the registry is full.
func (r *Registry) TryRegister(host, command string, pid int) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.maxJobs > 0 && len(r.jobs) >= r.maxJobs {
		return "", fmt.Errorf("job registry full (%d/%d active jobs)", len(r.jobs), r.maxJobs)
	}
	id := newJobID()
	r.jobs[id] = &Job{
		ID:        id,
		Host:      host,
		Command:   command,
		StartedAt: time.Now(),
	}
	_ = pid // PID not available over SSH exec channels; reserved for future use
	return id, nil
}

// Get returns the job with the given ID, or (nil, false) if not found.
func (r *Registry) Get(id string) (*Job, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	j, ok := r.jobs[id]
	return j, ok
}

// MarkDone marks a job as finished. exitCode may be nil for signal-killed jobs.
func (r *Registry) MarkDone(id string, exitCode *int) {
	r.mu.Lock()
	j, ok := r.jobs[id]
	r.mu.Unlock()
	if !ok {
		return
	}
	j.mu.Lock()
	now := time.Now()
	j.finishedAt = &now
	j.exitCode = exitCode
	j.cancelFn = nil // prevent stale cancels after job completes
	j.mu.Unlock()
}

// SetCancel registers a cancel function for the job. If cancel was already
// requested before SetCancel was called, fn is invoked immediately.
func (r *Registry) SetCancel(id string, fn func()) {
	r.mu.Lock()
	j, ok := r.jobs[id]
	r.mu.Unlock()
	if !ok {
		return
	}
	j.mu.Lock()
	j.cancelFn = fn
	wasCanceled := j.cancelRequested
	j.mu.Unlock()
	if wasCanceled {
		fn()
	}
}

// Cancel calls the job's cancel function if it is still running.
// If SetCancel has not been called yet, the request is recorded and fn will
// be called immediately when SetCancel is eventually invoked.
func (r *Registry) Cancel(id string) {
	r.mu.Lock()
	j, ok := r.jobs[id]
	r.mu.Unlock()
	if !ok {
		return
	}
	j.mu.Lock()
	j.cancelRequested = true
	fn := j.cancelFn
	j.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// AppendStdout appends bytes to the job's stdout tail (capped at 64 KiB).
func (r *Registry) AppendStdout(id string, data []byte) {
	r.mu.Lock()
	j, ok := r.jobs[id]
	r.mu.Unlock()
	if !ok {
		return
	}
	j.mu.Lock()
	j.stdout = appendCapped(j.stdout, data)
	j.mu.Unlock()
}

// AppendStderr appends bytes to the job's stderr tail (capped at 64 KiB).
func (r *Registry) AppendStderr(id string, data []byte) {
	r.mu.Lock()
	j, ok := r.jobs[id]
	r.mu.Unlock()
	if !ok {
		return
	}
	j.mu.Lock()
	j.stderr = appendCapped(j.stderr, data)
	j.mu.Unlock()
}

// Reap evicts finished jobs older than the TTL.
// Called automatically by the reaper goroutine; also exported for deterministic tests.
func (r *Registry) Reap() {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	for id, j := range r.jobs {
		j.mu.Lock()
		finished := j.finishedAt
		j.mu.Unlock()
		if finished != nil && now.Sub(*finished) > r.ttl {
			delete(r.jobs, id)
		}
	}
}

func (r *Registry) reaperLoop() {
	half := max(r.ttl/2, 5*time.Second)
	ticker := time.NewTicker(half)
	defer ticker.Stop()
	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.Reap()
		}
	}
}
