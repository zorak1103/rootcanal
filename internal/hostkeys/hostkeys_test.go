package hostkeys

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/zorak1103/rootcanal/internal/config"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// --- fakeScanner ---

type fakeScanner struct {
	key ssh.PublicKey
	err error
}

func (f *fakeScanner) ScanHostKey(_ context.Context, _ config.Host, _ config.Limits) (ssh.PublicKey, error) {
	return f.key, f.err
}

func TestProbeKey_TypeAndVerify(t *testing.T) {
	var k probeKey
	if got := k.Type(); got != "ecdsa-sha2-nistp256" {
		t.Errorf("Type() = %q, want ecdsa-sha2-nistp256", got)
	}
	if err := k.Verify(nil, nil); err == nil {
		t.Error("Verify() should always return an error — probeKey exists only to trigger knownhosts.KeyError")
	}
}

func TestStoredFingerprint_UnresolvableHost(t *testing.T) {
	dir := t.TempDir()
	storedKey := newTestKey(t)
	khPath := writeKnownHostsAt(t, dir, unresolvableHostport, storedKey)

	wantFP := ssh.FingerprintSHA256(storedKey)
	gotFP := storedFingerprint(khPath, unresolvableHostport, storedKey.Type())
	if gotFP != wantFP {
		t.Errorf("storedFingerprint() = %q, want %q", gotFP, wantFP)
	}
}

func TestStoredFingerprint_InvalidFile(t *testing.T) {
	fp := storedFingerprint(filepath.Join(t.TempDir(), "missing"), unresolvableHostport, "ssh-ed25519")
	if fp != "" {
		t.Errorf("expected empty fingerprint for an unreadable known_hosts file, got %q", fp)
	}
}

func TestStoredFingerprint_MalformedHostport_ReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	storedKey := newTestKey(t)
	khPath := writeKnownHostsAt(t, dir, unresolvableHostport, storedKey)

	// A probe failure (malformed hostport, no colon) must be indistinguishable
	// from "not stored" here — storedFingerprint is used only by Accept, where
	// a "" result still leads to a safe outcome: rewriteKnownHostsEntry redoes
	// the same probe and errors out for real before any write happens. Inspect
	// does not use storedFingerprint for exactly this reason — see
	// TestInspect_ProbeFails.
	fp := storedFingerprint(khPath, malformedHostport, storedKey.Type())
	if fp != "" {
		t.Errorf("expected empty fingerprint for a malformed hostport, got %q", fp)
	}
}

// --- helpers ---

func newTestKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := ssh.NewPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return pub
}

// testHostport is the address used consistently across this file's fake
// hosts, known_hosts entries, and probes.
const testHostport = "127.0.0.1:2222"

// testHostName is the config.Config host key used consistently across this
// file's fake configs.
const testHostName = "web1"

