package hostpool

import (
	"context"
	"fmt"
	"sync"
	"time"

	"gitlab.com/zorak1103/rootcanal/internal/config"
	"gitlab.com/zorak1103/rootcanal/internal/sshconn"
	"golang.org/x/crypto/ssh"
)

var idleTimeout = 30 * time.Second

type entry struct {
	client    *ssh.Client
	refs      int
	idleTimer *time.Timer
}

// Pool holds a ref-counted *ssh.Client per host, creating and closing them on demand.
type Pool struct {
	cfg     *config.Config
	dialer  sshconn.Dialer
	mu      sync.Mutex
	entries map[string]*entry
}

// New creates a Pool using the given config and dialer.
func New(cfg *config.Config, d sshconn.Dialer) *Pool {
	return &Pool{
		cfg:     cfg,
		dialer:  d,
		entries: make(map[string]*entry),
	}
}

// Get returns a shared *ssh.Client for hostName and a release func the caller
// must invoke when done. The caller must not call Close on the returned client.
func (p *Pool) Get(ctx context.Context, hostName string) (*ssh.Client, func(), error) {
	h, ok := p.cfg.Hosts[hostName]
	if !ok {
		return nil, nil, fmt.Errorf("unknown host %q", hostName)
	}

	p.mu.Lock()
	if e, ok := p.entries[hostName]; ok {
		if p.cfg.Limits.MaxSessionsPerHost > 0 && e.refs >= p.cfg.Limits.MaxSessionsPerHost {
			p.mu.Unlock()
			return nil, nil, fmt.Errorf("host %q: per-host session limit of %d reached", hostName, p.cfg.Limits.MaxSessionsPerHost)
		}
		if e.idleTimer != nil {
			e.idleTimer.Stop()
			e.idleTimer = nil
		}
		e.refs++
		client := e.client
		p.mu.Unlock()
		return client, p.releaseFunc(hostName), nil
	}
	p.mu.Unlock()

	// Dial outside the lock so other hosts are not blocked.
	// TODO(M3): use singleflight to prevent duplicate dials to the same host.
	client, err := p.dialer.Dial(ctx, h, p.cfg.Limits)
	if err != nil {
		return nil, nil, fmt.Errorf("dialing %q: %w", hostName, err)
	}

	p.mu.Lock()
	// Another goroutine may have dialed concurrently; prefer the existing entry.
	if existing, ok := p.entries[hostName]; ok {
		p.mu.Unlock()
		client.Close()
		p.mu.Lock()
		existing.refs++
		c := existing.client
		p.mu.Unlock()
		return c, p.releaseFunc(hostName), nil
	}
	p.entries[hostName] = &entry{client: client, refs: 1}
	p.mu.Unlock()

	return client, p.releaseFunc(hostName), nil
}

func (p *Pool) releaseFunc(hostName string) func() {
	return func() {
		p.mu.Lock()
		defer p.mu.Unlock()

		e, ok := p.entries[hostName]
		if !ok {
			return
		}
		e.refs--
		if e.refs <= 0 && e.idleTimer == nil {
			e.idleTimer = time.AfterFunc(idleTimeout, func() {
				p.mu.Lock()
				defer p.mu.Unlock()
				if e, ok := p.entries[hostName]; ok && e.refs <= 0 {
					if e.client != nil {
						e.client.Close()
					}
					delete(p.entries, hostName)
				}
			})
		}
	}
}

// Close immediately closes all cached clients and stops idle timers.
func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for name, e := range p.entries {
		if e.idleTimer != nil {
			e.idleTimer.Stop()
		}
		if e.client != nil {
			e.client.Close()
		}
		delete(p.entries, name)
	}
}
