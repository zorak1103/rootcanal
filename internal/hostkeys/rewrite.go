package hostkeys

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// writeKnownHostsEntry writes liveKey into path at lineNum, or appends a new
// line when lineNum is 0 (selectTarget found nothing stored for hostport).
// expect is the key selectTarget saw at that line when it decided to rewrite
// (nil when appending); rewriteKnownHostsLine verifies it is still there
// before writing.
func writeKnownHostsEntry(path, hostport string, liveKey ssh.PublicKey, lineNum int, expect ssh.PublicKey) error {
	if lineNum == 0 {
		newLine := knownhosts.Line([]string{knownhosts.Normalize(hostport)}, liveKey)
		return appendLine(path, newLine)
	}
	return rewriteKnownHostsLine(path, lineNum, liveKey, expect)
}

// rewriteKnownHostsLine replaces the key portion of the 1-indexed lineNum in
// path with liveKey, preserving the line's existing host-pattern field —
// hostname/IP alias list or a hashed "|1|salt|hash" pattern — rather than
// regenerating it from a hostport, which would silently drop aliases or
// de-hash a hashed known_hosts file.
//
// It refuses to rewrite a line whose current key does not match expect: the
// probe that chose this line and this write are two separate reads of the
// file, and a mismatch means something changed the line in between (a
// concurrent ssh_accept_host_key call, a manual ssh-keygen -R) — writing
// anyway could land liveKey on an entry for a different, unrelated key.
// It also refuses "@cert-authority"/"@revoked" marker lines, whose semantics
// a plain key-portion rewrite would silently change.
func rewriteKnownHostsLine(path string, lineNum int, liveKey, expect ssh.PublicKey) error {
	return writeLineAt(path, lineNum, func(oldLine string) (string, error) {
		fields := strings.Fields(oldLine)
		if len(fields) < 3 {
			return "", fmt.Errorf("known_hosts %q line %d: malformed entry", path, lineNum)
		}
		if strings.HasPrefix(fields[0], "@") {
			return "", fmt.Errorf("known_hosts %q line %d: refusing to rewrite a %q marker line", path, lineNum, fields[0])
		}
		parsed, _, _, _, err := ssh.ParseAuthorizedKey([]byte(fields[1] + " " + fields[2]))
		if err != nil {
			return "", fmt.Errorf("known_hosts %q line %d: parsing existing key: %w", path, lineNum, err)
		}
		if expect == nil || !bytes.Equal(parsed.Marshal(), expect.Marshal()) {
			return "", fmt.Errorf("known_hosts %q line %d changed since it was read; refusing to overwrite — retry", path, lineNum)
		}
		return fields[0] + " " + liveKey.Type() + " " + base64.StdEncoding.EncodeToString(liveKey.Marshal()), nil
	})
}

// rewriteLine replaces the 1-indexed lineNum in path with newLine atomically.
// All other lines are preserved byte-for-byte.
func rewriteLine(path string, lineNum int, newLine string) error {
	return writeLineAt(path, lineNum, func(string) (string, error) {
		return newLine, nil
	})
}

// writeLineAt replaces the 1-indexed lineNum in path with the result of
// transform(oldLine), atomically. All other lines are preserved byte-for-byte.
func writeLineAt(path string, lineNum int, transform func(oldLine string) (string, error)) error {
	data, err := os.ReadFile(path) // #nosec G304 — operator-controlled known_hosts path
	if err != nil {
		return fmt.Errorf("reading %q: %w", path, err)
	}
	lines := strings.Split(string(data), "\n")
	if lineNum < 1 || lineNum > len(lines) {
		return fmt.Errorf("line %d out of range (file has %d lines)", lineNum, len(lines))
	}
	newLine, err := transform(lines[lineNum-1])
	if err != nil {
		return err
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
