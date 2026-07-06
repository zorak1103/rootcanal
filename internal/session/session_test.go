package session

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/zorak1103/rootcanal/internal/config"
	"github.com/zorak1103/rootcanal/internal/hostpool"
	"github.com/zorak1103/rootcanal/internal/sshconn"
	"golang.org/x/crypto/ssh"
)

// ---- fake sshSession ----

type fakeSession struct {
	outWriter io.Writer
	stdinCh   chan []byte
	closeCh   chan struct{}
	closeOnce sync.Once
	shellErr  error
	ptyErr    error
}

func newFakeSession() *fakeSession {
	return &fakeSession{
		stdinCh: make(chan []byte, 16),
		closeCh: make(chan struct{}),
	}
}

func (f *fakeSession) setOutput(w io.Writer) { f.outWriter = w }

func (f *fakeSession) StdinPipe() (io.WriteCloser, error) {
	return &fakeStdin{ch: f.stdinCh}, nil
}

func (f *fakeSession) RequestPty(_ string, _, _ int, _ ssh.TerminalModes) error {
	return f.ptyErr
}

func (f *fakeSession) Shell() error {
	if f.shellErr != nil {
		return f.shellErr
	}
	go func() {
		for {
			select {
			case input := <-f.stdinCh:
				if f.outWriter == nil {
					continue
				}
				// Boot marker: respond with the RC_READY_<nonce> marker.
				if idx := bytes.Index(input, []byte("RC_READY_")); idx != -1 {
					rest := input[idx:]
					if end := bytes.IndexByte(rest, '\n'); end > 0 {
						marker := rest[:end] // "RC_READY_<nonce>"
						_, _ = f.outWriter.Write(append(append([]byte("\n"), marker...), '\n'))
					}
					continue
				}
				// Exit marker: "<user_cmd>; printf '\nRC_EXIT_<nonce>_%d\n' $?\n"
				// Respond with exit code 0.
				if _, after, ok := bytes.Cut(input, []byte("RC_EXIT_")); ok {
					// nonce ends at "_%d" (the printf format spec in the wire command)
					if nonce, _, ok := bytes.Cut(after, []byte("_%d")); ok {
						_, _ = f.outWriter.Write([]byte("\nRC_EXIT_" + string(nonce) + "_0\n"))
					}
					continue
				}
				// Default: echo input (used by raw-mode tests and Task 5).
				_, _ = f.outWriter.Write(append([]byte("$ "), input...))
			case <-f.closeCh:
				return
			}
		}
	}()
	return nil
}

func (f *fakeSession) Wait() error {
	<-f.closeCh
	return nil
}

func (f *fakeSession) Close() error {
	f.closeOnce.Do(func() { close(f.closeCh) })
	return nil
}

type fakeStdin struct{ ch chan<- []byte }

func (s *fakeStdin) Write(p []byte) (int, error) {
	b := make([]byte, len(p))
	copy(b, p)
	s.ch <- b
	return len(p), nil
}
func (s *fakeStdin) Close() error { return nil }

// ---- factory helpers ----

func fakeSessions(sessions ...*fakeSession) newSessionFn {
	ifaces := make([]sshSession, len(sessions))
	for i, s := range sessions {
		ifaces[i] = s
	}
	return fakeSessionsIface(ifaces...)
}

// fakeSessionsIface is like fakeSessions but accepts any sshSession implementation.
func fakeSessionsIface(sessions ...sshSession) newSessionFn {
	idx := 0
	return func(_ context.Context, _ string) (sshSession, func(), error) {
		if idx >= len(sessions) {
			return nil, nil, errors.New("no more fake sessions")
		}
		s := sessions[idx]
		idx++
		return s, func() {}, nil
	}
}

func errFactory(err error) newSessionFn {
	return func(_ context.Context, _ string) (sshSession, func(), error) {
		return nil, nil, err
	}
}

func minCfg() *config.Config {
	t := true
	return &config.Config{
		Limits: config.Limits{
			MaxSessionsTotal:     32,
			MaxSessionsPerHost:   4,
			DefaultIdleTimeout:   15 * time.Minute,
			MaxSessionAge:        4 * time.Hour,
			OutputBufferBytes:    4096,
			DefaultSendTimeoutMs: 2000,
			MaxSendTimeoutMs:     30000,
			DefaultTerm:          "dumb",
			DefaultCleanOutput:   &t,
			RunOnceMaxBytes:      1 << 20,
			RunOnceMaxTimeoutMs:  60000,
			MaxRunOnceConcurrent: 16,
		},
		Hosts: map[string]config.Host{
			"h": {Address: "h:22", User: "u", KnownHosts: "system", Auth: config.Auth{Type: "agent"}},
		},
	}
}

// ---- ringBuf tests ----

func TestRingBuf_WriteAndDrain(t *testing.T) {
	rb := newRingBuf(64)
	rb.Write([]byte("hello"))
	got, trunc := rb.Drain()
	if string(got) != "hello" {
		t.Errorf("Drain() = %q, want %q", got, "hello")
	}
	if trunc {
		t.Error("truncated should be false")
	}
}

