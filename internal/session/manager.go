package session

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
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

// SessionInfo is a snapshot of a session's metadata returned by List.
type SessionInfo struct {
	ID           string
	Name         string
	Host         string
	OpenedAt     time.Time
	LastUsedAt   time.Time
	LastExitCode *int
	StillRunning bool
}

// Manager manages persistent SSH shell sessions.
type Manager interface {
	Open(ctx context.Context, host, name string) (id string, err error)
	Send(ctx context.Context, id string, in SendInput) (SendResult, error)
	Close(ctx context.Context, id string) (closedReason string, err error)
	List() []SessionInfo
	RunOnce(ctx context.Context, host string, in RunOnceInput) (RunOnceOutput, error)
	Shutdown(ctx context.Context) error
}

// newSessionFn opens a new SSH session for the named host.
// The returned release func decrements the pool refcount.
type newSessionFn func(ctx context.Context, host string) (sshSession, func(), error)

// NewManager creates a Manager backed by pool.
func NewManager(cfg *config.Config, pool *hostpool.Pool, log *slog.Logger) Manager {
	m := newManager(cfg, func(ctx context.Context, host string) (sshSession, func(), error) {
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
	m.pool = pool
	return m
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
	pool    *hostpool.Pool // nil in tests that don't exercise RunOnce

	mu       sync.RWMutex
	sessions map[string]*session
	perHost  map[string]int
	pending  int // in-flight Open() calls that have reserved a slot but not yet registered
	stopping bool

	gcStop chan struct{}
	gcDone chan struct{}
}

func (m *manager) Open(ctx context.Context, host, name string) (string, error) {
	if _, ok := m.cfg.Hosts[host]; !ok {
		return "", fmt.Errorf("unknown host %q", host)
	}

	if name != "" {
		if err := validateName(name); err != nil {
			return "", err
		}
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
	if name != "" {
		for _, s := range m.sessions {
			if s.name == name {
				m.mu.Unlock()
				return "", fmt.Errorf("session name %q already in use", name)
			}
		}
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
	// Create session struct before bootSession so it can write/read.
	s := &session{
		name:        name,
		host:        host,
		sshSess:     sshSess,
		stdin:       stdin,
		releasePool: releasePool,
		openedAt:    now,
		lastUsedAt:  now,
		out:         buf,
		done:        make(chan struct{}),
	}

	// Suppress MOTD and set up a clean shell environment.
	if err := m.bootSession(ctx, s, 5*time.Second); err != nil {
		_ = sshSess.Close()
		releasePool()
		return "", fmt.Errorf("booting shell on %q: %w", host, err)
	}

	id := newSessionID()
	s.id = id

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
		err := sshSess.Wait()
		s.mu.Lock()
		if !s.closed { // explicit Close() already set this
			s.closed = true
			var exitErr *ssh.ExitError
			var missingErr *ssh.ExitMissingError
			switch {
			case err == nil:
				s.closedReason = "exit"
			case errors.As(err, &exitErr):
				s.closedReason = "exit"
			case errors.As(err, &missingErr):
				s.closedReason = "lost"
			default:
				s.closedReason = "lost"
			}
		}
		s.mu.Unlock()
		close(s.done)
	}()

	m.log.Info("session opened", "id", id, "host", host)
	return id, nil
}

// bootSession initialises a freshly started shell: suppresses MOTD and sets
// up a clean environment (no echo, empty PS1/PS2). It writes a ready marker
// and blocks until the shell echoes it back or maxWait is exceeded.
func (m *manager) bootSession(ctx context.Context, s *session, maxWait time.Duration) error {
	nonce := newMarkerNonce()
	bootCmd := fmt.Sprintf(
		"stty -echo 2>/dev/null; export PS1='' PS2=''; printf '\\nRC_READY_%s\\n'\n",
		nonce,
	)
	if _, err := s.stdin.Write([]byte(bootCmd)); err != nil {
		return fmt.Errorf("boot: write: %w", err)
	}

	marker := []byte("RC_READY_" + nonce)
	deadline := time.Now().Add(maxWait)
	var accumulated []byte

	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("session boot timeout after %s: shell did not respond to ready marker", maxWait)
		}
		bCtx, cancel := context.WithDeadline(ctx, deadline)
		s.out.WaitForData(bCtx, 100*time.Millisecond, remaining)
		cancel()

		chunk, _ := s.out.Drain()
		accumulated = append(accumulated, chunk...)

		if bytes.Contains(accumulated, marker) {
			return nil
		}
		if ctx.Err() != nil {
			return fmt.Errorf("boot: %w", ctx.Err())
		}
	}
}

func (m *manager) Send(ctx context.Context, id string, in SendInput) (SendResult, error) {
	m.mu.RLock()
	s, ok := m.sessions[id]
	m.mu.RUnlock()
	if !ok {
		return SendResult{}, fmt.Errorf("session %q not found", id)
	}

	if in.Input != "" && in.WaitIdleMs > 0 {
		return SendResult{}, fmt.Errorf("input and wait_idle_ms are mutually exclusive")
	}

	var warnings []string
	timeout := time.Duration(in.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = time.Duration(m.cfg.Limits.DefaultSendTimeoutMs) * time.Millisecond
	}
	maxTimeout := time.Duration(m.cfg.Limits.MaxSendTimeoutMs) * time.Millisecond
	if maxTimeout > 0 && timeout > maxTimeout {
		warnings = append(warnings, fmt.Sprintf("timeout_ms clamped from %d to %d",
			in.TimeoutMs, m.cfg.Limits.MaxSendTimeoutMs))
		timeout = maxTimeout
	}

	s.sendMu.Lock()
	defer s.sendMu.Unlock()

	s.mu.Lock()
	if s.closed {
		cr := s.closedReason
		s.mu.Unlock()
		return SendResult{ClosedReason: cr, Warnings: warnings}, nil
	}

	// Peek mode: wait for idle, no marker injection.
	if in.WaitIdleMs > 0 {
		s.lastUsedAt = time.Now()
		s.mu.Unlock()
		idleDur := time.Duration(in.WaitIdleMs) * time.Millisecond
		s.out.WaitForData(ctx, idleDur, idleDur)
		out, trunc := s.out.Drain()
		s.mu.Lock()
		cr := s.closedReason
		s.mu.Unlock()
		return SendResult{
			Output:       string(cleanOutput(out)),
			Truncated:    trunc,
			ClosedReason: cr,
			Warnings:     warnings,
		}, nil
	}

	// Continuation mode: empty input waits for in-flight marker.
	if in.Input == "" {
		if s.inflight == nil {
			s.mu.Unlock()
			return SendResult{Warnings: warnings}, nil
		}
		nonce := s.inflight.nonce
		s.mu.Unlock()
		return m.waitForMarker(ctx, s, nonce, timeout, in.Raw, warnings)
	}

	// New command: reject if another is in flight.
	if s.inflight != nil {
		s.mu.Unlock()
		return SendResult{}, fmt.Errorf("command still in flight; send empty input to continue waiting")
	}

	// Raw mode: write as-is, no marker.
	if in.Raw {
		s.lastUsedAt = time.Now()
		s.mu.Unlock()
		ch := make(chan error, 1)
		go func() { _, err := s.stdin.Write([]byte(in.Input)); ch <- err }()
		select {
		case err := <-ch:
			if err != nil {
				return SendResult{}, fmt.Errorf("writing to session %q stdin: %w", id, err)
			}
		case <-ctx.Done():
			return SendResult{}, ctx.Err()
		}
		s.out.WaitForData(ctx, quiesce, timeout)
		out, trunc := s.out.Drain()
		s.mu.Lock()
		cr := s.closedReason
		s.mu.Unlock()
		return SendResult{Output: string(out), Truncated: trunc, ClosedReason: cr, Warnings: warnings}, nil
	}

	// Normal mode: inject exit marker.
	nonce := newMarkerNonce()
	s.inflight = &inflight{nonce: nonce, input: in.Input}
	s.lastUsedAt = time.Now()
	s.mu.Unlock()

	cmd := fmt.Sprintf("%s; printf '\\nRC_EXIT_%s_%%d\\n' $?\n", in.Input, nonce)
	ch := make(chan error, 1)
	go func() { _, err := s.stdin.Write([]byte(cmd)); ch <- err }()
	select {
	case err := <-ch:
		if err != nil {
			s.mu.Lock()
			s.inflight = nil
			s.mu.Unlock()
			return SendResult{}, fmt.Errorf("writing to session %q stdin: %w", id, err)
		}
	case <-ctx.Done():
		s.mu.Lock()
		s.inflight = nil
		s.mu.Unlock()
		return SendResult{}, ctx.Err()
	}

	return m.waitForMarker(ctx, s, nonce, timeout, false, warnings)
}

func (m *manager) waitForMarker(
	ctx context.Context,
	s *session,
	nonce string,
	timeout time.Duration,
	raw bool,
	warnings []string,
) (SendResult, error) {
	markerPrefix := []byte("\nRC_EXIT_" + nonce + "_")
	var accumulated []byte
	var trunc bool
	deadline := time.Now().Add(timeout)

	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		bCtx, cancel := context.WithDeadline(ctx, deadline)
		s.out.WaitForData(bCtx, quiesce, remaining)
		cancel()

		chunk, t := s.out.Drain()
		accumulated = append(accumulated, chunk...)
		if t {
			trunc = true
		}

		if before, rest, found := bytes.Cut(accumulated, markerPrefix); found {
			// Extract exit code from: \nRC_EXIT_<nonce>_<code>\n
			end := bytes.IndexByte(rest, '\n')
			if end < 0 {
				end = len(rest)
			}
			code, _ := strconv.Atoi(strings.TrimSpace(string(rest[:end])))

			output := before
			if !raw {
				output = cleanOutput(output)
			}

			s.mu.Lock()
			s.inflight = nil
			ec := code
			s.lastExitCode = &ec
			s.lastUsedAt = time.Now()
			cr := s.closedReason
			s.mu.Unlock()

			return SendResult{
				Output:       string(output),
				ExitCode:     &code,
				Truncated:    trunc,
				ClosedReason: cr,
				Warnings:     warnings,
			}, nil
		}

		// Check if session closed while waiting.
		select {
		case <-s.done:
			output := accumulated
			if !raw {
				output = cleanOutput(output)
			}
			s.mu.Lock()
			s.inflight = nil
			cr := s.closedReason
			s.lastUsedAt = time.Now()
			s.mu.Unlock()
			return SendResult{
				Output:       string(output),
				Truncated:    trunc,
				ClosedReason: cr,
				Warnings:     warnings,
			}, nil
		default:
		}

		if ctx.Err() != nil {
			s.mu.Lock()
			s.inflight = nil
			s.mu.Unlock()
			return SendResult{}, ctx.Err()
		}
	}

	// Timeout without marker: keep inflight so continuation works.
	// DO NOT clear s.inflight here — the caller uses empty-input Send to continue.
	output := accumulated
	if !raw {
		output = cleanOutput(output)
	}
	s.mu.Lock()
	s.lastUsedAt = time.Now()
	s.mu.Unlock()

	return SendResult{
		Output:       string(output),
		StillRunning: true,
		Truncated:    trunc,
		Warnings:     warnings,
	}, nil
}

