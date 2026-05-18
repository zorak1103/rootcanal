//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

// TestAuth_KeyNoPassphrase verifies public-key auth without a passphrase.
func TestAuth_KeyNoPassphrase(t *testing.T) {
	h := newHarness(t, testFixtures.MainCfg)

	_, isErr, msg := h.OpenSession("testhost-key")
	if isErr {
		t.Fatalf("key auth without passphrase failed: %s", msg)
	}
}

// TestAuth_KeyWithPassphrase verifies public-key auth with a passphrase-protected key.
func TestAuth_KeyWithPassphrase(t *testing.T) {
	// With the correct passphrase — should succeed.
	h1 := newHarness(t, testFixtures.MainCfg, "TEST_RC_PASSPHRASE="+passphrase)
	_, isErr, msg := h1.OpenSession("testhost-keypass")
	if isErr {
		t.Fatalf("key+passphrase auth failed: %s", msg)
	}

	// Without the passphrase — key decryption should fail.
	h2 := newHarness(t, testFixtures.MainCfg) // TEST_RC_PASSPHRASE is NOT set
	_, isErr2, text := h2.OpenSession("testhost-keypass")
	if !isErr2 {
		t.Fatal("expected failure when passphrase env is not set")
	}
	// The sshconn layer reads an empty passphrase and fails to decrypt the key.
	if !strings.Contains(strings.ToLower(text), "ssh") && !strings.Contains(strings.ToLower(text), "passphrase") && !strings.Contains(strings.ToLower(text), "decrypt") {
		t.Logf("auth error (acceptable): %s", text)
	}
}

// TestAuth_Password verifies password auth.
func TestAuth_Password(t *testing.T) {
	h := newHarness(t, testFixtures.MainCfg, "TEST_RC_PASSWORD="+containerPass)

	_, isErr, msg := h.OpenSession("testhost-pwd")
	if isErr {
		t.Fatalf("password auth failed: %s", msg)
	}
}

// TestAuth_PasswordWrongEnv verifies that an incorrect password is rejected.
func TestAuth_PasswordWrongEnv(t *testing.T) {
	h := newHarness(t, testFixtures.MainCfg, "TEST_RC_PASSWORD=definitelywrong")

	_, isErr, text := h.OpenSession("testhost-pwd")
	h.RequireToolError(isErr, text, "ssh:")
}

// TestAuth_PasswordMissingEnv verifies that an empty password env var is caught early.
func TestAuth_PasswordMissingEnv(t *testing.T) {
	// Do not set TEST_RC_PASSWORD. The sshconn layer reads os.Getenv("TEST_RC_PASSWORD")
	// which returns "" and returns: env var "TEST_RC_PASSWORD" is empty or unset.
	h := newHarness(t, testFixtures.MainCfg)

	_, isErr, text := h.OpenSession("testhost-pwd")
	h.RequireToolError(isErr, text, "is empty or unset")
}