func TestRingBuf_Overflow(t *testing.T) {
	rb := newRingBuf(4)
	rb.Write([]byte("abcde")) // 5 bytes into 4-byte buf → overflow
	got, trunc := rb.Drain()
	if !trunc {
		t.Error("expected truncated=true on overflow")
	}
	if len(got) != 4 {
		t.Errorf("expected 4 bytes, got %d: %q", len(got), got)
	}
}

func TestRingBuf_EmptyDrain(t *testing.T) {
	rb := newRingBuf(64)
	got, trunc := rb.Drain()
	if got != nil || trunc {
		t.Errorf("empty Drain() = %v, %v; want nil, false", got, trunc)
	}
}

func TestRingBuf_WaitForData_Cancelled(t *testing.T) {
	rb := newRingBuf(64)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled
	rb.WaitForData(ctx, 50*time.Millisecond, 5*time.Second)
	got, _ := rb.Drain()
	if got != nil {
		t.Error("expected no data for immediately cancelled ctx")
	}
}

func TestRingBuf_WaitForData_Timeout(t *testing.T) {
	rb := newRingBuf(64)
	start := time.Now()
	rb.WaitForData(context.Background(), 50*time.Millisecond, 100*time.Millisecond)
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("WaitForData took too long: %v", elapsed)
	}
}

func TestRingBuf_WaitForData_GetsData(t *testing.T) {
	rb := newRingBuf(64)
	go func() {
		time.Sleep(20 * time.Millisecond)
		rb.Write([]byte("data"))
	}()
	rb.WaitForData(context.Background(), 50*time.Millisecond, 2*time.Second)
	got, _ := rb.Drain()
	if !bytes.Contains(got, []byte("data")) {
		t.Errorf("expected 'data' in output, got %q", got)
	}
}

// ---- manager tests ----

func TestManager_Open_UnknownHost(t *testing.T) {
	m := newManager(minCfg(), fakeSessions(), nil)
	defer m.Shutdown(context.Background())
	_, err := m.Open(context.Background(), "unknown", "")
	if err == nil {
		t.Fatal("expected error for unknown host")
	}
}

func TestManager_Open_DialError(t *testing.T) {
	m := newManager(minCfg(), errFactory(errors.New("connection refused")), nil)
	defer m.Shutdown(context.Background())
	_, err := m.Open(context.Background(), "h", "")
	if err == nil {
		t.Fatal("expected dial error")
	}
}

func TestManager_Open_PTYError(t *testing.T) {
	fs := newFakeSession()
	fs.ptyErr = errors.New("pty not supported")
	m := newManager(minCfg(), fakeSessions(fs), nil)
	defer m.Shutdown(context.Background())
	_, err := m.Open(context.Background(), "h", "")
	if err == nil {
		t.Fatal("expected PTY error")
	}
}

func TestManager_Open_MaxTotal(t *testing.T) {
	cfg := minCfg()
	cfg.Limits.MaxSessionsTotal = 1
	f1, f2 := newFakeSession(), newFakeSession()
	m := newManager(cfg, fakeSessions(f1, f2), nil)
	defer m.Shutdown(context.Background())

	_, err := m.Open(context.Background(), "h", "")
	if err != nil {
		t.Fatalf("first Open unexpected error: %v", err)
	}
	_, err = m.Open(context.Background(), "h", "")
	if err == nil {
		t.Fatal("expected error when global limit exceeded")
	}
}

func TestManager_Open_MaxPerHost(t *testing.T) {
	cfg := minCfg()
	cfg.Limits.MaxSessionsPerHost = 1
	cfg.Limits.MaxSessionsTotal = 10
	f1, f2 := newFakeSession(), newFakeSession()
	m := newManager(cfg, fakeSessions(f1, f2), nil)
	defer m.Shutdown(context.Background())

	_, err := m.Open(context.Background(), "h", "")
	if err != nil {
		t.Fatalf("first Open unexpected error: %v", err)
	}
	_, err = m.Open(context.Background(), "h", "")
	if err == nil {
		t.Fatal("expected error when per-host limit exceeded")
	}
}

func TestManager_SendClose_RoundTrip(t *testing.T) {
	fs := newFakeSession()
	m := newManager(minCfg(), fakeSessions(fs), nil)
	defer m.Shutdown(context.Background())

	id, err := m.Open(context.Background(), "h", "")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	res, err := m.Send(context.Background(), id, SendInput{Input: "ls\n", TimeoutMs: 500})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if res.ExitCode == nil {
		t.Error("expected ExitCode to be set after successful Send")
	}

	if _, err := m.Close(context.Background(), id); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(m.List()) != 0 {
		t.Errorf("expected 0 sessions after close, got %d", len(m.List()))
	}
}

func TestManager_Send_UnknownID(t *testing.T) {
	m := newManager(minCfg(), fakeSessions(), nil)
	defer m.Shutdown(context.Background())
	_, err := m.Send(context.Background(), "s_UNKNOWN", SendInput{TimeoutMs: 100})
	if err == nil {
		t.Fatal("expected error for unknown session ID")
	}
}

