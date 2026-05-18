package sshconn

import (
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"gitlab.com/zorak1103/rootcanal/internal/config"
	"gitlab.com/zorak1103/rootcanal/internal/fileperms"
)

// ---- MAN-002: HostKeyAlgorithms pinning ----

func TestHostKeyCallback_PinsAlgorithms(t *testing.T) {
	addr, khPath := startTestSSHServer(t)

	h := config.Host{KnownHosts: khPath}
	_, algos, err := hostKeyCallback(h, addr)
	if err != nil {
		t.Fatalf("hostKeyCallback() error: %v", err)
	}
	if len(algos) == 0 {
		t.Fatal("expected non-empty algorithm list; got nil — server can drive downgrade")
	}
	// The test server uses ECDSA P256; verify the pinned type matches.
	const wantAlgo = "ecdsa-sha2-nistp256"
	if !slices.Contains(algos, wantAlgo) {
		t.Errorf("expected %q in pinned algorithms %v", wantAlgo, algos)
	}
}

func TestHostKeyCallback_NilAlgos_UnknownHost(t *testing.T) {
	// An address not present in known_hosts should yield no pinned algorithms,
	// but must still return without error (the callback handles the check at dial time).
	_, khPath := startTestSSHServer(t)

	h := config.Host{KnownHosts: khPath}
	_, algos, err := hostKeyCallback(h, "192.0.2.1:9999") // not in known_hosts
	if err != nil {
		t.Fatalf("hostKeyCallback() error for unknown host: %v", err)
	}
	// Nil/empty algos is acceptable for unknown hosts — ssh client falls back
	// to server preference ordering. This test simply verifies no panic.
	_ = algos
}

func TestBuildClientConfig_AlgorithmsPinned(t *testing.T) {
	addr, khPath := startTestSSHServer(t)
	t.Setenv("TEST_SEC_PASS", "x")

	h := config.Host{
		Address:    addr,
		User:       "u",
		KnownHosts: khPath,
		Auth:       config.Auth{Type: "password", PasswordEnv: "TEST_SEC_PASS"},
	}
	cfg, err := BuildClientConfig(h)
	if err != nil {
		t.Fatalf("BuildClientConfig(): %v", err)
	}
	if len(cfg.HostKeyAlgorithms) == 0 {
		t.Error("HostKeyAlgorithms must be non-empty after fix; nil allows server-driven downgrade")
	}
}

// ---- MAN-004: Handshake deadline ----

func TestProdDialer_HandshakeTimeout(t *testing.T) {
	// TCP listener that accepts but never speaks SSH — simulates a stalled server.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	addr := ln.Addr().String()

	// Accept connections but keep them alive without sending anything.
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// Hold the connection open indefinitely.
			go func(c net.Conn) { defer c.Close(); time.Sleep(10 * time.Minute) }(conn)
		}
	}()

	dir := t.TempDir()
	kh := filepath.Join(dir, "kh")
	os.WriteFile(kh, []byte("# empty\n"), 0600)
	t.Setenv("TEST_TIMEOUT_PASS", "x")

	h := config.Host{
		Address:    addr,
		User:       "u",
		KnownHosts: kh,
		Auth:       config.Auth{Type: "password", PasswordEnv: "TEST_TIMEOUT_PASS"},
	}
	const dialTimeout = 150 * time.Millisecond
	limits := config.Limits{DialTimeout: dialTimeout}

	start := time.Now()
	_, err = ProdDialer{}.Dial(t.Context(), h, limits)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected handshake error, got nil")
	}
	// Must return within 3× dialTimeout; must NOT block for the server's 10-min hold.
	if elapsed > 3*dialTimeout {
		t.Errorf("Dial blocked for %v; handshake deadline not enforced (timeout=%v)", elapsed, dialTimeout)
	}
}

// ---- MAN-006: File permission checks ----

func TestCheckFilePerms_InsecurePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission checks not enforced on Windows")
	}

	dir := t.TempDir()

	tests := []struct {
		perm    fs.FileMode
		wantErr bool
	}{
		{0o600, false},
		{0o640, true},  // group-readable
		{0o644, true},  // world-readable
		{0o660, true},  // group-writable
		{0o666, true},  // world-readable+writable
		{0o700, false}, // owner-only
		{0o755, true},  // world-readable
	}

	for _, tt := range tests {
		path := filepath.Join(dir, strings.ReplaceAll(tt.perm.String(), " ", "_"))
		if err := os.WriteFile(path, []byte("key"), tt.perm); err != nil {
			t.Fatalf("WriteFile %04o: %v", tt.perm, err)
		}
		err := fileperms.Check(path)
		if tt.wantErr && err == nil {
			t.Errorf("perm %04o: expected error, got nil", tt.perm)
		}
		if !tt.wantErr && err != nil {
			t.Errorf("perm %04o: unexpected error: %v", tt.perm, err)
		}
		if err != nil && !strings.Contains(err.Error(), "insecure permissions") {
			t.Errorf("perm %04o: error should mention 'insecure permissions', got: %v", tt.perm, err)
		}
	}
}

func TestBuildKeyAuth_InsecureKeyPerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission checks not enforced on Windows")
	}

	keyPath := generateTestKeyFile(t)
	// generateTestKeyFile writes with 0o600; make it world-readable.
	if err := os.Chmod(keyPath, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := buildKeyAuth(config.Auth{KeyPath: keyPath})
	if err == nil {
		t.Fatal("expected error for world-readable key; buildKeyAuth should reject insecure permissions")
	}
	if !strings.Contains(err.Error(), "insecure permissions") {
		t.Errorf("error should mention 'insecure permissions', got: %v", err)
	}
}
