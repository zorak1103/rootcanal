package hostkeys

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// probeHostport is a syntactically valid host:port form used across this
// file's probe tests. It deliberately does not resolve via DNS, pinning that
// probeStoredEntries never calls net.ResolveTCPAddr — it only requires that
// hostport parses via net.SplitHostPort.
const probeHostport = "testhost:99999"

// malformedHostport has no colon, so knownhosts' internal net.SplitHostPort
// fails on it and probeStoredEntries returns a plain error rather than a
// *knownhosts.KeyError — the failure mode selectTarget and
// writeKnownHostsEntry must surface as an error, never as "not stored".
const malformedHostport = "nohostport"

// keyB64 returns the base64-encoded wire format of a public key — exactly what
// appears in a known_hosts file. This is the correct token to search for when
// verifying file contents (ssh.FingerprintSHA256 is a hash, never stored inline).
func keyB64(k ssh.PublicKey) string {
	return base64.StdEncoding.EncodeToString(k.Marshal())
}

func TestRewriteLine_ReplacesTargetLine(t *testing.T) {
	dir := t.TempDir()
	key1 := newTestKey(t)
	key2 := newTestKey(t)
	newKey := newTestKey(t)

	line1 := knownhosts.Line([]string{"host-a"}, key1)
	line2 := knownhosts.Line([]string{"host-b"}, key2)
	khPath := filepath.Join(dir, "known_hosts")
	_ = os.WriteFile(khPath, []byte(line1+"\n"+line2+"\n"), 0600)

	newLine := knownhosts.Line([]string{"host-a"}, newKey)
	if err := rewriteLine(khPath, 1, newLine); err != nil {
		t.Fatalf("rewriteLine: %v", err)
	}

	data, _ := os.ReadFile(khPath)
	content := string(data)
	if !strings.Contains(content, keyB64(newKey)) {
		t.Errorf("rewritten file does not contain new key; file:\n%s", content)
	}
	if strings.Contains(content, keyB64(key1)) {
		t.Error("old key still present after rewrite")
	}
	if !strings.Contains(content, keyB64(key2)) {
		t.Error("second host line was lost")
	}
}

func TestRewriteLine_Perms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not enforced on Windows")
	}
	dir := t.TempDir()
	khPath := filepath.Join(dir, "known_hosts")
	_ = os.WriteFile(khPath, []byte("line1\n"), 0600)
	if err := rewriteLine(khPath, 1, "line1-replaced"); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(khPath)
	if fi.Mode().Perm() != 0600 {
		t.Errorf("perm = %o, want 0600", fi.Mode().Perm())
	}
}

func TestProbeStoredEntries_InvalidFile(t *testing.T) {
	_, err := probeStoredEntries(filepath.Join(t.TempDir(), "does-not-exist"), probeHostport)
	if err == nil {
		t.Fatal("expected error for a known_hosts path that cannot be loaded")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("error = %q, want it to mention the file does not exist", err.Error())
	}
}