func writeKnownHostsAt(t *testing.T, dir, hostport string, key ssh.PublicKey) string {
	t.Helper()
	path := filepath.Join(dir, "known_hosts")
	line := knownhosts.Line([]string{knownhosts.Normalize(hostport)}, key)
	if err := os.WriteFile(path, []byte(line+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeKnownHosts(t *testing.T, dir string, key ssh.PublicKey) string {
	t.Helper()
	return writeKnownHostsAt(t, dir, testHostport, key)
}

func fakeHost(khPath string, allow bool) config.Host {
	return config.Host{
		Address:               testHostport,
		User:                  "u",
		Auth:                  config.Auth{Type: "agent"},
		KnownHosts:            khPath,
		AllowKnownHostsUpdate: allow,
	}
}

func makeCfg(h config.Host) *config.Config {
	return &config.Config{
		Limits: config.Limits{DialTimeout: time.Second},
		Hosts:  map[string]config.Host{testHostName: h},
	}
}

// --- Inspect tests ---

func TestInspect_Changed(t *testing.T) {
	dir := t.TempDir()
	storedKey := newTestKey(t)
	liveKey := newTestKey(t)
	khPath := writeKnownHosts(t, dir, storedKey)

	cfg := makeCfg(fakeHost(khPath, true))
	r := New(cfg, &fakeScanner{key: liveKey})

	res, err := r.Inspect(context.Background(), "web1")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if !res.Changed {
		t.Error("want Changed=true")
	}
	if res.NewFP == res.CurrentFP {
		t.Error("fingerprints should differ")
	}
	if res.NewFP != ssh.FingerprintSHA256(liveKey) {
		t.Errorf("NewFP = %q, want %q", res.NewFP, ssh.FingerprintSHA256(liveKey))
	}
	if res.CurrentFP != ssh.FingerprintSHA256(storedKey) {
		t.Errorf("CurrentFP = %q, want %q", res.CurrentFP, ssh.FingerprintSHA256(storedKey))
	}
}

func TestInspect_NotChanged(t *testing.T) {
	dir := t.TempDir()
	key := newTestKey(t)
	khPath := writeKnownHosts(t, dir, key)

	cfg := makeCfg(fakeHost(khPath, true))
	r := New(cfg, &fakeScanner{key: key})

	res, err := r.Inspect(context.Background(), "web1")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if res.Changed {
		t.Error("want Changed=false when live key matches stored")
	}
}

func TestInspect_NotPermitted(t *testing.T) {
	dir := t.TempDir()
	khPath := writeKnownHosts(t, dir, newTestKey(t))
	cfg := makeCfg(fakeHost(khPath, false /* allow=false */))
	r := New(cfg, &fakeScanner{key: newTestKey(t)})

	_, err := r.Inspect(context.Background(), "web1")
	if err == nil {
		t.Fatal("expected error for host without AllowKnownHostsUpdate")
	}
}

func TestInspect_UnknownHost(t *testing.T) {
	cfg := &config.Config{Hosts: map[string]config.Host{}}
	r := New(cfg, &fakeScanner{key: newTestKey(t)})
	_, err := r.Inspect(context.Background(), "nohost")
	if err == nil {
		t.Fatal("expected error for unknown host")
	}
}

func TestInspect_NoStoredKeyOfType(t *testing.T) {
	dir := t.TempDir()
	khPath := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(khPath, []byte(""), 0600); err != nil {
		t.Fatal(err)
	}
	liveKey := newTestKey(t)
	cfg := makeCfg(fakeHost(khPath, true))
	r := New(cfg, &fakeScanner{key: liveKey})

	res, err := r.Inspect(context.Background(), "web1")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if res.CurrentFP != "" {
		t.Errorf("CurrentFP should be empty when no stored entry; got %q", res.CurrentFP)
	}
	if !res.Changed {
		t.Error("want Changed=true when no stored entry exists")
	}
}

func TestInspect_ProbeFails(t *testing.T) {
	dir := t.TempDir()
	storedKey := newTestKey(t)
	khPath := writeKnownHostsAt(t, dir, unresolvableHostport, storedKey)
	liveKey := newTestKey(t)

	host := fakeHost(khPath, true)
	host.Address = malformedHostport // no colon: probeStoredKey errors, doesn't return "not stored"
	cfg := makeCfg(host)
	r := New(cfg, &fakeScanner{key: liveKey})

	// A probe failure must surface as an error, never as CurrentFP=="" with
	// Changed=true — that combination reads as "no key stored, safe to trust",
	// which is exactly the wrong signal to hand an operator (or the LLM driving
	// ssh_accept_host_key) deciding whether to confirm a host key change.
	if _, err := r.Inspect(context.Background(), testHostName); err == nil {
		t.Fatal("expected error when the known_hosts probe fails, not a degraded success result")
	}
}

// --- Accept tests ---

func TestAccept_WritesNewEntry(t *testing.T) {
	dir := t.TempDir()
	storedKey := newTestKey(t)
	liveKey := newTestKey(t)
	khPath := writeKnownHosts(t, dir, storedKey)

	cfg := makeCfg(fakeHost(khPath, true))
	r := New(cfg, &fakeScanner{key: liveKey})

	res, err := r.Accept(context.Background(), "web1", ssh.FingerprintSHA256(liveKey))
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if !res.Refreshed {
		t.Error("want Refreshed=true")
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(khPath)
		if fi.Mode().Perm() != 0600 {
			t.Errorf("known_hosts perm = %o, want 0600", fi.Mode().Perm())
		}
	}
}

func TestAccept_AlreadyCurrent(t *testing.T) {
	dir := t.TempDir()
	key := newTestKey(t)
	khPath := writeKnownHosts(t, dir, key)
	cfg := makeCfg(fakeHost(khPath, true))
	r := New(cfg, &fakeScanner{key: key})

	res, err := r.Accept(context.Background(), "web1", ssh.FingerprintSHA256(key))
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if res.Refreshed {
		t.Error("want Refreshed=false when key already current")
	}
}

func TestAccept_UnknownHost(t *testing.T) {
	cfg := &config.Config{Hosts: map[string]config.Host{}}
	r := New(cfg, &fakeScanner{key: newTestKey(t)})
	_, err := r.Accept(context.Background(), "nohost", "fp")
	if err == nil {
		t.Fatal("expected error for unknown host")
	}
}

func TestAccept_ScanError(t *testing.T) {
	dir := t.TempDir()
	khPath := writeKnownHosts(t, dir, newTestKey(t))
	cfg := makeCfg(fakeHost(khPath, true))
	r := New(cfg, &fakeScanner{err: errors.New("scan failed")})

	_, err := r.Accept(context.Background(), "web1", "fp")
	if err == nil {
		t.Fatal("expected error when the scanner fails")
	}
}

func TestAccept_MissingExpectedFingerprint(t *testing.T) {
	dir := t.TempDir()
	khPath := writeKnownHosts(t, dir, newTestKey(t))
	cfg := makeCfg(fakeHost(khPath, true))
	r := New(cfg, &fakeScanner{key: newTestKey(t)})

	_, err := r.Accept(context.Background(), "web1", "")
	if err == nil {
		t.Fatal("expected error for empty expected_fingerprint")
	}
}

func TestAccept_FingerprintMismatch(t *testing.T) {
	dir := t.TempDir()
	storedKey := newTestKey(t)
	liveKey := newTestKey(t)
	khPath := writeKnownHosts(t, dir, storedKey)
	cfg := makeCfg(fakeHost(khPath, true))
	r := New(cfg, &fakeScanner{key: liveKey})

	_, err := r.Accept(context.Background(), "web1", ssh.FingerprintSHA256(storedKey))
	if err == nil {
		t.Fatal("expected error when expected_fingerprint doesn't match live key")
	}
}

func TestAccept_OtherHostsPreserved(t *testing.T) {
	dir := t.TempDir()
	key1 := newTestKey(t)
	key2 := newTestKey(t)
	liveKey := newTestKey(t)
	line1 := knownhosts.Line([]string{knownhosts.Normalize("127.0.0.1:2222")}, key1)
	line2 := knownhosts.Line([]string{knownhosts.Normalize("10.0.0.1:22")}, key2)
	khPath := filepath.Join(dir, "known_hosts")
	_ = os.WriteFile(khPath, []byte(line1+"\n"+line2+"\n"), 0600)

	cfg := makeCfg(fakeHost(khPath, true))
	r := New(cfg, &fakeScanner{key: liveKey})

	_, err := r.Accept(context.Background(), "web1", ssh.FingerprintSHA256(liveKey))
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	cb, _ := knownhosts.New(khPath)
	addr, _ := net.ResolveTCPAddr("tcp", "10.0.0.1:22")
	if cbErr := cb("10.0.0.1:22", addr, key2); cbErr != nil {
		t.Errorf("other host no longer verifiable: %v", cbErr)
	}
}

func TestAccept_AppendsWhenNoStoredEntry(t *testing.T) {
	dir := t.TempDir()
	khPath := filepath.Join(dir, "known_hosts")
	_ = os.WriteFile(khPath, []byte(""), 0600)
	liveKey := newTestKey(t)
	cfg := makeCfg(fakeHost(khPath, true))
	r := New(cfg, &fakeScanner{key: liveKey})

	res, err := r.Accept(context.Background(), "web1", ssh.FingerprintSHA256(liveKey))
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if !res.Refreshed {
		t.Error("want Refreshed=true when appending new entry")
	}
}
