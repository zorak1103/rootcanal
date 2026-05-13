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
	"testing"
	"time"

	"gitlab.com/zorak1103/rootcanal/internal/config"
	"gitlab.com/zorak1103/rootcanal/internal/sshconn"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

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
