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

// TestLogging_StderrBeforeHandshake verifies that startup logs go to stderr
// before an MCP session is established.
func TestLogging_StderrBeforeHandshake(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stderrBuf := &synchronizedBuffer{}
	// Provide empty stdin so the server blocks waiting for MCP messages.
	cmd := exec.CommandContext(ctx, testFixtures.BinPath, "-config", testFixtures.MainCfg)
	cmd.Stdin = bytes.NewReader(nil)
	cmd.Stderr = stderrBuf
	// Discard stdout (the server writes MCP messages there).
	cmd.Stdout = io.Discard

	if err := cmd.Start(); err != nil {
		t.Fatalf("start binary: %v", err)
	}

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if strings.Contains(stderrBuf.String(), "rootcanal starting") {
			break
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			cancel()
			_ = cmd.Wait()
			t.Fatalf("timed out waiting for startup log, got: %q", stderrBuf.String())
		}
	}

	cancel()
	_ = cmd.Wait()
}

// TestLogging_StderrAfterHandshake verifies that the server keeps logging to
// stderr after the MCP session is established.
func TestLogging_StderrAfterHandshake(t *testing.T) {
	h := newHarness(t, testFixtures.MainCfg)

	// Closing the connected session causes the post-handshake shutdown log. It
	// must remain on stderr rather than being sent as a deprecated MCP log
	// notification.
	if err := h.sess.Close(); err != nil {
		t.Fatalf("close MCP session: %v", err)
	}

	if logs := h.Logs(); len(logs) != 0 {
		t.Errorf("expected no MCP log notifications, got %v", logs)
	}
	if stderr := h.Stderr(); !strings.Contains(stderr, "shutting down") {
		t.Errorf("expected post-handshake shutdown log in stderr, got: %q", stderr)
	}
}
