//go:build e2e

package e2e

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runBinary executes the rootcanal binary with the given arguments and returns
// the combined stdout+stderr and the exit error (nil on exit 0).
func runBinary(args ...string) (string, error) {
	out, err := exec.Command(testFixtures.BinPath, args...).CombinedOutput()
	return string(out), err
}

func TestCLI_Version(t *testing.T) {
	out, err := runBinary("-version")
	if err != nil {
		t.Fatalf("-version exited non-zero: %v\n%s", err, out)
	}
	if !strings.Contains(out, "rootcanal") {
		t.Errorf("expected 'rootcanal' in version output, got: %q", out)
	}
}

func TestCLI_ValidateConfig_OK(t *testing.T) {
	out, err := runBinary("-validate-config", "-config", testFixtures.MainCfg)
	if err != nil {
		t.Fatalf("-validate-config failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "OK:") {
		t.Errorf("expected 'OK:' in output, got: %q", out)
	}
}

func TestCLI_ValidateConfig_BadKnownHosts(t *testing.T) {
	// Write a minimal config pointing to a nonexistent known_hosts file.
	cfg := writeBadKnownHostsConfig(t)

	out, err := runBinary("-validate-config", "-config", cfg)
	if err == nil {
		t.Fatalf("expected non-zero exit for bad config, got: %q", out)
	}
	if !strings.Contains(out, "config error") {
		t.Errorf("expected 'config error' in output, got: %q", out)
	}
}

func TestCLI_Probe_OK(t *testing.T) {
	out, err := runBinary("-probe", "testhost-key", "-config", testFixtures.MainCfg)
	if err != nil {
		t.Fatalf("-probe testhost-key failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "OK:") {
		t.Errorf("expected 'OK:' in probe output, got: %q", out)
	}
}

func TestCLI_Probe_UnknownHost(t *testing.T) {
	out, err := runBinary("-probe", "ghost-host-does-not-exist", "-config", testFixtures.MainCfg)
	if err == nil {
		t.Fatalf("expected non-zero exit for unknown host, got: %q", out)
	}
	if !strings.Contains(out, "not found") {
		t.Errorf("expected 'not found' in output, got: %q", out)
	}
}

func TestCLI_Probe_DialFails(t *testing.T) {
	// Write a config with a host that points at a port that refuses connections.
	cfg := writeUnreachableHostConfig(t)

	out, err := runBinary("-probe", "unreachable", "-config", cfg)
	if err == nil {
		t.Fatalf("expected non-zero exit for unreachable host, got: %q", out)
	}
	// The error message should mention a dial/connection failure.
	if !strings.Contains(out, "probe") {
		t.Errorf("expected 'probe' in output, got: %q", out)
	}
}

// writeBadKnownHostsConfig generates a minimal config YAML with a nonexistent
// known_hosts path and returns the config file path.
func writeBadKnownHostsConfig(t *testing.T) string {
	t.Helper()
	content := fmt.Sprintf(`hosts:
  test:
    address: "127.0.0.1:22"
    user: testuser
    auth:
      type: key
      key_path: "%s"
    known_hosts: "/nonexistent/path/known_hosts_does_not_exist"
`, toSlash(testFixtures.KeyPath))
	path := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write bad config: %v", err)
	}
	return path
}

// writeUnreachableHostConfig generates a config where the host address uses
// port 1, which should be refused immediately.
func writeUnreachableHostConfig(t *testing.T) string {
	t.Helper()

	// Build a minimal known_hosts file so config validation passes.
	var buf bytes.Buffer
	buf.WriteString("# placeholder\n")
	khPath := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(khPath, buf.Bytes(), 0600); err != nil {
		t.Fatalf("write known_hosts: %v", err)
	}

	content := fmt.Sprintf(`hosts:
  unreachable:
    address: "127.0.0.1:1"
    user: testuser
    auth:
      type: key
      key_path: "%s"
    known_hosts: "%s"
`, toSlash(testFixtures.KeyPath), toSlash(khPath))
	path := filepath.Join(t.TempDir(), "unreachable.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write unreachable config: %v", err)
	}
	return path
}
