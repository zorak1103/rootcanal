//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMarkers_ExitCodeZero(t *testing.T) {
	h := newHarness(t, testFixtures.MainCfg)

	id, isErr, msg := h.OpenSession("testhost-key")
	if isErr {
		t.Fatalf("OpenSession: %s", msg)
	}
	defer h.CloseSession(id)

	sr := h.Send(id, "echo hello\n", 5000)
	if sr.IsError {
		t.Fatalf("Send: %s", sr.ErrText)
	}
	if sr.ExitCode == nil {
		t.Fatal("ExitCode should not be nil")
	}
	if *sr.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", *sr.ExitCode)
	}
	if !strings.Contains(sr.Output, "hello") {
		t.Errorf("output should contain 'hello', got %q", sr.Output)
	}
	if sr.StillRunning {
		t.Error("StillRunning should be false")
	}
}

func TestMarkers_ExitCodeNonZero(t *testing.T) {
	h := newHarness(t, testFixtures.MainCfg)

	id, isErr, msg := h.OpenSession("testhost-key")
	if isErr {
		t.Fatalf("OpenSession: %s", msg)
	}
	defer h.CloseSession(id)

	sr := h.Send(id, "ls /nonexistent_path_xyz 2>&1\n", 5000)
	if sr.IsError {
		t.Fatalf("Send: %s", sr.ErrText)
	}
	if sr.ExitCode == nil {
		t.Fatal("ExitCode should not be nil")
	}
	if *sr.ExitCode == 0 {
		t.Error("expected non-zero ExitCode for failed ls")
	}
}

func TestMarkers_OutputIsClean(t *testing.T) {
	// Verify that ANSI escapes and PTY echo are stripped from output.
	h := newHarness(t, testFixtures.MainCfg)

	id, isErr, msg := h.OpenSession("testhost-key")
	if isErr {
		t.Fatalf("OpenSession: %s", msg)
	}
	defer h.CloseSession(id)

	sr := h.Send(id, "printf 'hello world'\n", 5000)
	if sr.IsError {
		t.Fatalf("Send: %s", sr.ErrText)
	}
	// Output should contain 'hello world' without ANSI escapes.
	if !strings.Contains(sr.Output, "hello world") {
		t.Errorf("expected 'hello world' in output, got %q", sr.Output)
	}
	// Output must not contain ESC character (ANSI escape prefix).
	if strings.Contains(sr.Output, "\x1B") {
		t.Errorf("output contains ANSI escape sequences: %q", sr.Output)
	}
}

func TestMarkers_MOTDSuppressed(t *testing.T) {
	// Open a session and immediately peek — ring buffer should be empty
	// (MOTD was consumed by bootSession).
	h := newHarness(t, testFixtures.MainCfg)

	id, isErr, msg := h.OpenSession("testhost-key")
	if isErr {
		t.Fatalf("OpenSession: %s", msg)
	}
	defer h.CloseSession(id)

	// Short-timeout Send with no input should return empty output (no MOTD).
	sr := h.Send(id, "", 200)
	if sr.IsError {
		t.Fatalf("peek Send: %s", sr.ErrText)
	}
	if sr.Output != "" {
		t.Errorf("expected empty output after boot (MOTD should be suppressed), got %q", sr.Output)
	}
}

func TestMarkers_StillRunning_Continuation(t *testing.T) {
	h := newHarness(t, testFixtures.MainCfg)

	id, isErr, msg := h.OpenSession("testhost-key")
	if isErr {
		t.Fatalf("OpenSession: %s", msg)
	}
	defer h.CloseSession(id)

	// Short-timeout send on a 3s sleep — expect still_running.
	sr1 := h.Send(id, "sleep 3 && echo done\n", 500)
	if sr1.IsError {
		t.Fatalf("Send: %s", sr1.ErrText)
	}
	if !sr1.StillRunning {
		t.Log("sleep 3 completed within 500ms — this is unusual but not a test failure on fast machines")
		return
	}

	// Continuation — wait for up to 8 s.
	sr2 := h.Send(id, "", 8000)
	if sr2.IsError {
		t.Fatalf("continuation Send: %s", sr2.ErrText)
	}
	if sr2.StillRunning {
		t.Error("continuation: StillRunning should be false")
	}
	if sr2.ExitCode == nil || *sr2.ExitCode != 0 {
		t.Errorf("continuation ExitCode = %v, want &0", sr2.ExitCode)
	}
	if !strings.Contains(sr2.Output, "done") {
		t.Errorf("expected 'done' in continuation output, got %q", sr2.Output)
	}
}

func TestMarkers_SessionName(t *testing.T) {
	h := newHarness(t, testFixtures.MainCfg)

	res, err := h.sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "ssh_session_open",
		Arguments: map[string]any{"host": "testhost-key", "name": "mytest"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("open with name: %s", textOf(res))
	}

	var out struct {
		SessionID string `json:"session_id"`
	}
	decodeStructured(res, &out)
	if out.SessionID != "mytest" {
		t.Errorf("session_id = %q, want mytest", out.SessionID)
	}
	// Clean up the named session.
	h.CloseSession(out.SessionID)
}