func TestManager_Close_UnknownID(t *testing.T) {
	m := newManager(minCfg(), fakeSessions(), nil)
	defer m.Shutdown(context.Background())
	if _, err := m.Close(context.Background(), "s_NONE"); err == nil {
		t.Fatal("expected error for unknown session ID")
	}
}

func TestManager_List(t *testing.T) {
	fs := newFakeSession()
	m := newManager(minCfg(), fakeSessions(fs), nil)
	defer m.Shutdown(context.Background())

	if len(m.List()) != 0 {
		t.Fatal("expected 0 sessions before Open")
	}
	id, _ := m.Open(context.Background(), "h", "")
	list := m.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 session, got %d", len(list))
	}
	if list[0].ID != id || list[0].Host != "h" {
		t.Errorf("wrong session info: %+v", list[0])
	}
}

func TestManager_Shutdown(t *testing.T) {
	f1, f2 := newFakeSession(), newFakeSession()
	m := newManager(minCfg(), fakeSessions(f1, f2), nil)
	m.Open(context.Background(), "h", "")
	m.Open(context.Background(), "h", "")
	m.Shutdown(context.Background())
	if len(m.List()) != 0 {
		t.Errorf("expected 0 sessions after Shutdown, got %d", len(m.List()))
	}
}

func TestManager_GC_IdleSession(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := minCfg()
		cfg.Limits.DefaultIdleTimeout = 1 * time.Minute
		cfg.Limits.MaxSessionAge = 4 * time.Hour

		fs := newFakeSession()
		m := newManager(cfg, fakeSessions(fs), nil)

		id, err := m.Open(context.Background(), "h", "")
		if err != nil {
			t.Fatalf("Open: %v", err)
		}

		// Advance fake time past the idle timeout — synctest ticks time forward
		// when all goroutines in the bubble are durably blocked.
		time.Sleep(cfg.Limits.DefaultIdleTimeout + time.Second)
		synctest.Wait()

		for _, s := range m.List() {
			if s.ID == id {
				t.Errorf("GC should have closed idle session %q", id)
			}
		}
		m.Shutdown(context.Background())
	})
}

// ---- newSessionID ----

func TestNewSessionID_PanicOnRandError(t *testing.T) {
	old := randRead
	randRead = func(b []byte) (int, error) { return 0, errors.New("hardware RNG unavailable") }
	t.Cleanup(func() { randRead = old })
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic when randRead fails")
		}
	}()
	newSessionID()
}

func TestNewSessionID_Format(t *testing.T) {
	id := newSessionID()
	if len(id) < 4 || id[:2] != "s_" {
		t.Errorf("unexpected session ID format: %q", id)
	}
}

func TestNewSessionID_Unique(t *testing.T) {
	seen := make(map[string]bool)
	for range 100 {
		id := newSessionID()
		if seen[id] {
			t.Fatalf("duplicate session ID: %q", id)
		}
		seen[id] = true
	}
}

// ---- validateName ----

func TestValidateName(t *testing.T) {
	cases := []struct {
		name    string
		wantErr bool
	}{
		{"myservice", false},
		{"my-service", false},
		{"my.service", false},
		{"my_service", false},
		{"a", false}, // single char valid
		{"abc123", false},
		{"a-b.c_d", false},
		// 63-char max length (1 first char + 62 remaining)
		{"a" + strings.Repeat("b", 62), false},
		// Too long: 64 chars
		{"a" + strings.Repeat("b", 63), true},
		// Reserved prefix
		{"s_abc", true},
		{"s_", true},
		// Invalid start chars
		{"-start", true},
		{".start", true},
		{"_start", true},
		{"Uppercase", true},
		{"", true},
		// Valid chars in body
		{"a-1.2_3", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateName(tc.name)
			if tc.wantErr && err == nil {
				t.Errorf("validateName(%q): expected error, got nil", tc.name)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("validateName(%q): unexpected error: %v", tc.name, err)
			}
		})
	}
}

// ---- newMarkerNonce ----

func TestNewMarkerNonce_Format(t *testing.T) {
	nonce := newMarkerNonce()
	if len(nonce) != 8 {
		t.Errorf("len(newMarkerNonce()) = %d, want 8", len(nonce))
	}
	for _, c := range nonce {
		if !((c >= 'A' && c <= 'Z') || (c >= '2' && c <= '7')) {
			t.Errorf("newMarkerNonce() contains non-base32 char %q in %q", c, nonce)
		}
	}
}

func TestNewMarkerNonce_PanicOnRandError(t *testing.T) {
	old := randRead
	randRead = func(b []byte) (int, error) { return 0, errors.New("rand failed") }
	t.Cleanup(func() { randRead = old })

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic from newMarkerNonce when rand fails")
		}
	}()
	newMarkerNonce()
}

// ---- realSSHSession.setOutput ----

