package hostkeys

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// rewriteKnownHostsEntry finds the stored line for liveKey.Type() at hostport
// and replaces it. Appends a new line if no entry of that type is stored.
func rewriteKnownHostsEntry(path, hostport string, liveKey ssh.PublicKey) error {
	lineNum, err := findStoredKeyLine(path, hostport, liveKey.Type())
	if err != nil {
		return err
	}
	newLine := knownhosts.Line([]string{knownhosts.Normalize(hostport)}, liveKey)
	if lineNum == 0 {
		return appendLine(path, newLine)
	}
	return rewriteLine(path, lineNum, newLine)
}

// findStoredKeyLine probes path for a stored entry of keyType at hostport and
// returns its 1-indexed line number. Returns 0 if not found (caller will append).
func findStoredKeyLine(path, hostport, keyType string) (int, error) {
	cb, err := knownhosts.New(path)
	if err != nil {
		return 0, fmt.Errorf("loading known_hosts %q: %w", path, err)
	}
	addr, _ := net.ResolveTCPAddr("tcp", hostport)
	if addr == nil {
		addr = &net.TCPAddr{}
	}
	probeErr := cb(hostport, addr, probeKey{})
	var kerr *knownhosts.KeyError
	if !errors.As(probeErr, &kerr) {
		return 0, nil
	}
	for _, kk := range kerr.Want {
		if kk.Key.Type() == keyType {
			return kk.Line, nil
		}
	}
	return 0, nil
}

// rewriteLine replaces the 1-indexed lineNum in path with newLine atomically.
// All other lines are preserved byte-for-byte.
func rewriteLine(path string, lineNum int, newLine string) error {
	data, err := os.ReadFile(path) // #nosec G304 — operator-controlled known_hosts path
	if err != nil {
		return fmt.Errorf("reading %q: %w", path, err)
	}
	lines := strings.Split(string(data), "\n")
	if lineNum < 1 || lineNum > len(lines) {
		return fmt.Errorf("line %d out of range (file has %d lines)", lineNum, len(lines))
	}
	lines[lineNum-1] = newLine
	return atomicWrite(path, strings.Join(lines, "\n"))
}

// appendLine adds newLine to the end of path atomically.
func appendLine(path, newLine string) error {
	data, err := os.ReadFile(path) // #nosec G304
	if err != nil {
		return fmt.Errorf("reading %q: %w", path, err)
	}
	content := string(data)
	if content != "" && content[len(content)-1] != '\n' {
		content += "\n"
	}
	content += newLine + "\n"
	return atomicWrite(path, content)
}

// atomicWrite writes content to a temp file in the same dir as path (same
// filesystem → Rename is atomic), sets 0600 perms, then renames over path.
// 0600 is required so fileperms.Check passes on the next rootcanal dial.
func atomicWrite(path, content string) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".known_hosts_tmp")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("setting temp file permissions: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("replacing %q: %w", path, err)
	}
	return nil
}
