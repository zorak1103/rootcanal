//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

// TestKnownHosts_GoldenPin verifies that a session opens when the correct
// host key is pinned in known_hosts.
func TestKnownHosts_GoldenPin(t *testing.T) {
	h := newHarness(t, testFixtures.MainCfg)

	_, isErr, msg := h.OpenSession("testhost-key")
	if isErr {
		t.Fatalf("expected success with correct host key, got: %s", msg)
	}
}

// TestKnownHosts_Mismatch verifies that a host-key mismatch is caught and
// propagated as a tool error.
func TestKnownHosts_Mismatch(t *testing.T) {
	h := newHarness(t, testFixtures.MainCfg)

	_, isErr, text := h.OpenSession("testhost-bad-hostkey")
	if !isErr {
		t.Fatal("expected tool error for host-key mismatch")
	}
	// The error comes from golang.org/x/crypto/ssh/knownhosts via the SSH handshake.
	// It will contain some indication of a host-key or verification failure.
	if !strings.Contains(text, "ssh:") && !strings.Contains(text, "knownhosts") && !strings.Contains(text, "host key") {
		t.Errorf("expected SSH/knownhosts error, got: %q", text)
	}
}

// TestKnownHosts_MismatchDoesNotAffectOtherHosts verifies that a host-key
// mismatch on one host does not prevent other sessions from opening.
func TestKnownHosts_MismatchDoesNotAffectOtherHosts(t *testing.T) {
	h := newHarness(t, testFixtures.MainCfg)

	// Bad host key on one host...
	_, isErr, _ := h.OpenSession("testhost-bad-hostkey")
	if !isErr {
		t.Fatal("expected error for bad host key")
	}

	// ...should not poison the session manager for other hosts.
	_, isErr2, msg := h.OpenSession("testhost-key")
	if isErr2 {
		t.Fatalf("expected success on good host after bad-key failure, got: %s", msg)
	}
}
