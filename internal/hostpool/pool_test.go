package hostpool

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zorak1103/rootcanal/internal/config"
	"github.com/zorak1103/rootcanal/internal/sshconn"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// funcDialer is an injectable Dialer backed by a closure.
type funcDialer struct {
	fn func(context.Context, config.Host, config.Limits) (*ssh.Client, error)
}

func (d *funcDialer) Dial(ctx context.Context, h config.Host, l config.Limits) (*ssh.Client, error) {
	return d.fn(ctx, h, l)
}

// fakeDialer is an injectable Dialer for pool tests.
type fakeDialer struct {
	client *ssh.Client
	err    error
}

func (f *fakeDialer) Dial(_ context.Context, _ config.Host, _ config.Limits) (*ssh.Client, error) {
	return f.client, f.err
}

// startSSHServer starts a minimal in-process SSH server (NoClientAuth).
// Returns the listener address and a known_hosts file path.
func startSSHServer(t *testing.T) (addr, knownHostsPath string) {
	t.Helper()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serverSigner, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}

	srvCfg := &ssh.ServerConfig{NoClientAuth: true}
	srvCfg.AddHostKey(serverSigner)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr = ln.Addr().String()
	t.Cleanup(func() { ln.Close() })

	khPath := filepath.Join(t.TempDir(), "known_hosts")
	line := knownhosts.Line([]string{knownhosts.Normalize(addr)}, serverSigner.PublicKey())
	if err := os.WriteFile(khPath, []byte(line+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	knownHostsPath = khPath

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				_, chans, reqs, err := ssh.NewServerConn(c, srvCfg)
				if err != nil {
					return
				}
				go ssh.DiscardRequests(reqs)
				for ch := range chans {
					ch.Reject(ssh.UnknownChannelType, "")
				}
			}(conn)
		}
	}()

	return addr, knownHostsPath
}

func minCfg(hosts map[string]config.Host) *config.Config {
	return &config.Config{
		Limits: config.Limits{MaxSessionsPerHost: 4, DialTimeout: 5 * time.Second},
		Hosts:  hosts,
	}
}

// ---- error paths (no real SSH) ----

func TestPool_EffectiveKeepalive(t *testing.T) {
	customInterval := 5 * time.Second
	customMaxFails := 2
	cfg := &config.Config{
		Limits: config.Limits{
			DefaultKeepaliveInterval:    30 * time.Second,
			DefaultKeepaliveMaxFailures: 3,
		},
		Hosts: map[string]config.Host{
			"default": {},
			"custom":  {KeepaliveInterval: &customInterval, KeepaliveMaxFailures: &customMaxFails},
		},
	}
	p := New(cfg, &fakeDialer{})

	if interval, maxFails := p.effectiveKeepalive("default"); interval != 30*time.Second || maxFails != 3 {
		t.Errorf("default host: got (%v, %d), want (%v, %d)", interval, maxFails, 30*time.Second, 3)
	}
	if interval, maxFails := p.effectiveKeepalive("custom"); interval != customInterval || maxFails != customMaxFails {
		t.Errorf("custom host: got (%v, %d), want (%v, %d)", interval, maxFails, customInterval, customMaxFails)
	}
}

func TestPool_UnknownHost(t *testing.T) {
	p := New(minCfg(nil), &fakeDialer{})
	_, _, err := p.Get(context.Background(), "no-such-host")
	if err == nil {
		t.Fatal("expected error for unknown host")
	}
}

func TestPool_DialError(t *testing.T) {
	cfg := minCfg(map[string]config.Host{
		"h": {Address: "h:22", User: "u", KnownHosts: "system", Auth: config.Auth{Type: "agent"}},
	})
	p := New(cfg, &fakeDialer{err: errors.New("connection refused")})
	_, _, err := p.Get(context.Background(), "h")
	if err == nil {
		t.Fatal("expected dial error")
	}
}

func TestPool_CapExceeded(t *testing.T) {
	cfg := minCfg(map[string]config.Host{
		"h": {Address: "h:22", User: "u", KnownHosts: "system", Auth: config.Auth{Type: "agent"}},
	})
	p := New(cfg, &fakeDialer{})
	// Inject a pre-filled entry at max refs.
	p.entries["h"] = &entry{client: nil, refs: 4}

	_, _, err := p.Get(context.Background(), "h")
	if err == nil {
		t.Fatal("expected cap exceeded error")
	}
}

func TestPool_CloseEmpty(t *testing.T) {
	p := New(minCfg(nil), &fakeDialer{})
	p.Close() // must not panic
}

// ---- integration (real SSH) ----

