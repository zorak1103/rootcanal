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

func TestFindStoredKeyLine_InvalidFile(t *testing.T) {
	_, err := findStoredKeyLine(filepath.Join(t.TempDir(), "does-not-exist"), "host:22", "ssh-ed25519")
	if err == nil {
		t.Fatal("expected error for a known_hosts path that cannot be loaded")
	}
}

func TestFindStoredKeyLine_NoMatchingType(t *testing.T) {
	dir := t.TempDir()
	storedKey := newTestKey(t) // ecdsa-sha2-nistp256
	khPath := filepath.Join(dir, "known_hosts")
	line := knownhosts.Line([]string{knownhosts.Normalize("host:22")}, storedKey)
	_ = os.WriteFile(khPath, []byte(line+"\n"), 0600)

	// An entry exists for this host, but of a different key type — the caller
	// should be told to append rather than rewrite (line == 0).
	lineNum, err := findStoredKeyLine(khPath, "host:22", "ssh-ed25519")
	if err != nil {
		t.Fatalf("findStoredKeyLine: %v", err)
	}
	if lineNum != 0 {
		t.Errorf("expected 0 (no matching type), got %d", lineNum)
	}
}

func TestStoredFingerprint_InvalidFile(t *testing.T) {
	fp := storedFingerprint(filepath.Join(t.TempDir(), "missing"), "host:22", "ssh-ed25519")
	if fp != "" {
		t.Errorf("expected empty fingerprint for an unreadable known_hosts file, got %q", fp)
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