func TestProbeStoredEntries_CorruptFile_Errors(t *testing.T) {
	// This is the reachable probe failure: known_hosts exists but a line
	// cannot be parsed. It's distinct from a missing file (which is caught
	// earlier by os.Stat) and from a malformed hostport passed by the caller —
	// neither of the other probe-failure tests in this file exercises it.
	dir := t.TempDir()
	khPath := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(khPath, []byte("not a known_hosts line at all\n"), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := probeStoredEntries(khPath, probeHostport)
	if err == nil {
		t.Fatal("expected error for a known_hosts file with unparseable content")
	}
}

func TestSelectTarget_DifferentKeyType_Errors(t *testing.T) {
	dir := t.TempDir()
	storedKey := newTestKey(t) // ecdsa-sha2-nistp256
	liveKey := newTestKeyEd25519(t)
	khPath := filepath.Join(dir, "known_hosts")
	line := knownhosts.Line([]string{knownhosts.Normalize(probeHostport)}, storedKey)
	if err := os.WriteFile(khPath, []byte(line+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	entries, err := probeStoredEntries(khPath, probeHostport)
	if err != nil {
		t.Fatalf("probeStoredEntries: %v", err)
	}

	// selectTarget must surface a key-type mismatch as an error, not as "0, nil"
	// (= not stored) — appending in that case would leave the stale ecdsa entry
	// trusted alongside the new ed25519 one. The message must name the single
	// stale entry's type and line, not the "N stored entries" wording used for
	// genuine duplicates (TestSelectTarget_DuplicateEntries_Errors) — those are
	// different operator-facing situations even though both are errors.
	_, err = selectTarget(entries, liveKey, khPath, probeHostport)
	if err == nil {
		t.Fatal("expected error for a stored entry of a different key type")
	}
	if !strings.Contains(err.Error(), storedKey.Type()) {
		t.Errorf("error should name the stale entry's key type %q: %v", storedKey.Type(), err)
	}
	if strings.Contains(err.Error(), "stored entries for") {
		t.Errorf("single type-mismatch entry should not be reported as duplicate entries: %v", err)
	}
}

func TestSelectTarget_DuplicateEntries_Errors(t *testing.T) {
	dir := t.TempDir()
	key1 := newTestKey(t)
	key2 := newTestKey(t)
	liveKey := newTestKey(t)
	khPath := filepath.Join(dir, "known_hosts")
	line1 := knownhosts.Line([]string{knownhosts.Normalize(probeHostport)}, key1)
	line2 := knownhosts.Line([]string{knownhosts.Normalize(probeHostport)}, key2)
	if err := os.WriteFile(khPath, []byte(line1+"\n"+line2+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	entries, err := probeStoredEntries(khPath, probeHostport)
	if err != nil {
		t.Fatalf("probeStoredEntries: %v", err)
	}

	// Two same-type entries is the genuine "which one supersedes" ambiguity,
	// distinct from a single differently-typed entry — the message must say
	// how many entries were found, not the single-entry type-mismatch wording.
	_, err = selectTarget(entries, liveKey, khPath, probeHostport)
	if err == nil {
		t.Fatal("expected error for duplicate stored entries")
	}
	if !strings.Contains(err.Error(), "2 stored entries") {
		t.Errorf("error should report the entry count: %v", err)
	}
}

func TestSelectTarget_UnresolvableHost_FindsLine(t *testing.T) {
	dir := t.TempDir()
	storedKey := newTestKey(t)
	khPath := filepath.Join(dir, "known_hosts")
	line := knownhosts.Line([]string{knownhosts.Normalize(probeHostport)}, storedKey)
	if err := os.WriteFile(khPath, []byte(line+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	entries, err := probeStoredEntries(khPath, probeHostport)
	if err != nil {
		t.Fatalf("probeStoredEntries: %v", err)
	}
	lineNum, err := selectTarget(entries, storedKey, khPath, probeHostport)
	if err != nil {
		t.Fatalf("selectTarget: %v", err)
	}
	if lineNum != 1 {
		t.Errorf("expected line 1 for matching unresolvable host, got %d", lineNum)
	}
}

func TestProbeStoredEntries_MalformedHostport_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	storedKey := newTestKey(t)
	khPath := filepath.Join(dir, "known_hosts")
	line := knownhosts.Line([]string{knownhosts.Normalize(probeHostport)}, storedKey)
	if err := os.WriteFile(khPath, []byte(line+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	// malformedHostport has no colon, so knownhosts' internal SplitHostPort fails
	// and the probe returns a plain error rather than a *knownhosts.KeyError. That
	// must surface as an error here, not as "not stored" — see probeStoredEntries's
	// doc comment for why conflating the two is a security bug.
	_, err := probeStoredEntries(khPath, malformedHostport)
	if err == nil {
		t.Fatal("expected error for a malformed hostport that fails knownhosts' internal SplitHostPort")
	}
}

func TestWriteKnownHostsEntry_UnresolvableHost_RewritesInPlace(t *testing.T) {
	dir := t.TempDir()
	oldKey := newTestKey(t)
	newKey := newTestKey(t)
	khPath := filepath.Join(dir, "known_hosts")
	line := knownhosts.Line([]string{knownhosts.Normalize(probeHostport)}, oldKey)
	if err := os.WriteFile(khPath, []byte(line+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := writeKnownHostsEntry(khPath, probeHostport, newKey, 1, oldKey); err != nil {
		t.Fatalf("writeKnownHostsEntry: %v", err)
	}

	content, err := os.ReadFile(khPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	want := knownhosts.Line([]string{knownhosts.Normalize(probeHostport)}, newKey) + "\n"
	if string(content) != want {
		t.Errorf("content = %q, want %q", content, want)
	}
}

func TestWriteKnownHostsEntry_AppendsWhenLineNumZero(t *testing.T) {
	dir := t.TempDir()
	khPath := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(khPath, []byte(""), 0600); err != nil {
		t.Fatal(err)
	}
	newKey := newTestKey(t)

	if err := writeKnownHostsEntry(khPath, probeHostport, newKey, 0, nil); err != nil {
		t.Fatalf("writeKnownHostsEntry: %v", err)
	}

	content, err := os.ReadFile(khPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	want := knownhosts.Line([]string{knownhosts.Normalize(probeHostport)}, newKey) + "\n"
	if string(content) != want {
		t.Errorf("content = %q, want %q", content, want)
	}
}

// TestRewriteKnownHostsLine_PreservesPatternField pins that rewriting a line
// keeps its existing host-pattern field — a hostname/IP alias list or a
// hashed "|1|salt|hash" pattern — instead of regenerating one from hostport,
// which would silently drop aliases or de-hash a hashed known_hosts file.
func TestRewriteKnownHostsLine_PreservesPatternField(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
	}{
		{"alias list", "web1.example.com,192.0.2.1"},
		{"hashed pattern", "|1|3sM2wtxfhjWmwUq0hMqZAF7NBSU=|xIwlrPYwSk2fBWzHBQtP2vcnR/A="},
		{"bracketed port", "[web1.example.com]:2222"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			oldKey := newTestKey(t)
			newKey := newTestKey(t)
			khPath := filepath.Join(dir, "known_hosts")
			line := knownhosts.Line([]string{tt.pattern}, oldKey)
			if err := os.WriteFile(khPath, []byte(line+"\n"), 0600); err != nil {
				t.Fatal(err)
			}

			if err := rewriteKnownHostsLine(khPath, 1, newKey, oldKey); err != nil {
				t.Fatalf("rewriteKnownHostsLine: %v", err)
			}

			content, err := os.ReadFile(khPath)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(string(content), tt.pattern+" ") {
				t.Errorf("pattern field not preserved; content = %q, want prefix %q", content, tt.pattern+" ")
			}
			if !strings.Contains(string(content), keyB64(newKey)) {
				t.Errorf("rewritten content does not contain new key; content = %q", content)
			}
			if strings.Contains(string(content), keyB64(oldKey)) {
				t.Error("old key still present after rewrite")
			}
		})
	}
}

func TestRewriteKnownHostsLine_ContentChangedUnderUs_Errors(t *testing.T) {
	dir := t.TempDir()
	onDiskKey := newTestKey(t)
	staleExpectedKey := newTestKey(t) // what the caller thinks is on line 1 — it's wrong
	newKey := newTestKey(t)
	khPath := filepath.Join(dir, "known_hosts")
	line := knownhosts.Line([]string{knownhosts.Normalize(probeHostport)}, onDiskKey)
	original := line + "\n"
	if err := os.WriteFile(khPath, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}

	// The file changed under the caller between the probe that chose line 1
	// and this write (e.g. a concurrent ssh_accept_host_key call, or a manual
	// ssh-keygen -R) — writing anyway could land newKey on an entry for an
	// unrelated key.
	if err := rewriteKnownHostsLine(khPath, 1, newKey, staleExpectedKey); err == nil {
		t.Fatal("expected error when the on-disk key no longer matches what the caller expected")
	}

	content, err := os.ReadFile(khPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != original {
		t.Errorf("file was modified despite the expect mismatch: got %q, want unchanged %q", content, original)
	}
}

func TestRewriteKnownHostsLine_MarkerLine_Errors(t *testing.T) {
	dir := t.TempDir()
	oldKey := newTestKey(t)
	newKey := newTestKey(t)
	khPath := filepath.Join(dir, "known_hosts")
	line := "@cert-authority " + knownhosts.Line([]string{knownhosts.Normalize(probeHostport)}, oldKey)
	original := line + "\n"
	if err := os.WriteFile(khPath, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}

	if err := rewriteKnownHostsLine(khPath, 1, newKey, oldKey); err == nil {
		t.Fatal("expected error for an @cert-authority marker line")
	}

	content, err := os.ReadFile(khPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != original {
		t.Errorf("file was modified on a marker line: got %q, want unchanged %q", content, original)
	}
}

func TestRewriteLine_FileNotFound(t *testing.T) {
	err := rewriteLine(filepath.Join(t.TempDir(), "missing"), 1, "line")
	if err == nil {
		t.Fatal("expected error for a nonexistent known_hosts file")
	}
}

func TestRewriteLine_LineOutOfRange(t *testing.T) {
	dir := t.TempDir()
	khPath := filepath.Join(dir, "known_hosts")
	_ = os.WriteFile(khPath, []byte("line1\n"), 0600)
	if err := rewriteLine(khPath, 5, "line5"); err == nil {
		t.Fatal("expected out-of-range error for a line number beyond the file's length")
	}
}

func TestAppendLine_FileNotFound(t *testing.T) {
	err := appendLine(filepath.Join(t.TempDir(), "missing"), "line")
	if err == nil {
		t.Fatal("expected error for a nonexistent known_hosts file")
	}
}

func TestAtomicWrite_CreateTempFails_WhenDirMissing(t *testing.T) {
	missingDir := filepath.Join(t.TempDir(), "does-not-exist")
	err := atomicWrite(filepath.Join(missingDir, "known_hosts"), "content")
	if err == nil {
		t.Fatal("expected error when the target directory does not exist")
	}
}

func TestAtomicWrite_RenameFails_WhenTargetIsDir(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "known_hosts")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(target, "content"); err == nil {
		t.Fatal("expected rename error when the target path is an existing directory")
	}
}

func TestAppendLine_AddsEntry(t *testing.T) {
	dir := t.TempDir()
	key := newTestKey(t)
	khPath := filepath.Join(dir, "known_hosts")
	_ = os.WriteFile(khPath, []byte(""), 0600)

	newLine := knownhosts.Line([]string{"newhost"}, key)
	if err := appendLine(khPath, newLine); err != nil {
		t.Fatalf("appendLine: %v", err)
	}
	data, _ := os.ReadFile(khPath)
	if !strings.Contains(string(data), keyB64(key)) {
		t.Errorf("appended key not found; file:\n%s", data)
	}
}

func TestRewriteLine_LastLineNoTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	khPath := filepath.Join(dir, "known_hosts")
	// No trailing newline: strings.Split yields exactly 2 elements, so line 2
	// (the last one) is a legitimate, in-range target.
	if err := os.WriteFile(khPath, []byte("a\nb"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := rewriteLine(khPath, 2, "new"); err != nil {
		t.Fatalf("rewriteLine: %v", err)
	}

	data, _ := os.ReadFile(khPath)
	if string(data) != "a\nnew" {
		t.Errorf("content = %q, want %q", data, "a\nnew")
	}
}

func TestAppendLine_NoTrailingNewline_DoesNotCorruptLastEntry(t *testing.T) {
	dir := t.TempDir()
	key := newTestKey(t)
	khPath := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(khPath, []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}

	newLine := knownhosts.Line([]string{"newhost"}, key)
	if err := appendLine(khPath, newLine); err != nil {
		t.Fatalf("appendLine: %v", err)
	}

	data, _ := os.ReadFile(khPath)
	want := "abc\n" + newLine + "\n"
	if string(data) != want {
		t.Errorf("content = %q, want %q (the pre-existing entry must survive intact)", data, want)
	}
}