func TestPool_GetAndRelease(t *testing.T) {
	addr, khPath := startSSHServer(t)
	t.Setenv("TEST_POOL_PASS", "irrelevant")

	cfg := minCfg(map[string]config.Host{
		"srv": {
			Address:    addr,
			User:       "u",
			KnownHosts: khPath,
			Auth:       config.Auth{Type: "password", PasswordEnv: "TEST_POOL_PASS"},
		},
	})
	p := New(cfg, sshconn.ProdDialer{})
	t.Cleanup(p.Close)

	client, release, err := p.Get(context.Background(), "srv")
	if err != nil {
		t.Fatalf("Get() unexpected error: %v", err)
	}
	if client == nil {
		t.Fatal("Get() returned nil client")
	}

	// A second Get should reuse the connection (refs = 2).
	_, release2, err := p.Get(context.Background(), "srv")
	if err != nil {
		t.Fatalf("second Get() unexpected error: %v", err)
	}
	if len(p.entries) != 1 {
		t.Errorf("expected 1 pool entry, got %d", len(p.entries))
	}
	if p.entries["srv"].refs != 2 {
		t.Errorf("expected refs=2, got %d", p.entries["srv"].refs)
	}

	release()
	release2()

	// After both releases, refs should be 0 and idle timer started.
	p.mu.Lock()
	e := p.entries["srv"]
	p.mu.Unlock()
	if e.refs != 0 {
		t.Errorf("expected refs=0 after release, got %d", e.refs)
	}
	if e.idleTimer == nil {
		t.Error("expected idle timer to be set after release")
	}
}

func TestPool_PartialRelease_NoTimer(t *testing.T) {
	// Two refs to same host; releasing one should NOT start the idle timer.
	addr, khPath := startSSHServer(t)
	t.Setenv("TEST_PARTIAL_PASS", "irrelevant")

	cfg := minCfg(map[string]config.Host{
		"srv": {Address: addr, User: "u", KnownHosts: khPath,
			Auth: config.Auth{Type: "password", PasswordEnv: "TEST_PARTIAL_PASS"}},
	})
	p := New(cfg, sshconn.ProdDialer{})
	t.Cleanup(p.Close)

	_, release1, err := p.Get(context.Background(), "srv")
	if err != nil {
		t.Fatalf("Get 1: %v", err)
	}
	_, release2, err := p.Get(context.Background(), "srv")
	if err != nil {
		t.Fatalf("Get 2: %v", err)
	}

	release1() // refs: 2 → 1; timer must NOT start

	p.mu.Lock()
	e := p.entries["srv"]
	p.mu.Unlock()

	if e.refs != 1 {
		t.Errorf("expected refs=1, got %d", e.refs)
	}
	if e.idleTimer != nil {
		t.Error("idle timer must not start while refs > 0")
	}
	release2()
}

func TestPool_IdleTimerFires(t *testing.T) {
	old := idleTimeout
	idleTimeout = 20 * time.Millisecond
	t.Cleanup(func() { idleTimeout = old })

	addr, khPath := startSSHServer(t)
	t.Setenv("TEST_IDLE_PASS", "irrelevant")

	cfg := minCfg(map[string]config.Host{
		"srv": {Address: addr, User: "u", KnownHosts: khPath,
			Auth: config.Auth{Type: "password", PasswordEnv: "TEST_IDLE_PASS"}},
	})
	p := New(cfg, sshconn.ProdDialer{})
	t.Cleanup(p.Close)

	_, release, err := p.Get(context.Background(), "srv")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	release() // refs → 0, idle timer starts

	time.Sleep(60 * time.Millisecond) // wait for timer to fire

	p.mu.Lock()
	_, exists := p.entries["srv"]
	p.mu.Unlock()
	if exists {
		t.Error("expected entry to be evicted after idle timeout")
	}
}

func TestPool_CloseWithActiveEntries(t *testing.T) {
	addr, khPath := startSSHServer(t)
	t.Setenv("TEST_POOL_CLOSE_PASS", "irrelevant")

	cfg := minCfg(map[string]config.Host{
		"srv": {
			Address:    addr,
			User:       "u",
			KnownHosts: khPath,
			Auth:       config.Auth{Type: "password", PasswordEnv: "TEST_POOL_CLOSE_PASS"},
		},
	})
	p := New(cfg, sshconn.ProdDialer{})

	_, release, err := p.Get(context.Background(), "srv")
	if err != nil {
		t.Fatalf("Get() unexpected error: %v", err)
	}
	defer release()

	p.Close() // must not panic even with active refs
	if len(p.entries) != 0 {
		t.Errorf("expected empty pool after Close, got %d entries", len(p.entries))
	}
}

// ---- lost-race paths ----

