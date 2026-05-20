//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

func TestRunOnce_Success(t *testing.T) {
	h := newHarness(t, testFixtures.MainCfg)

	r := h.RunOnce("testhost-key", "echo marker123")
	if r.IsError {
		t.Fatalf("RunOnce: %s", r.ErrText)
	}
	if r.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "marker123") {
		t.Errorf("Stdout = %q, want 'marker123'", r.Stdout)
	}
	if r.Stderr != "" {
		t.Errorf("Stderr = %q, want empty", r.Stderr)
	}
}

func TestRunOnce_NonZeroExit(t *testing.T) {
	h := newHarness(t, testFixtures.MainCfg)

	r := h.RunOnce("testhost-key", "ls /no/such/path/xyz")
	if r.IsError {
		t.Fatalf("RunOnce protocol error: %s", r.ErrText)
	}
	if r.ExitCode == 0 {
		t.Error("expected non-zero ExitCode for failed ls")
	}
}

func TestRunOnce_Stdin(t *testing.T) {
	h := newHarness(t, testFixtures.MainCfg)

	r := h.RunOnce("testhost-key", "cat", map[string]any{"stdin": "hello from stdin\n"})
	if r.IsError {
		t.Fatalf("RunOnce: %s", r.ErrText)
	}
	if !strings.Contains(r.Stdout, "hello from stdin") {
		t.Errorf("Stdout = %q, want 'hello from stdin'", r.Stdout)
	}
}

func TestRunOnce_NoMOTD(t *testing.T) {
	// Exec channel should produce no MOTD or shell init output.
	h := newHarness(t, testFixtures.MainCfg)

	r := h.RunOnce("testhost-key", "echo exactly-this")
	if r.IsError {
		t.Fatalf("RunOnce: %s", r.ErrText)
	}
	if r.Stdout != "exactly-this\n" {
		t.Errorf("Stdout = %q, want %q", r.Stdout, "exactly-this\n")
	}
}

func TestRunOnce_SeparateStreams(t *testing.T) {
	// stdout and stderr are returned separately.
	h := newHarness(t, testFixtures.MainCfg)

	r := h.RunOnce("testhost-key", "echo out; echo err >&2")
	if r.IsError {
		t.Fatalf("RunOnce: %s", r.ErrText)
	}
	if !strings.Contains(r.Stdout, "out") {
		t.Errorf("Stdout missing 'out': %q", r.Stdout)
	}
	if !strings.Contains(r.Stderr, "err") {
		t.Errorf("Stderr missing 'err': %q", r.Stderr)
	}
}

func TestRunOnce_UnknownHost(t *testing.T) {
	h := newHarness(t, testFixtures.MainCfg)

	r := h.RunOnce("no-such-host", "echo hi")
	if !r.IsError {
		t.Error("expected error for unknown host")
	}
}
