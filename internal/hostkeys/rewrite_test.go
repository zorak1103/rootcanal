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