// TestPool_LostRace_PrefersExistingEntry exercises the code path where a
// concurrent dial completes after another goroutine already inserted an entry.
// The dialer injects the "existing" entry into the pool while it runs, then
// returns a "duplicate" client that the lost-race path should discard.
func TestPool_LostRace_PrefersExistingEntry(t *testing.T) {
	addr, khPath := startSSHServer(t)
	t.Setenv("TEST_LOSTRACE_PASS", "irrelevant")

	host := config.Host{
		Address: addr, User: "u", KnownHosts: khPath,
		Auth: config.Auth{Type: "password", PasswordEnv: "TEST_LOSTRACE_PASS"},
	}
	cfg := minCfg(map[string]config.Host{"srv": host})

	var p *Pool
	var existingClient *ssh.Client

	d := &funcDialer{fn: func(ctx context.Context, h config.Host, l config.Limits) (*ssh.Client, error) {
		dup, err := sshconn.ProdDialer{}.Dial(ctx, h, l)
		if err != nil {
			return nil, err
		}
		existing, err := sshconn.ProdDialer{}.Dial(ctx, h, l)
		if err != nil {
			_ = dup.Close()
			return nil, err
		}
		existingClient = existing
		// Inject the existing entry with an idle timer to also cover the
		// idle-timer-stop branch in the lost-race path.
		timer := time.AfterFunc(1*time.Hour, func() { /* sentinel */ })
		p.mu.Lock()
		p.entries["srv"] = &entry{client: existing, refs: 1, idleTimer: timer}
		p.mu.Unlock()
		return dup, nil // will be closed by lost-race path
	}}

	p = New(cfg, d)
	t.Cleanup(p.Close)

	client, release, err := p.Get(context.Background(), "srv")
	if err != nil {
		t.Fatalf("Get() unexpected error: %v", err)
	}
	defer release()

	if client != existingClient {
		t.Error("lost-race path should prefer the existing entry, not the duplicate")
	}
	p.mu.Lock()
	e := p.entries["srv"]
	p.mu.Unlock()
	if e.refs != 2 {
		t.Errorf("expected refs=2 after lost-race bump, got %d", e.refs)
	}
	if e.idleTimer != nil {
		t.Error("idle timer should have been cleared in lost-race path")
	}
}

// TestPool_LostRace_CapExceeded covers the per-host cap check in the lost-race path.
// ---- sanitizeConnErr unit tests ----

func TestSanitizeConnErr_NonNetError(t *testing.T) {
	orig := errors.New("ssh: handshake failed: auth error")
	got := sanitizeConnErr(orig)
	if got != orig { //nolint:errorlint // identity check: verifies pass-through, not equivalence
		t.Errorf("expected non-net error to pass through unchanged")
	}
}

func TestSanitizeConnErr_Timeout(t *testing.T) {
	inner := &net.OpError{Op: "dial", Net: "tcp", Err: &timeoutErr{}}
	got := sanitizeConnErr(inner)
	if got.Error() != "connection timed out" {
		t.Errorf("got %q, want %q", got.Error(), "connection timed out")
	}
}

func TestSanitizeConnErr_NilInnerErr(t *testing.T) {
	// An OpError with no wrapped Err (and thus not a timeout) falls through
	// to the generic "network error" message.
	inner := &net.OpError{Op: "dial", Net: "tcp"}
	got := sanitizeConnErr(inner)
	if got.Error() != "network error" {
		t.Errorf("got %q, want %q", got.Error(), "network error")
	}
}

func TestSanitizeConnErr_ConnectionRefused(t *testing.T) {
	// Dial a port guaranteed to refuse to get a real net.OpError.
	conn, err := net.DialTimeout("tcp", "127.0.0.1:1", 200*time.Millisecond)
	if conn != nil {
		conn.Close()
	}
	if err == nil {
		t.Skip("127.0.0.1:1 unexpectedly accepted a connection")
	}
	got := sanitizeConnErr(err)
	if got == err { //nolint:errorlint // identity check: verifies the error was replaced, not equivalence
		t.Fatal("expected net.OpError to be replaced")
	}
	msg := got.Error()
	if strings.Contains(msg, "127.0.0.1") || strings.Contains(msg, ":1") {
		t.Errorf("sanitized error still contains address: %q", msg)
	}
}

// TestPool_DialError_NoAddressLeak verifies end-to-end that a TCP connection
// failure returned by pool.Get does not expose the remote host:port.
func TestPool_DialError_NoAddressLeak(t *testing.T) {
	const addr = "127.0.0.1:1" // port 1 — always refused
	cfg := minCfg(map[string]config.Host{
		"h": {Address: addr, User: "u", KnownHosts: "system", Auth: config.Auth{Type: "agent"}},
	})
	cfg.Limits.DialTimeout = 200 * time.Millisecond

	p := New(cfg, sshconn.ProdDialer{})
	defer p.Close()

	_, _, err := p.Get(context.Background(), "h")
	if err == nil {
		t.Fatal("expected dial error for unreachable host")
	}
	msg := err.Error()
	if strings.Contains(msg, "127.0.0.1") || strings.Contains(msg, ":1") {
		t.Errorf("pool error leaks host address: %q", msg)
	}
}