func TestRealSSHSession_setOutput(t *testing.T) {
	var buf bytes.Buffer
	raw := &ssh.Session{}
	s := &realSSHSession{raw}
	s.setOutput(&buf)
	if raw.Stdout != &buf {
		t.Error("setOutput must assign Stdout")
	}
	if raw.Stderr != &buf {
		t.Error("setOutput must assign Stderr")
	}
}

// ---- NewManager production wiring ----

func TestNewManager_Wiring(t *testing.T) {
	// Use an unreachable address so pool.Get fails fast, exercising the factory closure.
	cfg := &config.Config{
		Limits: config.Limits{
			MaxSessionsTotal: 32, MaxSessionsPerHost: 4,
			DefaultIdleTimeout: 15 * time.Minute, MaxSessionAge: 4 * time.Hour,
			OutputBufferBytes: 4096, DefaultSendTimeoutMs: 2000, MaxSendTimeoutMs: 30000,
			DialTimeout: 200 * time.Millisecond,
		},
		Hosts: map[string]config.Host{
			"h": {Address: "127.0.0.1:1", User: "u", KnownHosts: "system",
				Auth: config.Auth{Type: "password", PasswordEnv: "TEST_NM_WIRING_PASS"}},
		},
	}
	t.Setenv("TEST_NM_WIRING_PASS", "x")
	pool := hostpool.New(cfg, sshconn.ProdDialer{})
	mgr := NewManager(cfg, pool, nil)

	// Open invokes the factory closure; connection to 127.0.0.1:1 fails fast.
	_, _ = mgr.Open(context.Background(), "h", "")

	mgr.Shutdown(context.Background())
	pool.Close()
}

// ---- Send on a session whose closed flag is set (in-map closed branch) ----

func TestManager_Send_SessionMarkedClosed(t *testing.T) {
	fs := newFakeSession()
	m := newManager(minCfg(), fakeSessions(fs), nil)
	defer m.Shutdown(context.Background())

	id, err := m.Open(context.Background(), "h", "")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Mark the session closed without removing it from the map.
	m.mu.RLock()
	s := m.sessions[id]
	m.mu.RUnlock()
	s.mu.Lock()
	s.closed = true
	s.closedReason = "explicit"
	s.mu.Unlock()

	res, err := m.Send(context.Background(), id, SendInput{TimeoutMs: 100})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ClosedReason == "" {
		t.Error("expected non-empty ClosedReason for session with closed flag set")
	}
	if res.Output != "" {
		t.Errorf("expected empty output, got %q", res.Output)
	}
}

// ---- ringBuf zero-capacity fallback ----

func TestRingBuf_ZeroCapacity(t *testing.T) {
	rb := newRingBuf(0)
	if rb.cap != 1<<20 {
		t.Errorf("expected 1 MiB fallback capacity, got %d", rb.cap)
	}
}

// ---- ringBuf WaitForData quiescence reset ----

func TestRingBuf_WaitForData_QuiesceReset(t *testing.T) {
	rb := newRingBuf(256)

	go func() {
		time.Sleep(10 * time.Millisecond)
		rb.Write([]byte("first"))
		// Second write arrives during quiescence window — resets the timer.
		time.Sleep(30 * time.Millisecond)
		rb.Write([]byte("second"))
	}()

	rb.WaitForData(context.Background(), 60*time.Millisecond, 2*time.Second)
	got, _ := rb.Drain()
	if !bytes.Contains(got, []byte("first")) || !bytes.Contains(got, []byte("second")) {
		t.Errorf("expected both writes in output, got %q", got)
	}
}

// ---- TOCTOU tests ----

// slowFactory returns a factory that blocks until the returned gate channel is
// closed, then creates a fresh fakeSession. This simulates the SSH dial latency
// that makes the TOCTOU window exploitable.
func slowFactory(gate <-chan struct{}) newSessionFn {
	return func(_ context.Context, _ string) (sshSession, func(), error) {
		<-gate
		return newFakeSession(), func() {}, nil
	}
}

func TestManager_Open_TOCTOU_GlobalCap(t *testing.T) {
	const maxTotal = 3
	cfg := minCfg()
	cfg.Limits.MaxSessionsTotal = maxTotal
	cfg.Limits.MaxSessionsPerHost = 20 // don't trigger per-host limit

	gate := make(chan struct{})
	m := newManager(cfg, slowFactory(gate), nil)
	defer m.Shutdown(context.Background())

	const concurrent = maxTotal + 3 // try to create 6 when cap is 3
	var wg sync.WaitGroup
	var successCount, failCount atomic.Int32
	wg.Add(concurrent)
	for range concurrent {
		go func() {
			defer wg.Done()
			_, err := m.Open(context.Background(), "h", "")
			if err == nil {
				successCount.Add(1)
			} else {
				failCount.Add(1)
			}
		}()
	}

	// Give all goroutines time to pass (or fail at) the limit check.
	time.Sleep(20 * time.Millisecond)
	close(gate) // release the factory for all waiting goroutines
	wg.Wait()

	got := int(successCount.Load())
	if got > maxTotal {
		t.Errorf("global cap bypassed: %d sessions created, cap was %d", got, maxTotal)
	}
	if int(successCount.Load())+int(failCount.Load()) != concurrent {
		t.Errorf("goroutine accounting mismatch: %d success + %d fail != %d total",
			successCount.Load(), failCount.Load(), concurrent)
	}
}

