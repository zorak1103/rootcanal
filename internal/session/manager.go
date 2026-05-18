package session

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"gitlab.com/zorak1103/rootcanal/internal/config"
	"gitlab.com/zorak1103/rootcanal/internal/hostpool"
	"golang.org/x/crypto/ssh"
)

const (
	ptyTerm   = "xterm-256color"
	ptyHeight = 40
	ptyWidth  = 120
	quiesce   = 50 * time.Millisecond
)

// SessionInfo is a snapshot of a session's metadata.
type SessionInfo struct {
	ID         string
	Host       string
	OpenedAt   time.Time
	LastUsedAt time.Time
}

// Manager manages persistent SSH shell sessions.
type Manager interface {
	Open(ctx context.Context, host string) (id string, err error)
	Send(ctx context.Context, id string, input []byte, timeout time.Duration) (output []byte, truncated, closed bool, err error)
	Close(ctx context.Context, id string) error
	List() []SessionInfo
	Shutdown(ctx context.Context) error
}

// newSessionFn opens a new SSH session for the named host.
// The returned release func decrements the pool refcount.
type newSessionFn func(ctx context.Context, host string) (sshSession, func(), error)

// NewManager creates a Manager backed by pool.
func NewManager(cfg *config.Config, pool *hostpool.Pool, log *slog.Logger) Manager {
	return newManager(cfg, func(ctx context.Context, host string) (sshSession, func(), error) {
		client, release, err := pool.Get(ctx, host)
		if err != nil {
			return nil, nil, err
		}
		raw, err := client.NewSession()
		if err != nil {
			release()
			return nil, nil, fmt.Errorf("creating SSH session for %q: %w", host, err)
		}
		return &realSSHSession{raw}, release, nil
	}, log)
}

func newManager(cfg *config.Config, factory newSessionFn, log *slog.Logger) *manager {
	if log == nil {
		log = slog.Default()
	}
	m := &manager{
		cfg:      cfg,
		factory:  factory,
		log:      log,
		sessions: make(map[string]*session),
		perHost:  make(map[string]int),
		gcStop:   make(chan struct{}),
		gcDone:   make(chan struct{}),
	}
	go m.runGC()
	return m
}

type manager struct {
	cfg     *config.Config
	factory newSessionFn
	log     *slog.Logger

	mu       sync.RWMutex
	sessions map[string]*session
	perHost  map[string]int
	pending  int // in-flight Open() calls that have reserved a slot but not yet registered
	stopping bool

	gcStop chan struct{}
	gcDone chan struct{}
}

func (m *manager) Open(ctx context.Context, host string) (string, error) {
	if _, ok := m.cfg.Hosts[host]; !ok {
		return "", fmt.Errorf("unknown host %q", host)
	}

	limits := m.cfg.Limits

	// Atomically reserve a session slot before calling the factory.
	// Both checks happen under the same lock so no concurrent Open() can
	// slip through the gap between check and increment.
	m.mu.Lock()
	if m.stopping {
		m.mu.Unlock()
		return "", fmt.Errorf("manager is shutting down")
	}
	if limits.MaxSessionsTotal > 0 && len(m.sessions)+m.pending >= limits.MaxSessionsTotal {
		m.mu.Unlock()
		return "", fmt.Errorf("global session limit of %d reached", limits.MaxSessionsTotal)
	}
	if limits.MaxSessionsPerHost > 0 && m.perHost[host] >= limits.MaxSessionsPerHost {
		m.mu.Unlock()
		return "", fmt.Errorf("host %q: per-host session limit of %d reached", host, limits.MaxSessionsPerHost)
	}
	m.pending++
	m.perHost[host]++
	m.mu.Unlock()

	// On any failure path, roll back the reservation.
	handedOff := false
	defer func() {
		if !handedOff {
			m.mu.Lock()
			m.pending--
			m.perHost[host]--
			if m.perHost[host] <= 0 {
				delete(m.perHost, host)
			}
			m.mu.Unlock()
		}
	}()

	sshSess, releasePool, err := m.factory(ctx, host)
	if err != nil {
		return "", err
	}

	buf := newRingBuf(limits.OutputBufferBytes)
	sshSess.setOutput(buf)

	if err := sshSess.RequestPty(ptyTerm, ptyHeight, ptyWidth, ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}); err != nil {
		_ = sshSess.Close()
		releasePool()
		return "", fmt.Errorf("requesting PTY for %q: %w", host, err)
	}

	stdin, err := sshSess.StdinPipe()
	if err != nil {
		_ = sshSess.Close()
		releasePool()
		return "", fmt.Errorf("getting stdin pipe for %q: %w", host, err)
	}

	if err := sshSess.Shell(); err != nil {
		_ = sshSess.Close()
		releasePool()
		return "", fmt.Errorf("starting shell on %q: %w", host, err)
	}

	now := time.Now()
	id := newSessionID()
	done := make(chan struct{})
	s := &session{
		id:          id,
		host:        host,
		sshSess:     sshSess,
		stdin:       stdin,
		releasePool: releasePool,
		openedAt:    now,
		lastUsedAt:  now,
		out:         buf,
		done:        done,
	}

	m.mu.Lock()
	if m.stopping {
		m.mu.Unlock()
		_ = sshSess.Close()
		releasePool()
		return "", fmt.Errorf("manager is shutting down")
	}
	m.sessions[id] = s
	m.pending-- // reservation fulfilled; slot now counted in len(m.sessions)
	handedOff = true
	m.mu.Unlock()

	// Launch Wait goroutine after registration so Shutdown cannot miss this session.
	go func() {
		_ = sshSess.Wait()
		close(done)
	}()

	m.log.Info("session opened", "id", id, "host", host)
	return id, nil
}

