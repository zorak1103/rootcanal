package session

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"gitlab.com/zorak1103/rootcanal/internal/config"
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
				if f.outWriter != nil {
					_, _ = f.outWriter.Write(append([]byte("$ "), input...))
				}
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
	return &config.Config{
		Limits: config.Limits{
			MaxSessionsTotal:    32,
			MaxSessionsPerHost:  4,
			DefaultIdleTimeout:  15 * time.Minute,
			MaxSessionAge:       4 * time.Hour,
			OutputBufferBytes:   4096,
			DefaultSendTimeoutMs: 2000,
			MaxSendTimeoutMs:    30000,
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
	_, err := m.Open(context.Background(), "unknown")
	if err == nil {
		t.Fatal("expected error for unknown host")
	}
}

func TestManager_Open_DialError(t *testing.T) {
	m := newManager(minCfg(), errFactory(errors.New("connection refused")), nil)
	defer m.Shutdown(context.Background())
	_, err := m.Open(context.Background(), "h")
	if err == nil {
		t.Fatal("expected dial error")
	}
}

func TestManager_Open_PTYError(t *testing.T) {
	fs := newFakeSession()
	fs.ptyErr = errors.New("pty not supported")
	m := newManager(minCfg(), fakeSessions(fs), nil)
	defer m.Shutdown(context.Background())
	_, err := m.Open(context.Background(), "h")
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

	_, err := m.Open(context.Background(), "h")
	if err != nil {
		t.Fatalf("first Open unexpected error: %v", err)
	}
	_, err = m.Open(context.Background(), "h")
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

	_, err := m.Open(context.Background(), "h")
	if err != nil {
		t.Fatalf("first Open unexpected error: %v", err)
	}
	_, err = m.Open(context.Background(), "h")
	if err == nil {
		t.Fatal("expected error when per-host limit exceeded")
	}
}

func TestManager_SendClose_RoundTrip(t *testing.T) {
	fs := newFakeSession()
	m := newManager(minCfg(), fakeSessions(fs), nil)
	defer m.Shutdown(context.Background())

	id, err := m.Open(context.Background(), "h")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	output, _, _, err := m.Send(context.Background(), id, []byte("ls\n"), 500*time.Millisecond)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(output) == 0 {
		t.Error("expected some output, got none")
	}

	if err := m.Close(context.Background(), id); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(m.List()) != 0 {
		t.Errorf("expected 0 sessions after close, got %d", len(m.List()))
	}
}

func TestManager_Send_UnknownID(t *testing.T) {
	m := newManager(minCfg(), fakeSessions(), nil)
	defer m.Shutdown(context.Background())
	_, _, _, err := m.Send(context.Background(), "s_UNKNOWN", nil, 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected error for unknown session ID")
	}
}

func TestManager_Close_UnknownID(t *testing.T) {
	m := newManager(minCfg(), fakeSessions(), nil)
	defer m.Shutdown(context.Background())
	if err := m.Close(context.Background(), "s_NONE"); err == nil {
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
	id, _ := m.Open(context.Background(), "h")
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
	m.Open(context.Background(), "h")
	m.Open(context.Background(), "h")
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

		id, err := m.Open(context.Background(), "h")
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