func TestManager_Open_TOCTOU_PerHostCap(t *testing.T) {
	const maxPerHost = 2
	cfg := minCfg()
	cfg.Limits.MaxSessionsTotal = 20
	cfg.Limits.MaxSessionsPerHost = maxPerHost

	gate := make(chan struct{})
	m := newManager(cfg, slowFactory(gate), nil)
	defer m.Shutdown(context.Background())

	const concurrent = maxPerHost + 3 // try to create 5 for cap=2
	var wg sync.WaitGroup
	var successCount atomic.Int32
	wg.Add(concurrent)
	for range concurrent {
		go func() {
			defer wg.Done()
			_, err := m.Open(context.Background(), "h", "")
			if err == nil {
				successCount.Add(1)
			}
		}()
	}

	time.Sleep(20 * time.Millisecond)
	close(gate)
	wg.Wait()

	if got := int(successCount.Load()); got > maxPerHost {
		t.Errorf("per-host cap bypassed: %d sessions for host, cap was %d", got, maxPerHost)
	}
}

func TestManager_Open_BootMarkerDrainsOutput(t *testing.T) {
	// motdFakeSession emits MOTD before responding to the boot marker.
	fs := &motdFakeSession{
		fakeSession: newFakeSession(),
		motd:        "Welcome to Ubuntu 24.04.4 LTS\n\nLast login: Mon May 20 06:43:00 2026\n",
	}
	mgr := newManager(minCfg(), fakeSessionsIface(fs), nil)
	defer mgr.Shutdown(context.Background())

	id, err := mgr.Open(context.Background(), "h", "")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty session ID")
	}

	// The ring buffer should be empty after bootSession drained MOTD.
	// A short-timeout Send with no input should see no buffered output.
	res, err := mgr.Send(context.Background(), id, SendInput{TimeoutMs: 200})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if res.Output != "" {
		t.Errorf("expected empty output after boot (MOTD should be drained), got %q", res.Output)
	}

	_, _ = mgr.Close(context.Background(), id)
}

// motdFakeSession emits a MOTD banner before responding to the boot marker.
type motdFakeSession struct {
	*fakeSession
	motd string
}

func (m *motdFakeSession) Shell() error {
	if m.shellErr != nil {
		return m.shellErr
	}
	go func() {
		// Emit MOTD immediately.
		if m.outWriter != nil && m.motd != "" {
			_, _ = m.outWriter.Write([]byte(m.motd))
		}
		for {
			select {
			case input := <-m.stdinCh:
				if m.outWriter == nil {
					continue
				}
				if idx := bytes.Index(input, []byte("RC_READY_")); idx != -1 {
					rest := input[idx:]
					if end := bytes.IndexByte(rest, '\n'); end > 0 {
						marker := rest[:end]
						_, _ = m.outWriter.Write(append(append([]byte("\n"), marker...), '\n'))
					}
					continue
				}
				_, _ = m.outWriter.Write(append([]byte("$ "), input...))
			case <-m.closeCh:
				return
			}
		}
	}()
	return nil
}

// gatedFakeSession holds exit marker responses until gate is closed.
type gatedFakeSession struct {
	*fakeSession
	gate <-chan struct{}
}

func (g *gatedFakeSession) Shell() error {
	if g.shellErr != nil {
		return g.shellErr
	}
	go func() {
		for {
			select {
			case input := <-g.stdinCh:
				if g.outWriter == nil {
					continue
				}
				// Boot marker
				if idx := bytes.Index(input, []byte("RC_READY_")); idx != -1 {
					rest := input[idx:]
					if end := bytes.IndexByte(rest, '\n'); end > 0 {
						marker := rest[:end]
						_, _ = g.outWriter.Write(append(append([]byte("\n"), marker...), '\n'))
					}
					continue
				}
				// Exit marker: wait for gate before responding
				if _, after, ok := bytes.Cut(input, []byte("RC_EXIT_")); ok {
					if nonce, _, ok := bytes.Cut(after, []byte("_%d")); ok {
						select {
						case <-g.gate: // block until test releases
							_, _ = g.outWriter.Write([]byte("\nRC_EXIT_" + string(nonce) + "_0\n"))
						case <-g.closeCh:
							return
						}
					}
					continue
				}
				_, _ = g.outWriter.Write(append([]byte("$ "), input...))
			case <-g.closeCh:
				return
			}
		}
	}()
	return nil
}

func TestManager_Send_ExitCode(t *testing.T) {
	fs := newFakeSession()
	mgr := newManager(minCfg(), fakeSessions(fs), nil)
	defer mgr.Shutdown(context.Background())

	id, err := mgr.Open(context.Background(), "h", "")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	res, err := mgr.Send(context.Background(), id, SendInput{Input: "ls\n", TimeoutMs: 2000})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if res.ExitCode == nil {
		t.Fatal("ExitCode should not be nil")
	}
	if *res.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", *res.ExitCode)
	}
	if res.StillRunning {
		t.Error("StillRunning should be false")
	}
	mgr.Close(context.Background(), id)
}