func (m *manager) Send(ctx context.Context, id string, input []byte, timeout time.Duration) ([]byte, bool, bool, error) {
	m.mu.RLock()
	s, ok := m.sessions[id]
	m.mu.RUnlock()
	if !ok {
		return nil, false, false, fmt.Errorf("session %q not found", id)
	}

	limits := m.cfg.Limits
	if timeout <= 0 {
		timeout = time.Duration(limits.DefaultSendTimeoutMs) * time.Millisecond
	}
	maxTimeout := time.Duration(limits.MaxSendTimeoutMs) * time.Millisecond
	if maxTimeout > 0 && timeout > maxTimeout {
		timeout = maxTimeout
	}

	s.sendMu.Lock()
	defer s.sendMu.Unlock()

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, false, true, nil
	}
	s.lastUsedAt = time.Now()
	s.mu.Unlock()

	if len(input) > 0 {
		ch := make(chan error, 1)
		go func() {
			_, err := s.stdin.Write(input)
			ch <- err
		}()
		select {
		case err := <-ch:
			if err != nil {
				return nil, false, false, fmt.Errorf("writing to session %q stdin: %w", id, err)
			}
		case <-ctx.Done():
			return nil, false, false, ctx.Err()
		}
	}

	s.out.WaitForData(ctx, quiesce, timeout)

	output, truncated := s.out.Drain()

	var closed bool
	select {
	case <-s.done:
		closed = true
	default:
	}

	return output, truncated, closed, nil
}

func (m *manager) Close(_ context.Context, id string) error {
	m.mu.Lock()
	s, ok := m.sessions[id]
	if ok {
		delete(m.sessions, id)
		m.perHost[s.host]--
		if m.perHost[s.host] <= 0 {
			delete(m.perHost, s.host)
		}
	}
	m.mu.Unlock()

	if !ok {
		return fmt.Errorf("session %q not found", id)
	}

	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()

	// Wait for any in-flight Send to complete before closing pipes.
	s.sendMu.Lock()
	defer s.sendMu.Unlock()

	_ = s.stdin.Close()
	_ = s.sshSess.Close()
	s.releasePool()

	m.log.Info("session closed", "id", id, "host", s.host)
	return nil
}

func (m *manager) List() []SessionInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	infos := make([]SessionInfo, 0, len(m.sessions))
	for _, s := range m.sessions {
		s.mu.Lock()
		info := SessionInfo{
			ID:         s.id,
			Host:       s.host,
			OpenedAt:   s.openedAt,
			LastUsedAt: s.lastUsedAt,
		}
		s.mu.Unlock()
		infos = append(infos, info)
	}
	return infos
}

func (m *manager) Shutdown(ctx context.Context) error {
	close(m.gcStop)
	select {
	case <-m.gcDone:
	case <-ctx.Done():
	}

	m.mu.Lock()
	m.stopping = true
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	m.mu.Unlock()

	for _, id := range ids {
		if ctx.Err() != nil {
			break
		}
		_ = m.Close(ctx, id)
	}
	return ctx.Err()
}

func (m *manager) runGC() {
	defer close(m.gcDone)

	limits := m.cfg.Limits
	idleTimeout := limits.DefaultIdleTimeout
	maxAge := limits.MaxSessionAge

	interval := min(idleTimeout, maxAge) / 4
	interval = min(interval, 60*time.Second)
	interval = max(interval, time.Second)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-m.gcStop:
			return
		case <-ticker.C:
			m.gcTick(idleTimeout, maxAge)
		}
	}
}

func (m *manager) gcTick(idleTimeout, maxAge time.Duration) {
	m.mu.RLock()
	var snapshot []string
	for id, s := range m.sessions {
		if s.isExpired(idleTimeout, maxAge) {
			snapshot = append(snapshot, id)
		}
	}
	m.mu.RUnlock()

	for _, id := range snapshot {
		m.log.Info("GC closing idle session", "id", id)
		_ = m.Close(context.Background(), id)
	}
}
