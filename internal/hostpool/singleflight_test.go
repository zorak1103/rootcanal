package hostpool

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gitlab.com/zorak1103/rootcanal/internal/config"
	"gitlab.com/zorak1103/rootcanal/internal/sshconn"
	"golang.org/x/crypto/ssh"
)

// TestPool_SingleflightCoalescesDialsPerHost verifies that N concurrent Get()
// calls for the same uncached host trigger exactly one SSH authentication
// attempt and all callers receive the same *ssh.Client.
func TestPool_SingleflightCoalescesDialsPerHost(t *testing.T) {
	const N = 5

	addr, khPath := startSSHServer(t)
	t.Setenv("TEST_SF_PASS", "irrelevant")

	cfg := minCfg(map[string]config.Host{
		"srv": {
			Address:    addr,
			User:       "u",
			KnownHosts: khPath,
			Auth:       config.Auth{Type: "password", PasswordEnv: "TEST_SF_PASS"},
		},
	})
	// Raise per-host limit so all N callers can get a slot.
	cfg.Limits.MaxSessionsPerHost = N + 1

	var dialCount int32

	// started is closed when the dial function is first entered;
	// resume is closed to unblock it so all goroutines have time to queue.
	started := make(chan struct{})
	resume := make(chan struct{})
	var startedOnce sync.Once

	d := &funcDialer{fn: func(ctx context.Context, h config.Host, l config.Limits) (*ssh.Client, error) {
		atomic.AddInt32(&dialCount, 1)
		startedOnce.Do(func() { close(started) })
		<-resume
		return sshconn.ProdDialer{}.Dial(ctx, h, l)
	}}

	p := New(cfg, d)
	t.Cleanup(p.Close)

	type result struct {
		client  *ssh.Client
		release func()
	}
	results := make(chan result, N)

	for i := 0; i < N; i++ {
		go func() {
			c, rel, err := p.Get(context.Background(), "srv")
			if err != nil {
				results <- result{}
				return
			}
			results <- result{client: c, release: rel}
		}()
	}

	// Wait for the dial function to start, then give the other goroutines
	// a moment to queue up inside singleflight.Do before releasing.
	<-started
	time.Sleep(20 * time.Millisecond)
	close(resume)

	var first *ssh.Client
	var releases []func()
	for i := 0; i < N; i++ {
		r := <-results
		if r.client == nil {
			t.Error("Get() returned nil client")
			continue
		}
		if first == nil {
			first = r.client
		} else if r.client != first {
			t.Error("expected all concurrent callers to receive the same *ssh.Client")
		}
		if r.release != nil {
			releases = append(releases, r.release)
		}
	}
	for _, rel := range releases {
		rel()
	}

	if got := atomic.LoadInt32(&dialCount); got != 1 {
		t.Errorf("expected exactly 1 dial call; got %d — singleflight not coalescing", got)
	}
}

// TestPool_SingleflightDialError verifies that when the dial fails, all
// concurrent waiters receive the error (not a nil client or a hang).
func TestPool_SingleflightDialError(t *testing.T) {
	const N = 4

	cfg := minCfg(map[string]config.Host{
		"h": {Address: "h:22", User: "u", KnownHosts: "system", Auth: config.Auth{Type: "agent"}},
	})
	cfg.Limits.MaxSessionsPerHost = N + 1

	resume := make(chan struct{})
	started := make(chan struct{})
	var startedOnce sync.Once

	d := &funcDialer{fn: func(_ context.Context, _ config.Host, _ config.Limits) (*ssh.Client, error) {
		startedOnce.Do(func() { close(started) })
		<-resume
		return nil, errBoom
	}}

	p := New(cfg, d)

	errs := make(chan error, N)
	for i := 0; i < N; i++ {
		go func() {
			_, _, err := p.Get(context.Background(), "h")
			errs <- err
		}()
	}

	<-started
	time.Sleep(20 * time.Millisecond)
	close(resume)

	for i := 0; i < N; i++ {
		err := <-errs
		if err == nil {
			t.Error("expected error when dial fails; got nil")
		}
	}
}

var errBoom = singleflightTestErr("boom")

type singleflightTestErr string

func (e singleflightTestErr) Error() string { return string(e) }