func TestManager_Send_StillRunning_Continuation(t *testing.T) {
	gate := make(chan struct{})
	gated := &gatedFakeSession{fakeSession: newFakeSession(), gate: gate}
	mgr := newManager(minCfg(), fakeSessionsIface(gated), nil)
	defer mgr.Shutdown(context.Background())

	id, _ := mgr.Open(context.Background(), "h", "")

	// First send: short timeout, marker blocked.
	res1, err := mgr.Send(context.Background(), id, SendInput{Input: "sleep 5\n", TimeoutMs: 100})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !res1.StillRunning {
		t.Fatal("expected StillRunning=true on first send")
	}
	if res1.ExitCode != nil {
		t.Errorf("ExitCode should be nil when still running, got %d", *res1.ExitCode)
	}

	// Release gate so fake emits exit marker.
	close(gate)

	// Continuation: empty input waits for the same marker.
	res2, err := mgr.Send(context.Background(), id, SendInput{Input: "", TimeoutMs: 2000})
	if err != nil {
		t.Fatalf("continuation Send: %v", err)
	}
	if res2.StillRunning {
		t.Error("continuation: StillRunning should be false")
	}
	if res2.ExitCode == nil || *res2.ExitCode != 0 {
		t.Errorf("continuation ExitCode = %v, want &0", res2.ExitCode)
	}

	mgr.Close(context.Background(), id)
}

func TestManager_Send_InFlightRejection(t *testing.T) {
	fs := newFakeSession()
	mgr := newManager(minCfg(), fakeSessions(fs), nil)
	defer mgr.Shutdown(context.Background())

	id, _ := mgr.Open(context.Background(), "h", "")

	// Manually set inflight to simulate a stuck command.
	mgr.mu.RLock()
	s := mgr.sessions[id]
	mgr.mu.RUnlock()
	s.mu.Lock()
	s.inflight = &inflight{nonce: "FAKEFAKE"}
	s.mu.Unlock()

	_, err := mgr.Send(context.Background(), id, SendInput{Input: "cmd2\n", TimeoutMs: 100})
	if err == nil {
		t.Error("expected error when inflight is set")
	}

	// Clean up inflight for Close to work.
	s.mu.Lock()
	s.inflight = nil
	s.mu.Unlock()
	mgr.Close(context.Background(), id)
}

func TestManager_Send_TimeoutWarning(t *testing.T) {
	cfg := minCfg()
	cfg.Limits.MaxSendTimeoutMs = 500
	fs := newFakeSession()
	mgr := newManager(cfg, fakeSessions(fs), nil)
	defer mgr.Shutdown(context.Background())

	id, _ := mgr.Open(context.Background(), "h", "")
	res, err := mgr.Send(context.Background(), id, SendInput{Input: "x\n", TimeoutMs: 5000})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(res.Warnings) == 0 {
		t.Error("expected timeout clamp warning")
	} else if !strings.Contains(res.Warnings[0], "clamped") {
		t.Errorf("warning should mention 'clamped', got %q", res.Warnings[0])
	}
	mgr.Close(context.Background(), id)
}

// ---- RunOnce unit tests (no real pool) ----

