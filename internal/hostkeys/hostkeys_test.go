package hostkeys

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func TestProbeStoredEntries_UnresolvableHost(t *testing.T) {
	dir := t.TempDir()
	storedKey := newTestKey(t)
	khPath := writeKnownHostsAt(t, dir, probeHostport, storedKey)

	entries, err := probeStoredEntries(khPath, probeHostport)
	if err != nil {
		t.Fatalf("probeStoredEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("probeStoredEntries() returned %d entries, want 1", len(entries))
	}
	wantFP := ssh.FingerprintSHA256(storedKey)
	if got := ssh.FingerprintSHA256(entries[0].Key); got != wantFP {
		t.Errorf("probeStoredEntries() key fingerprint = %q, want %q", got, wantFP)
	}
}

// TestAccept_MissingKnownHosts_ErrorMentionsCreate pins that a missing
// known_hosts file is reported as a distinct, actionable state — not folded
// into the generic "probe failed" error. rootcanal will not create the
// operator's trust store, and the only config value that reaches this with
// no os.Stat performed at load time is known_hosts: "system" on a machine
// that has never run ssh — the operator needs to be told to create the file,
// not left guessing why confirm=true refuses to bootstrap it.
func TestAccept_MissingKnownHosts_ErrorMentionsCreate(t *testing.T) {
	liveKey := newTestKey(t)
	host := fakeHost(filepath.Join(t.TempDir(), "missing"), true)
	cfg := makeCfg(host)
	r := New(cfg, &fakeScanner{key: liveKey})

	_, err := r.Accept(context.Background(), testHostName, ssh.FingerprintSHA256(liveKey))
	if err == nil {
		t.Fatal("expected error when known_hosts does not exist")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("error = %q, want it to mention the file does not exist", err.Error())
	}
}

func TestAccept_ProbeFails_MalformedHostport_LeavesFileUnchanged(t *testing.T) {
	dir := t.TempDir()
	storedKey := newTestKey(t)
	khPath := writeKnownHostsAt(t, dir, probeHostport, storedKey)
	liveKey := newTestKey(t)

	host := fakeHost(khPath, true)
	host.Address = malformedHostport // no colon: probeStoredEntries errors, doesn't return "not stored"
	cfg := makeCfg(host)
	r := New(cfg, &fakeScanner{key: liveKey})

	before, err := os.ReadFile(khPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Accept(context.Background(), testHostName, ssh.FingerprintSHA256(liveKey)); err == nil {
		t.Fatal("expected error when the known_hosts probe fails, not a degraded write")
	}
	after, err := os.ReadFile(khPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("file was modified on probe failure: got %q, want unchanged %q", after, before)
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

// newTestKeyEd25519 returns a key of a different type than newTestKey's
// ecdsa-sha2-nistp256 — needed to exercise the "stored entry is a different
// key type than the live key" path, which every other test in this file
// can't reach because it only ever uses one key type.
func newTestKeyEd25519(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return sshPub
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

func TestInspect_NothingStored(t *testing.T) {
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
	// Changed reports a rotation of a previously-trusted key, not "there was
	// never anything to compare against" — those are different states an
	// operator needs to tell apart (see the "no entry stored" vs "host key has
	// changed" messages in tools_knownhosts.go).
	if res.Changed {
		t.Error("want Changed=false when nothing was ever stored")
	}
	if len(res.StaleEntries) != 0 {
		t.Errorf("want no stale entries when known_hosts is empty; got %+v", res.StaleEntries)
	}
}

// TestInspect_DifferentKeyTypeStored_ReportsStaleEntry pins that a stored
// entry of a different key type than the live key is reported as a stale
// entry, not silently treated as "nothing stored" — the latter would make
// Accept append the live key alongside the old one, leaving both trusted.
func TestInspect_DifferentKeyTypeStored_ReportsStaleEntry(t *testing.T) {
	dir := t.TempDir()
	storedKey := newTestKey(t) // ecdsa-sha2-nistp256
	khPath := writeKnownHosts(t, dir, storedKey)
	liveKey := newTestKeyEd25519(t)
	cfg := makeCfg(fakeHost(khPath, true))
	r := New(cfg, &fakeScanner{key: liveKey})

	res, err := r.Inspect(context.Background(), "web1")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if res.CurrentFP != "" {
		t.Errorf("CurrentFP should be empty when the stored entry is a different key type; got %q", res.CurrentFP)
	}
	if res.Changed {
		t.Error("want Changed=false when the stored entry is a different key type (it's stale, not superseded)")
	}
	if len(res.StaleEntries) != 1 {
		t.Fatalf("want exactly 1 stale entry, got %+v", res.StaleEntries)
	}
	if res.StaleEntries[0].Type != storedKey.Type() {
		t.Errorf("stale entry type = %q, want %q", res.StaleEntries[0].Type, storedKey.Type())
	}
}

func TestInspect_ProbeFails(t *testing.T) {
	dir := t.TempDir()
	storedKey := newTestKey(t)
	khPath := writeKnownHostsAt(t, dir, probeHostport, storedKey)
	liveKey := newTestKey(t)

	host := fakeHost(khPath, true)
	host.Address = malformedHostport // no colon: probeStoredEntries errors, doesn't return "not stored"
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

// TestAccept_DuplicateSameTypeEntries_Errors is the regression test for the
// exact state main's append-on-probe-failure bug used to leave behind: two
// stored entries of the same type for one host. Accept must refuse to guess
// which one is superseded rather than rewriting the first and leaving the
// second trusted.
func TestAccept_DuplicateSameTypeEntries_Errors(t *testing.T) {
	dir := t.TempDir()
	key1 := newTestKey(t)
	key2 := newTestKey(t)
	liveKey := newTestKey(t)
	line1 := knownhosts.Line([]string{knownhosts.Normalize(testHostport)}, key1)
	line2 := knownhosts.Line([]string{knownhosts.Normalize(testHostport)}, key2)
	khPath := filepath.Join(dir, "known_hosts")
	original := line1 + "\n" + line2 + "\n"
	if err := os.WriteFile(khPath, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := makeCfg(fakeHost(khPath, true))
	r := New(cfg, &fakeScanner{key: liveKey})

	_, err := r.Accept(context.Background(), "web1", ssh.FingerprintSHA256(liveKey))
	if err == nil {
		t.Fatal("expected error when known_hosts has duplicate entries for the host")
	}

	after, readErr := os.ReadFile(khPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(after) != original {
		t.Errorf("file was modified on ambiguous duplicate entries: got %q, want unchanged %q", after, original)
	}
}

// TestAccept_DifferentKeyTypeStored_Errors is the regression test for a
// stored entry of a different key type than the live key: Accept must refuse
// rather than append, which would leave the old (possibly attacker-controlled,
// post-rebuild) key trusted alongside the new one.
func TestAccept_DifferentKeyTypeStored_Errors(t *testing.T) {
	dir := t.TempDir()
	storedKey := newTestKey(t) // ecdsa-sha2-nistp256
	khPath := writeKnownHosts(t, dir, storedKey)
	liveKey := newTestKeyEd25519(t)
	cfg := makeCfg(fakeHost(khPath, true))
	r := New(cfg, &fakeScanner{key: liveKey})

	before, err := os.ReadFile(khPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Accept(context.Background(), "web1", ssh.FingerprintSHA256(liveKey)); err == nil {
		t.Fatal("expected error when the stored entry is a different key type")
	}
	after, err := os.ReadFile(khPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("file was modified on key-type mismatch: got %q, want unchanged %q", after, before)
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
