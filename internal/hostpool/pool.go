package hostpool

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"gitlab.com/zorak1103/rootcanal/internal/config"
	"gitlab.com/zorak1103/rootcanal/internal/sshconn"
	"golang.org/x/crypto/ssh"
	"golang.org/x/sync/singleflight"
)

var idleTimeout = 30 * time.Second

type entry struct {
	client        *ssh.Client
	refs          int
	idleTimer     *time.Timer
	stopKeepalive func()
	// onDead is the eviction closure passed to StartKeepalive. Stored on the
	// entry so tests can fire it directly to simulate keepalive failure.
	onDead func()
}

// Pool holds a ref-counted *ssh.Client per host, creating and closing them on demand.
type Pool struct {
	cfg     *config.Config
	dialer  sshconn.Dialer
	mu      sync.Mutex
	entries map[string]*entry
	sf      singleflight.Group
}

// New creates a Pool using the given config and dialer.
func New(cfg *config.Config, d sshconn.Dialer) *Pool {
	return &Pool{
		cfg:     cfg,
		dialer:  d,
		entries: make(map[string]*entry),
	}
}

func (p *Pool) effectiveKeepalive(hostName string) (time.Duration, int) {
	h := p.cfg.Hosts[hostName]
	interval := p.cfg.Limits.DefaultKeepaliveInterval
	maxFails := p.cfg.Limits.DefaultKeepaliveMaxFailures
	if h.KeepaliveInterval != nil {
		interval = *h.KeepaliveInterval
	}
	if h.KeepaliveMaxFailures != nil {
		maxFails = *h.KeepaliveMaxFailures
	}
	return interval, maxFails
}

// Get returns a shared *ssh.Client for hostName and a release func the caller
// must invoke when done. The caller must not call Close on the returned client.
func (p *Pool) Get(ctx context.Context, hostName string) (*ssh.Client, func(), error) {
	h, ok := p.cfg.Hosts[hostName]
	if !ok {
		return nil, nil, config.UnknownHostError(hostName)
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
		return client, p.releaseFunc(hostName, e), nil
	}
	p.mu.Unlock()

	// Coalesce concurrent dials to the same host: exactly one SSH auth attempt
	// reaches the server regardless of caller concurrency. The entry is stored
	// inside the singleflight function so waiters receive a client that is
	// already in the map — they only bump refs, never close the shared handle.
	_, err, _ := p.sf.Do(hostName, func() (any, error) {
		client, err := p.dialer.Dial(ctx, h, p.cfg.Limits)
		if err != nil {
			return nil, err
		}
		p.mu.Lock()
		// Defensive: if a racing singleflight call already stored an entry
		// (possible after a previous Do completed for the same key), prefer it.
		if existing, ok := p.entries[hostName]; ok {
			p.mu.Unlock()
			_ = client.Close()
			return existing.client, nil
		}
		interval, maxFails := p.effectiveKeepalive(hostName)
		e := &entry{client: client, refs: 0}
		// Build the eviction closure before starting keepalive so that e is
		// fully initialized before any goroutine can reference it.
		evict := func() {
			p.mu.Lock()
			if cur, ok := p.entries[hostName]; ok && cur == e {
				if cur.idleTimer != nil {
					cur.idleTimer.Stop()
				}
				delete(p.entries, hostName)
			}
			p.mu.Unlock()
		}
		e.onDead = evict
		e.stopKeepalive = sshconn.StartKeepalive(client, interval, maxFails, nil, evict)
		p.entries[hostName] = e
		p.mu.Unlock()
		return client, nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("connecting to %q: %w", hostName, sanitizeConnErr(err))
	}

	p.mu.Lock()
	e, ok := p.entries[hostName]
	if !ok {
		p.mu.Unlock()
		return nil, nil, fmt.Errorf("host %q: entry vanished after dial", hostName)
	}
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

	return client, p.releaseFunc(hostName, e), nil
}

// releaseFunc returns a func that decrements the refcount for the specific pool
// entry e. If e has been evicted and replaced (e.g. by a keepalive-triggered
// reconnect), the release is a safe no-op — it never touches the replacement.
func (p *Pool) releaseFunc(hostName string, e *entry) func() {
	return func() {
		p.mu.Lock()
		defer p.mu.Unlock()

		cur, ok := p.entries[hostName]
		if !ok || cur != e {
			// Entry was evicted or replaced; this release belongs to the old
			// connection and must not touch the current pool state.
			return
		}
		e.refs--
		if e.refs <= 0 && e.idleTimer == nil {
			e.idleTimer = time.AfterFunc(idleTimeout, func() {
				var toClose *ssh.Client
				p.mu.Lock()
				// Identity check: only evict if the entry hasn't been replaced
				// by a concurrent Get between now and when the timer fired.
				if cur2, ok2 := p.entries[hostName]; ok2 && cur2 == e && cur2.refs <= 0 {
					toClose = cur2.client
					if cur2.stopKeepalive != nil {
						cur2.stopKeepalive()
					}
					delete(p.entries, hostName)
				}
				p.mu.Unlock()
				if toClose != nil {
					_ = toClose.Close()
				}
			})
		}
	}
}

// sanitizeConnErr strips the remote host:port from TCP-level errors so that
// network addresses are never surfaced to MCP clients. Go's net.OpError
// embeds the address in its Error() string; we replace it with just the
// underlying OS reason (e.g. "connection refused", "i/o timeout").
// SSH-level errors (auth, handshake, known-hosts) do not include network
// addresses in their messages and are returned unchanged.
func sanitizeConnErr(err error) error {
	var netErr *net.OpError
	if !errors.As(err, &netErr) {
		return err
	}
	if netErr.Timeout() {
		return errors.New("connection timed out")
	}
	if netErr.Err != nil {
		return fmt.Errorf("network error: %v", netErr.Err)
	}
	return errors.New("network error")
}

// Close immediately closes all cached clients and stops idle timers.
func (p *Pool) Close() {
	p.mu.Lock()
	clients := make([]*ssh.Client, 0, len(p.entries))
	for name, e := range p.entries {
		if e.idleTimer != nil {
			e.idleTimer.Stop()
		}
		if e.stopKeepalive != nil {
			e.stopKeepalive()
		}
		if e.client != nil {
			clients = append(clients, e.client)
		}
		delete(p.entries, name)
	}
	p.mu.Unlock()

	for _, c := range clients {
		_ = c.Close()
	}
}