func TestManager_RunOnce_NilPool(t *testing.T) {
	mgr := newManager(minCfg(), fakeSessions(), nil)
	defer mgr.Shutdown(context.Background())

	_, err := mgr.RunOnce(context.Background(), "h", RunOnceInput{Command: "ls"})
	if err == nil {
		t.Fatal("expected error when pool is nil")
	}
	if !strings.Contains(err.Error(), "no pool configured") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestManager_RunOnce_UnknownHost(t *testing.T) {
	mgr := newManager(minCfg(), fakeSessions(), nil)
	defer mgr.Shutdown(context.Background())

	_, err := mgr.RunOnce(context.Background(), "nope", RunOnceInput{Command: "ls"})
	if err == nil {
		t.Fatal("expected error for unknown host")
	}
}

func TestManager_Open_FailureReleasesReservation(t *testing.T) {
	cfg := minCfg()
	cfg.Limits.MaxSessionsTotal = 1

	calls := 0
	factory := func(_ context.Context, _ string) (sshSession, func(), error) {
		calls++
		if calls == 1 {
			return nil, nil, errors.New("transient dial error")
		}
		return newFakeSession(), func() {}, nil
	}

	m := newManager(cfg, factory, nil)
	defer m.Shutdown(context.Background())

	_, err := m.Open(context.Background(), "h", "")
	if err == nil {
		t.Fatal("expected first Open to fail (factory error)")
	}

	_, err = m.Open(context.Background(), "h", "")
	if err != nil {
		t.Fatalf("second Open should succeed after reservation was rolled back: %v", err)
	}
}

// ---- Send WaitIdleMs (peek mode) ----

func TestManager_Send_WaitIdleMs_Peek(t *testing.T) {
	// Peek mode returns whatever is buffered after idle_ms of silence.
	fs := newFakeSession()
	mgr := newManager(minCfg(), fakeSessions(fs), nil)
	defer mgr.Shutdown(context.Background())

	id, _ := mgr.Open(context.Background(), "h", "")

	// Peek with no activity → empty output.
	res, err := mgr.Send(context.Background(), id, SendInput{WaitIdleMs: 50})
	if err != nil {
		t.Fatalf("Send peek: %v", err)
	}
	if res.Output != "" {
		t.Errorf("peek output = %q, want empty", res.Output)
	}

	_, _ = mgr.Close(context.Background(), id)
}

func TestManager_Send_WaitIdleMs_MutuallyExclusive(t *testing.T) {
	fs := newFakeSession()
	mgr := newManager(minCfg(), fakeSessions(fs), nil)
	defer mgr.Shutdown(context.Background())

	id, _ := mgr.Open(context.Background(), "h", "")
	defer mgr.Close(context.Background(), id)

	_, err := mgr.Send(context.Background(), id, SendInput{Input: "ls\n", WaitIdleMs: 100})
	if err == nil {
		t.Error("expected error when both Input and WaitIdleMs are set")
	}
}

// ---- Send Raw mode ----

func TestManager_Send_Raw(t *testing.T) {
	fs := newFakeSession()
	mgr := newManager(minCfg(), fakeSessions(fs), nil)
	defer mgr.Shutdown(context.Background())

	id, _ := mgr.Open(context.Background(), "h", "")
	defer mgr.Close(context.Background(), id)

	// Raw mode: no marker injected, output returned as-is.
	res, err := mgr.Send(context.Background(), id, SendInput{Input: "ls\n", TimeoutMs: 500, Raw: true})
	if err != nil {
		t.Fatalf("Send raw: %v", err)
	}
	// ExitCode should be nil in raw mode.
	if res.ExitCode != nil {
		t.Errorf("ExitCode should be nil in raw mode, got %d", *res.ExitCode)
	}
	// Output should contain the echo (not cleaned).
	if !strings.Contains(res.Output, "ls\n") {
		t.Errorf("raw output should contain echoed input, got %q", res.Output)
	}
}

// ---- Send session closed during waitForMarker ----

func TestManager_Send_SessionClosedDuringWait(t *testing.T) {
	// Use a gated fake that blocks exit markers so Send stays inside waitForMarker.
	// We close the underlying SSH session directly (not via mgr.Close, which would
	// deadlock on sendMu) so that s.done fires and waitForMarker detects closure.
	gate := make(chan struct{})
	gated := &gatedFakeSession{fakeSession: newFakeSession(), gate: gate}
	mgr := newManager(minCfg(), fakeSessionsIface(gated), nil)
	defer mgr.Shutdown(context.Background())

	id, err := mgr.Open(context.Background(), "h", "")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	sendDone := make(chan SendResult, 1)
	go func() {
		res, _ := mgr.Send(context.Background(), id, SendInput{Input: "cmd\n", TimeoutMs: 5000})
		sendDone <- res
	}()

	// Give Send time to enter waitForMarker before we simulate SSH disconnect.
	time.Sleep(100 * time.Millisecond)

	// Mark session closed and close the underlying SSH session directly so s.done fires.
	mgr.mu.RLock()
	s := mgr.sessions[id]
	mgr.mu.RUnlock()
	s.mu.Lock()
	s.closed = true
	s.closedReason = "lost"
	s.mu.Unlock()
	// Closing the fake SSH session makes its Wait() return, closing s.done.
	gated.fakeSession.Close()
	close(gate)

	res := <-sendDone
	if res.ClosedReason == "" {
		t.Error("expected non-empty ClosedReason when session closed during Send")
	}
}

// ---- Send context cancellation ----

func TestManager_Send_ContextCancelled(t *testing.T) {
	gate := make(chan struct{})
	gated := &gatedFakeSession{fakeSession: newFakeSession(), gate: gate}
	cfg := minCfg()
	cfg.Limits.MaxSendTimeoutMs = 10000
	mgr := newManager(cfg, fakeSessionsIface(gated), nil)
	defer mgr.Shutdown(context.Background())

	id, _ := mgr.Open(context.Background(), "h", "")
	defer func() { close(gate); mgr.Close(context.Background(), id) }()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := mgr.Send(ctx, id, SendInput{Input: "cmd\n", TimeoutMs: 5000})
		errCh <- err
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	err := <-errCh
	if err == nil {
		t.Error("expected error from cancelled context")
	}
}

// ---- Open name validation via Open ----

func TestManager_Open_NameValidation(t *testing.T) {
	// validateName rejects the s_ prefix; Open should propagate that error.
	m := newManager(minCfg(), fakeSessions(newFakeSession()), nil)
	defer m.Shutdown(context.Background())

	_, err := m.Open(context.Background(), "h", "s_reserved")
	if err == nil {
		t.Fatal("expected error for name with s_ prefix")
	}
}

func TestManager_Open_NameCollision(t *testing.T) {
	f1 := newFakeSession()
	f2 := newFakeSession()
	m := newManager(minCfg(), fakeSessions(f1, f2), nil)
	defer m.Shutdown(context.Background())

	_, err := m.Open(context.Background(), "h", "myservice")
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}

	_, err = m.Open(context.Background(), "h", "myservice")
	if err == nil {
		t.Fatal("expected error for duplicate session name")
	}
}

