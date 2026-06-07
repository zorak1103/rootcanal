package sshconn

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gitlab.com/zorak1103/rootcanal/internal/config"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// startTestSSHServer starts a minimal in-process SSH server that accepts all
// connections (NoClientAuth). Returns the listener address and a known_hosts
// file path pre-seeded with the server's host key.
func startTestSSHServer(t *testing.T) (addr, knownHostsPath string) {
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

	knownHostsPath = filepath.Join(t.TempDir(), "known_hosts")
	line := knownhosts.Line([]string{knownhosts.Normalize(addr)}, serverSigner.PublicKey())
	if err := os.WriteFile(knownHostsPath, []byte(line+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

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

// generateTestKeyFile writes a fresh ECDSA private key as PEM to a temp file.
func generateTestKeyFile(t *testing.T) string {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	path := filepath.Join(t.TempDir(), "id_ecdsa")
	if err := os.WriteFile(path, pemBytes, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

// ---- expandPath ----

func TestExpandPath(t *testing.T) {
	home, _ := os.UserHomeDir()
	tests := []struct{ in, want string }{
		{"~/foo/bar", filepath.Join(home, "foo/bar")},
		{"/abs/path", "/abs/path"},
		{"relative", "relative"},
		{"~no-slash", "~no-slash"},
	}
	for _, tt := range tests {
		if got := expandPath(tt.in); got != tt.want {
			t.Errorf("expandPath(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// ---- resolveKnownHosts ----

func TestResolveKnownHosts(t *testing.T) {
	home, _ := os.UserHomeDir()
	sysPath := filepath.Join(home, ".ssh", "known_hosts")

	if got := ResolveKnownHosts("system"); got != sysPath {
		t.Errorf("ResolveKnownHosts(system) = %q, want %q", got, sysPath)
	}
	explicit := "/etc/ssh/known_hosts"
	if got := ResolveKnownHosts(explicit); got != explicit {
		t.Errorf("ResolveKnownHosts(%q) = %q, want %q", explicit, got, explicit)
	}
}

// ---- buildPasswordAuth ----

func TestBuildPasswordAuth(t *testing.T) {
	t.Run("env set", func(t *testing.T) {
		t.Setenv("TEST_RC_PASS", "secret")
		methods, err := buildPasswordAuth(config.Auth{PasswordEnv: "TEST_RC_PASS"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(methods) != 1 {
			t.Errorf("expected 1 method, got %d", len(methods))
		}
	})

	t.Run("env missing", func(t *testing.T) {
		os.Unsetenv("TEST_RC_PASS_MISSING")
		_, err := buildPasswordAuth(config.Auth{PasswordEnv: "TEST_RC_PASS_MISSING"})
		if err == nil {
			t.Fatal("expected error when env var unset")
		}
	})
}

// ---- buildKeyAuth ----

func TestBuildKeyAuth(t *testing.T) {
	t.Run("valid key", func(t *testing.T) {
		keyPath := generateTestKeyFile(t)
		methods, err := buildKeyAuth(config.Auth{KeyPath: keyPath})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(methods) != 1 {
			t.Errorf("expected 1 method, got %d", len(methods))
		}
	})

	t.Run("missing file", func(t *testing.T) {
		_, err := buildKeyAuth(config.Auth{KeyPath: "/nonexistent/key"})
		if err == nil {
			t.Fatal("expected error for missing file")
		}
	})

	t.Run("invalid key data", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "bad_key")
		_ = os.WriteFile(path, []byte("not a valid pem key"), 0600)
		_, err := buildKeyAuth(config.Auth{KeyPath: path})
		if err == nil {
			t.Fatal("expected error for invalid key data")
		}
	})
}

// ---- BuildClientConfig + ProdDialer.Dial (integration) ----

func TestProdDialer_Dial(t *testing.T) {
	addr, knownHostsPath := startTestSSHServer(t)

	t.Setenv("TEST_RC_DIAL_PASS", "irrelevant") // server has NoClientAuth

	h := config.Host{
		Address:    addr,
		User:       "testuser",
		KnownHosts: knownHostsPath,
		Auth:       config.Auth{Type: "password", PasswordEnv: "TEST_RC_DIAL_PASS"},
	}
	limits := config.Limits{DialTimeout: 5 * time.Second}

	client, err := ProdDialer{}.Dial(context.Background(), h, limits)
	if err != nil {
		t.Fatalf("Dial() unexpected error: %v", err)
	}
	_ = client.Close()
}

func TestProdDialer_Dial_BadHost(t *testing.T) {
	_, knownHostsPath := startTestSSHServer(t)
	t.Setenv("TEST_RC_DIAL_PASS2", "x")

	h := config.Host{
		Address:    "127.0.0.1:1", // port 1 — should fail TCP connect
		User:       "u",
		KnownHosts: knownHostsPath,
		Auth:       config.Auth{Type: "password", PasswordEnv: "TEST_RC_DIAL_PASS2"},
	}
	limits := config.Limits{DialTimeout: 500 * time.Millisecond}

	_, err := ProdDialer{}.Dial(context.Background(), h, limits)
	if err == nil {
		t.Fatal("expected error for unreachable host")
	}
}

func TestBuildClientConfig_HostKeyMismatch(t *testing.T) {
	// Write a known_hosts with a random key; server will present a different key.
	dir := t.TempDir()
	tmpKey := filepath.Join(dir, "kh")

	// Grab one server's public key but point the client at a different known_hosts.
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	signer, _ := ssh.NewSignerFromKey(priv)
	line := knownhosts.Line([]string{knownhosts.Normalize("127.0.0.1:22222")}, signer.PublicKey())
	_ = os.WriteFile(tmpKey, []byte(line+"\n"), 0600)

	t.Setenv("TEST_RC_CFG_PASS", "x")
	h := config.Host{
		Address:    "127.0.0.1:22222",
		User:       "u",
		KnownHosts: tmpKey,
		Auth:       config.Auth{Type: "password", PasswordEnv: "TEST_RC_CFG_PASS"},
	}
	cfg, err := BuildClientConfig(h)
	if err != nil {
		t.Fatalf("BuildClientConfig() unexpected error: %v", err)
	}
	// Config itself is valid; mismatch only fires on actual connection.
	if cfg.User != "u" {
		t.Errorf("User = %q, want %q", cfg.User, "u")
	}
}

func TestDial_KeyMismatchHint(t *testing.T) {
	addr, _ := startTestSSHServer(t)

	// Write a *different* key into known_hosts — produces a real mismatch on dial.
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	signer, _ := ssh.NewSignerFromKey(priv)
	dir := t.TempDir()
	khPath := filepath.Join(dir, "mismatch_kh")
	line := knownhosts.Line([]string{knownhosts.Normalize(addr)}, signer.PublicKey())
	_ = os.WriteFile(khPath, []byte(line+"\n"), 0600)

	t.Setenv("TEST_KH_MISMATCH_PASS", "x")
	h := config.Host{
		Address:    addr,
		User:       "u",
		KnownHosts: khPath,
		Auth:       config.Auth{Type: "password", PasswordEnv: "TEST_KH_MISMATCH_PASS"},
	}
	limits := config.Limits{DialTimeout: 2 * time.Second}

	_, err := ProdDialer{}.Dial(context.Background(), h, limits)
	if err == nil {
		t.Fatal("expected key mismatch error")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "ssh_accept_host_key") {
		t.Errorf("error missing ssh_accept_host_key hint; got: %s", errStr)
	}
	if !strings.Contains(errStr, "allow_known_hosts_update") {
		t.Errorf("error missing allow_known_hosts_update hint; got: %s", errStr)
	}
}

func TestBuildClientConfig_BadKnownHosts(t *testing.T) {
	t.Setenv("TEST_RC_CFG2_PASS", "x")
	h := config.Host{
		Address:    "h:22",
		User:       "u",
		KnownHosts: "/nonexistent/known_hosts_file",
		Auth:       config.Auth{Type: "password", PasswordEnv: "TEST_RC_CFG2_PASS"},
	}
	_, err := BuildClientConfig(h)
	if err == nil {
		t.Fatal("expected error for missing known_hosts")
	}
}

func TestBuildAuthMethods_AgentBranch(t *testing.T) {
	// Exercises the "agent" case in buildAuthMethods regardless of whether
	// the agent is actually running.
	_, _ = buildAuthMethods(config.Host{Auth: config.Auth{Type: "agent"}})
}

func TestBuildAuthMethods_UnknownType(t *testing.T) {
	_, err := buildAuthMethods(config.Host{Auth: config.Auth{Type: "kerberos"}})
	if err == nil {
		t.Fatal("expected error for unknown auth type")
	}
}

func TestProdDialer_Dial_ZeroDialTimeout(t *testing.T) {
	// Regression test: DialTimeout=0 must not set an immediate deadline.
	// time.Now().Add(0) == now, which would expire before the handshake starts.
	addr, knownHostsPath := startTestSSHServer(t)
	t.Setenv("TEST_RC_ZERO_PASS", "x")

	h := config.Host{
		Address:    addr,
		User:       "u",
		KnownHosts: knownHostsPath,
		Auth:       config.Auth{Type: "password", PasswordEnv: "TEST_RC_ZERO_PASS"},
	}
	client, err := ProdDialer{}.Dial(context.Background(), h, config.Limits{DialTimeout: 0})
	if err != nil {
		t.Fatalf("Dial with DialTimeout=0 failed: %v (zero timeout must not set an immediate deadline)", err)
	}
	_ = client.Close()
}

func TestProdDialer_Dial_BadHandshake(t *testing.T) {
	// TCP listener that accepts but does not speak SSH → handshake must fail.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	addr := ln.Addr().String()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		conn.Write([]byte("I am not SSH\r\n"))
		conn.Close()
	}()

	dir := t.TempDir()
	kh := filepath.Join(dir, "kh")
	os.WriteFile(kh, []byte("# empty\n"), 0600)
	t.Setenv("TEST_BADHS_PASS", "x")

	h := config.Host{
		Address:    addr,
		User:       "u",
		KnownHosts: kh,
		Auth:       config.Auth{Type: "password", PasswordEnv: "TEST_BADHS_PASS"},
	}
	_, err = ProdDialer{}.Dial(context.Background(), h, config.Limits{DialTimeout: 2 * time.Second})
	if err == nil {
		t.Fatal("expected handshake error for non-SSH server")
	}
}

func TestBuildClientConfig_KeyAuth(t *testing.T) {
	dir := t.TempDir()
	kh := filepath.Join(dir, "known_hosts")
	_ = os.WriteFile(kh, []byte("# empty\n"), 0600)
	keyPath := generateTestKeyFile(t)

	h := config.Host{
		Address:    "h:22",
		User:       "deploy",
		KnownHosts: kh,
		Auth:       config.Auth{Type: "key", KeyPath: keyPath},
	}
	cfg, err := BuildClientConfig(h)
	if err != nil {
		t.Fatalf("BuildClientConfig(key) unexpected error: %v", err)
	}
	if cfg.User != "deploy" {
		t.Errorf("User = %q, want deploy", cfg.User)
	}
	if len(cfg.Auth) == 0 {
		t.Error("expected auth methods, got none")
	}
}
