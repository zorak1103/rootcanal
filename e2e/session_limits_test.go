//go:build e2e

package e2e

import (
	"testing"
	"time"
)

// TestLimits_MaxSessionsTotal verifies that the global session cap is enforced.
// Uses LimitsCfg (max_sessions_total=2, max_sessions_per_host=1).
func TestLimits_MaxSessionsTotal(t *testing.T) {
	h := newHarness(t, testFixtures.LimitsCfg)

	id1, isErr, msg := h.OpenSession("testhost-key")
	if isErr {
		t.Fatalf("open session 1 failed: %s", msg)
	}
	defer h.CloseSession(id1)

	// Second session on a different host (testhost-sftp) to avoid per-host limit.
	id2, isErr, msg := h.OpenSession("testhost-sftp")
	if isErr {
		t.Fatalf("open session 2 failed: %s", msg)
	}
	defer h.CloseSession(id2)

	// Third open should hit the global limit.
	_, isErr, text := h.OpenSession("testhost-pwd")
	h.RequireToolError(isErr, text, "global session limit")
}

// TestLimits_MaxSessionsPerHost verifies the per-host session cap.
// Uses LimitsCfg (max_sessions_total=2, max_sessions_per_host=1).
func TestLimits_MaxSessionsPerHost(t *testing.T) {
	h := newHarness(t, testFixtures.LimitsCfg)

	id1, isErr, msg := h.OpenSession("testhost-key")
	if isErr {
		t.Fatalf("open session failed: %s", msg)
	}
	defer h.CloseSession(id1)

	// Second open on the same host must hit per-host limit (global still has room
	// since max_sessions_total=2 and global=1).
	_, isErr, text := h.OpenSession("testhost-key")
	h.RequireToolError(isErr, text, "per-host session limit")
}

// TestLimits_OutputTruncation verifies the ringbuf overflow flag.
// Uses LimitsCfg (output_buffer_bytes=4096).
func TestLimits_OutputTruncation(t *testing.T) {
	h := newHarness(t, testFixtures.LimitsCfg)

	id, isErr, msg := h.OpenSession("testhost-key")
	if isErr {
		t.Fatalf("open session failed: %s", msg)
	}
	defer h.CloseSession(id)
	h.Send(id, "\n", 1000) // drain prompt

	// head -c 6000 /dev/zero | base64 produces >8000 bytes of output, exceeding
	// the 4096-byte output_buffer_bytes limit in LimitsCfg.
	sr := h.Send(id, "head -c 6000 /dev/zero | base64\n", 5000)
	if sr.IsError {
		t.Fatalf("Send failed: %s", sr.ErrText)
	}
	if !sr.Truncated {
		t.Error("expected Truncated=true for output exceeding buffer size")
	}
	if len(sr.Output) > 4096+512 { // small overshoot tolerance for UTF-8 boundaries
		t.Errorf("expected output ≤4096 bytes but got %d bytes", len(sr.Output))
	}
}

// TestLimits_SendTimeoutClamp verifies that a short timeout_ms causes an early
// return rather than blocking for the full command duration.
func TestLimits_SendTimeoutClamp(t *testing.T) {
	h := newHarness(t, testFixtures.MainCfg)

	id, isErr, msg := h.OpenSession("testhost-key")
	if isErr {
		t.Fatalf("open session failed: %s", msg)
	}
	defer h.CloseSession(id)
	h.Send(id, "\n", 1000) // drain prompt

	start := time.Now()
	// sleep 10s — with timeout_ms=150 the server must return well before 1s.
	sr := h.Send(id, "sleep 10\n", 150)
	elapsed := time.Since(start)

	if sr.IsError {
		t.Fatalf("Send failed: %s", sr.ErrText)
	}
	if sr.Closed {
		t.Error("expected Closed=false while sleep is still running")
	}
	if elapsed > 1500*time.Millisecond {
		t.Errorf("expected early return within 1.5s, took %v", elapsed)
	}
}

// TestLimits_UnknownHost verifies that opening a session for an unknown host
// returns a tool error instead of panicking or hanging.
func TestLimits_UnknownHost(t *testing.T) {
	h := newHarness(t, testFixtures.MainCfg)

	_, isErr, text := h.OpenSession("nope-this-host-does-not-exist")
	h.RequireToolError(isErr, text, "unknown host")
}