// timeoutErr is a net.Error that reports Timeout() == true.
type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestPool_LostRace_CapExceeded(t *testing.T) {
	addr, khPath := startSSHServer(t)
	t.Setenv("TEST_LOSTRACE_CAP_PASS", "irrelevant")

	host := config.Host{
		Address: addr, User: "u", KnownHosts: khPath,
		Auth: config.Auth{Type: "password", PasswordEnv: "TEST_LOSTRACE_CAP_PASS"},
	}
	cfg := minCfg(map[string]config.Host{"srv": host})

	var p *Pool
	d := &funcDialer{fn: func(ctx context.Context, h config.Host, l config.Limits) (*ssh.Client, error) {
		dup, err := sshconn.ProdDialer{}.Dial(ctx, h, l)
		if err != nil {
			return nil, err
		}
		// Inject entry already at the per-host limit.
		p.mu.Lock()
		p.entries["srv"] = &entry{client: nil, refs: cfg.Limits.MaxSessionsPerHost}
		p.mu.Unlock()
		return dup, nil
	}}

	p = New(cfg, d)
	t.Cleanup(p.Close)

	_, _, err := p.Get(context.Background(), "srv")
	if err == nil {
		t.Fatal("expected cap-exceeded error on lost-race path")
	}
}

func TestPool_KeepaliveEviction_ReDialsAfterDeath(t *testing.T) {
	// After keepalive declares a connection dead, the next Get must re-dial.
	addr, khPath := startSSHServer(t)
	t.Setenv("TEST_KA_EVICT_PASS", "irrelevant")

	var dialCount atomic.Int32
	d := &funcDialer{fn: func(ctx context.Context, h config.Host, l config.Limits) (*ssh.Client, error) {
		dialCount.Add(1)
		return sshconn.ProdDialer{}.Dial(ctx, h, l)
	}}

	cfg := minCfg(map[string]config.Host{
		"srv": {Address: addr, User: "u", KnownHosts: khPath,
			Auth: config.Auth{Type: "password", PasswordEnv: "TEST_KA_EVICT_PASS"}},
	})
	p := New(cfg, d)
	t.Cleanup(p.Close)

	// First Get — dials once.
	_, release1, err := p.Get(context.Background(), "srv")
	if err != nil {
		t.Fatalf("Get 1: %v", err)
	}
	if dialCount.Load() != 1 {
		t.Fatalf("expected 1 dial after first Get, got %d", dialCount.Load())
	}

	// Simulate keepalive declaring the connection dead.
	p.mu.Lock()
	e := p.entries["srv"]
	p.mu.Unlock()
	e.onDead()

	// Entry must now be gone.
	p.mu.Lock()
	_, stillThere := p.entries["srv"]
	p.mu.Unlock()
	if stillThere {
		t.Error("pool entry should be evicted after onDead()")
	}

	// Second Get must re-dial (dial count rises to 2).
	_, release2, err := p.Get(context.Background(), "srv")
	if err != nil {
		t.Fatalf("Get 2: %v", err)
	}
	if dialCount.Load() != 2 {
		t.Fatalf("expected 2 dials after eviction+Get, got %d", dialCount.Load())
	}

	release1()
	release2()
}

func TestPool_StaleRelease_NoOp(t *testing.T) {
	// A release() from a session on an evicted entry must not corrupt the
	// replacement entry's refcount.
	addr, khPath := startSSHServer(t)
	t.Setenv("TEST_STALE_REL_PASS", "irrelevant")

	cfg := minCfg(map[string]config.Host{
		"srv": {Address: addr, User: "u", KnownHosts: khPath,
			Auth: config.Auth{Type: "password", PasswordEnv: "TEST_STALE_REL_PASS"}},
	})
	p := New(cfg, sshconn.ProdDialer{})
	t.Cleanup(p.Close)

	// Get a client and hold its release func.
	_, staleRelease, err := p.Get(context.Background(), "srv")
	if err != nil {
		t.Fatalf("Get 1: %v", err)
	}

	// Evict via onDead.
	p.mu.Lock()
	e := p.entries["srv"]
	p.mu.Unlock()
	e.onDead()

	// Get a replacement client (refs = 1).
	_, release2, err := p.Get(context.Background(), "srv")
	if err != nil {
		t.Fatalf("Get 2 after eviction: %v", err)
	}
	defer release2()

	// Stale release must be a no-op — must not decrement replacement's refs.
	staleRelease()

	p.mu.Lock()
	cur, ok := p.entries["srv"]
	p.mu.Unlock()
	if !ok {
		t.Fatal("replacement entry should still exist after stale release")
	}
	if cur.refs != 1 {
		t.Errorf("expected replacement refs=1 after stale release, got %d", cur.refs)
	}
}
