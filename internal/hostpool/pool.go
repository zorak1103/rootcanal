package hostpool

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/zorak1103/rootcanal/internal/config"
	"github.com/zorak1103/rootcanal/internal/sshconn"
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

func (p *Pool) effectiveKeepalive(hostName string) (interval time.Duration, maxFails int) {
	h := p.cfg.Hosts[hostName]
	interval = p.cfg.Limits.DefaultKeepaliveInterval
	maxFails = p.cfg.Limits.DefaultKeepaliveMaxFailures
	if h.KeepaliveInterval != nil {
		interval = *h.KeepaliveInterval
	}
	if h.KeepaliveMaxFailures != nil {
		maxFails = *h.KeepaliveMaxFailures
	}
	return interval, maxFails
}

// errNoEntry is returned by acquireEntry when hostName has no cached entry
// yet. It is not a failure — callers use it to decide whether to fall through
// to the dial path.
var errNoEntry = errors.New("hostpool: no cached entry")

// acquireEntry looks up the cached entry for hostName, enforces the per-host
// session limit, clears any pending idle timer, and bumps the refcount. It
// returns errNoEntry if no entry exists yet. Runs under p.mu with
// defer-based unlock so a panic here (e.g. a mutated nil-guard around
// idleTimer.Stop()) cannot leave the pool permanently locked.
func (p *Pool) acquireEntry(hostName string) (*entry, *ssh.Client, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	e, exists := p.entries[hostName]
	if !exists {
		return nil, nil, errNoEntry
	}
	if p.cfg.Limits.MaxSessionsPerHost > 0 && e.refs >= p.cfg.Limits.MaxSessionsPerHost {
		return nil, nil, fmt.Errorf("host %q: per-host session limit of %d reached", hostName, p.cfg.Limits.MaxSessionsPerHost)
	}
	if e.idleTimer != nil {
		e.idleTimer.Stop()
		e.idleTimer = nil
	}
	e.refs++
	return e, e.client, nil
}

// storeDialedEntry inserts a freshly dialed client into the pool under
// hostName, starting its keepalive goroutine, unless a racing singleflight
// call already stored an entry for the same host first — in which case the
// existing entry wins and the caller must close the now-redundant client
// (returned as dup). Runs under p.mu with defer-based unlock.
func (p *Pool) storeDialedEntry(hostName string, client *ssh.Client) (winner, dup *ssh.Client) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Defensive: if a racing singleflight call already stored an entry
	// (possible after a previous Do completed for the same key), prefer it.
	if existing, found := p.entries[hostName]; found {
		return existing.client, client
	}
	interval, maxFails := p.effectiveKeepalive(hostName)
	e := &entry{client: client, refs: 0}
	// Build the eviction closure before starting keepalive so that e is
	// fully initialized before any goroutine can reference it.
	e.onDead = func() { p.evictIfCurrent(hostName, e) }
	e.stopKeepalive = sshconn.StartKeepalive(client, interval, maxFails, nil, e.onDead)
	p.entries[hostName] = e
	return client, nil
}

// evictIfCurrent removes hostName's entry from the pool if it is still e,
// i.e. it hasn't already been replaced or evicted by another goroutine. Runs
// under p.mu with defer-based unlock.
func (p *Pool) evictIfCurrent(hostName string, e *entry) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if cur, exists := p.entries[hostName]; exists && cur == e {
		if cur.idleTimer != nil {
			cur.idleTimer.Stop()
		}
		delete(p.entries, hostName)
	}
}

// Get returns a shared *ssh.Client for hostName and a release func the caller
// must invoke when done. The caller must not call Close on the returned client.
func (p *Pool) Get(ctx context.Context, hostName string) (*ssh.Client, func(), error) {
	h, ok := p.cfg.Hosts[hostName]
	if !ok {
		return nil, nil, config.UnknownHostError(hostName)
	}

	if e, client, err := p.acquireEntry(hostName); err == nil {
		return client, p.releaseFunc(hostName, e), nil
	} else if !errors.Is(err, errNoEntry) {
		return nil, nil, err
	}

	// Coalesce concurrent dials to the same host: exactly one SSH auth attempt
	// reaches the server regardless of caller concurrency. The entry is stored
	// inside the singleflight function so waiters receive a client that is
	// already in the map — they only bump refs, never close the shared handle.
	_, err, _ := p.sf.Do(hostName, func() (any, error) {
		client, err := p.dialer.Dial(ctx, h, p.cfg.Limits)
		if err != nil {
			return nil, err
		}
		winner, dup := p.storeDialedEntry(hostName, client)
		if dup != nil {
			_ = dup.Close()
		}
		return winner, nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("connecting to %q: %w", hostName, sanitizeConnErr(err))
	}

	e, client, err := p.acquireEntry(hostName)
	if errors.Is(err, errNoEntry) {
		return nil, nil, fmt.Errorf("host %q: entry vanished after dial", hostName)
	}
	if err != nil {
		return nil, nil, err
	}

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
		return fmt.Errorf("network error: %w", netErr.Err)
	}
	return errors.New("network error")
}

// Close immediately closes all cached clients and stops idle timers.
func (p *Pool) Close() {
	for _, c := range p.drainEntries() {
		_ = c.Close()
	}
}

// drainEntries stops every cached entry's idle timer and keepalive, removes
// all entries from the pool, and returns their underlying clients for the
// caller to close outside the lock. Runs under p.mu with defer-based unlock
// so a panic partway through the loop cannot leave the pool locked.
func (p *Pool) drainEntries() []*ssh.Client {
	p.mu.Lock()
	defer p.mu.Unlock()

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
	return clients
}
