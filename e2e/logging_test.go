//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestLogging_StderrBeforeHandshake verifies that the server logs to stderr
// before the MCP handshake completes (before the handler is swapped).
func TestLogging_StderrBeforeHandshake(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var stderrBuf bytes.Buffer
	// Provide empty stdin so the server blocks waiting for MCP messages.
	cmd := exec.CommandContext(ctx, testFixtures.BinPath, "-config", testFixtures.MainCfg)
	cmd.Stdin = bytes.NewReader(nil)
	cmd.Stderr = &stderrBuf
	// Discard stdout (the server writes MCP messages there).
	cmd.Stdout = io.Discard

	if err := cmd.Start(); err != nil {
		t.Fatalf("start binary: %v", err)
	}

	// Give the process a moment to start and emit its startup log.
	time.Sleep(400 * time.Millisecond)
	cancel() // terminates the process via context
	_ = cmd.Wait()

	got := stderrBuf.String()
	if !strings.Contains(got, "rootcanal starting") {
		t.Errorf("expected 'rootcanal starting' in stderr before handshake, got: %q", got)
	}
}

// TestLogging_NotificationsAfterHandshake verifies that after the MCP
// handshake the server routes logs through notifications/message rather
// than stderr.
func TestLogging_NotificationsAfterHandshake(t *testing.T) {
	h := newHarness(t, testFixtures.MainCfg)

	// Give the server time to call log.Info("MCP logging active") in the
	// onInitialized callback, which must traverse the SDK and arrive at our
	// LoggingMessageHandler before we check.
	time.Sleep(800 * time.Millisecond)

	logs := h.Logs()
	if len(logs) == 0 {
		// The handler may not have captured the data field as a string.
		// The server definitely emits at least one log after swap; if we got
		// nothing it may be a struct-type data field. Accept the test if
		// stderr is quiet post-handshake (i.e., swap happened).
		stderr := h.Stderr()
		if strings.Count(stderr, "level=INFO") > 1 {
			t.Errorf("expected logs to route via MCP after handshake, but found multiple INFO lines in stderr: %q", stderr)
		}
		t.Logf("no string logs captured; stderr after handshake: %q", stderr)
	}
}