// fakeSessionWithWaitErr wraps fakeSession and returns a specific error from
// Wait() so we can drive the session into closedReason="lost" in tests.
type fakeSessionWithWaitErr struct {
	*fakeSession
	waitErr error
}

func (f *fakeSessionWithWaitErr) Wait() error {
	<-f.fakeSession.closeCh
	return f.waitErr
}

func TestManager_Send_LostAdvisory(t *testing.T) {
	// When a session closes with reason "lost", SendResult.Warnings must
	// contain the reconnect advisory.
	fs := newFakeSession()
	bad := &fakeSessionWithWaitErr{fakeSession: fs, waitErr: errors.New("connection reset")}

	m := newManager(minCfg(), fakeSessionsIface(bad), nil)

	id, err := m.Open(context.Background(), "h", "")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Close the underlying fake session → Wait() returns an error → closedReason="lost".
	_ = fs.Close()

	// Wait for the manager's Wait goroutine to set closedReason and close s.done.
	m.mu.RLock()
	s := m.sessions[id]
	m.mu.RUnlock()
	select {
	case <-s.done:
	case <-time.After(time.Second):
		t.Fatal("session did not close within 1s")
	}

	res, err := m.Send(context.Background(), id, SendInput{Input: "echo hi"})
	if err != nil {
		t.Fatalf("Send on closed session: %v", err)
	}
	if res.ClosedReason != "lost" {
		t.Errorf("ClosedReason = %q, want %q", res.ClosedReason, "lost")
	}

	found := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "connection lost") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'connection lost' advisory in Warnings, got %v", res.Warnings)
	}
}

func TestManager_List_ClosedReason(t *testing.T) {
	// SessionInfo returned by List() must expose ClosedReason.
	fs := newFakeSession()
	bad := &fakeSessionWithWaitErr{fakeSession: fs, waitErr: errors.New("connection reset")}

	m := newManager(minCfg(), fakeSessionsIface(bad), nil)

	id, err := m.Open(context.Background(), "h", "")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	_ = fs.Close()

	m.mu.RLock()
	s := m.sessions[id]
	m.mu.RUnlock()
	select {
	case <-s.done:
	case <-time.After(time.Second):
		t.Fatal("session did not close within 1s")
	}

	infos := m.List()
	if len(infos) != 1 {
		t.Fatalf("expected 1 session in List, got %d", len(infos))
	}
	if infos[0].ClosedReason != "lost" {
		t.Errorf("SessionInfo.ClosedReason = %q, want %q", infos[0].ClosedReason, "lost")
	}
}

// ---- Detach unit tests (no real pool) ----

func TestManager_Detach_NilPool(t *testing.T) {
	mgr := newManager(minCfg(), fakeSessions(), nil)
	defer mgr.Shutdown(context.Background())

	_, err := mgr.Detach(context.Background(), "h", RunOnceInput{Command: "sleep 60"}, nil)
	if err == nil {
		t.Fatal("expected error when pool is nil")
	}
	if !strings.Contains(err.Error(), "no pool configured") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestManager_Detach_UnknownHost(t *testing.T) {
	// Use NewManager with a real pool pointing at an unreachable address;
	// the host-not-found check happens before the pool.Get call.
	cfg := &config.Config{
		Limits: config.Limits{
			MaxSessionsTotal: 32, MaxSessionsPerHost: 4,
			DefaultIdleTimeout: 15 * time.Minute, MaxSessionAge: 4 * time.Hour,
			OutputBufferBytes: 4096, DefaultSendTimeoutMs: 2000, MaxSendTimeoutMs: 30000,
			DialTimeout: 100 * time.Millisecond,
		},
		Hosts: map[string]config.Host{
			"h": {Address: "127.0.0.1:1", User: "u", KnownHosts: "system",
				Auth: config.Auth{Type: "password", PasswordEnv: "TEST_DETACH_PASS"}},
		},
	}
	t.Setenv("TEST_DETACH_PASS", "x")
	pool := hostpool.New(cfg, sshconn.ProdDialer{})
	defer pool.Close()
	mgr := NewManager(cfg, pool, nil)
	defer mgr.Shutdown(context.Background())

	_, err := mgr.Detach(context.Background(), "nope", RunOnceInput{Command: "ls"}, nil)
	if err == nil {
		t.Fatal("expected error for unknown host")
	}
	if !strings.Contains(err.Error(), "unknown host") {
		t.Errorf("unexpected error: %v", err)
	}
}