func (m *manager) Close(_ context.Context, id string) (string, error) {
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
		return "", fmt.Errorf("session %q not found", id)
	}

	s.mu.Lock()
	s.closed = true
	s.closedReason = "explicit"
	s.mu.Unlock()

	s.sendMu.Lock()
	defer s.sendMu.Unlock()

	_ = s.stdin.Close()
	_ = s.sshSess.Close()
	s.releasePool()

	m.log.Info("session closed", "id", id, "host", s.host)
	return "explicit", nil
}

func (m *manager) List() []SessionInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	infos := make([]SessionInfo, 0, len(m.sessions))
	for _, s := range m.sessions {
		s.mu.Lock()
		info := SessionInfo{
			ID:           s.id,
			Name:         s.name,
			Host:         s.host,
			OpenedAt:     s.openedAt,
			LastUsedAt:   s.lastUsedAt,
			LastExitCode: s.lastExitCode,
			StillRunning: s.inflight != nil,
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
		m.mu.RLock()
		s, ok := m.sessions[id]
		m.mu.RUnlock()
		if ok {
			s.mu.Lock()
			if s.closedReason == "" {
				s.closedReason = "shutdown"
			}
			s.mu.Unlock()
		}
		_, _ = m.Close(ctx, id)
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
		// Set GC reason before Close so it's visible in SendResult.ClosedReason.
		m.mu.RLock()
		s, ok := m.sessions[id]
		m.mu.RUnlock()
		if ok {
			s.mu.Lock()
			if s.closedReason == "" {
				if time.Since(s.lastUsedAt) >= idleTimeout {
					s.closedReason = "idle"
				} else {
					s.closedReason = "max_age"
				}
			}
			s.mu.Unlock()
		}
		m.log.Info("GC closing idle session", "id", id)
		_, _ = m.Close(context.Background(), id)
	}
}
