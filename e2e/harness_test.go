//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Harness wraps a rootcanal subprocess and a connected MCP client session.
// Create one per test with newHarness; it is torn down automatically via t.Cleanup.
type Harness struct {
	t      *testing.T
	sess   *mcp.ClientSession
	stderr *bytes.Buffer

	mu   sync.Mutex
	logs []string // notifications/message log lines received from the server
}

// SendResult holds the decoded result of ssh_session_send.
type SendResult struct {
	Output    string
	Truncated bool
	Closed    bool
	IsError   bool
	ErrText   string
}

// ReadResult holds the decoded result of sftp_read.
type ReadResult struct {
	Content string
	Binary  bool
	Size    int
	IsError bool
	ErrText string
}

// WriteResult holds the decoded result of sftp_write.
type WriteResult struct {
	Text    string
	IsError bool
	ErrText string
}

// ListEntry mirrors the entrySummary from mcpserver/tools_sftp.go.
type ListEntry struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	Mode    string `json:"mode"`
	ModTime string `json:"mod_time"`
	IsDir   bool   `json:"is_dir"`
}

// ListResult holds the decoded result of sftp_list.
type ListResult struct {
	Path    string
	Entries []ListEntry
	IsError bool
	ErrText string
}

// newHarness spawns rootcanal and connects an MCP client. cfgPath selects the
// config file; extraEnv optionally sets additional env vars (e.g. "KEY=VAL").
func newHarness(t *testing.T, cfgPath string, extraEnv ...string) *Harness {
	t.Helper()

	h := &Harness{t: t, stderr: &bytes.Buffer{}}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	cmd := exec.CommandContext(ctx, testFixtures.BinPath, "-config", cfgPath)
	cmd.Env = append(os.Environ(), extraEnv...)
	cmd.Stderr = h.stderr

	client := mcp.NewClient(&mcp.Implementation{Name: "e2e-test", Version: "v0.0.1"}, &mcp.ClientOptions{
		LoggingMessageHandler: func(_ context.Context, r *mcp.LoggingMessageRequest) {
			h.mu.Lock()
			h.logs = append(h.logs, fmt.Sprint(r.Params.Data))
			h.mu.Unlock()
		},
	})

	transport := &mcp.CommandTransport{Command: cmd}
	sess, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("harness: connect MCP: %v", err)
	}
	h.sess = sess

	t.Cleanup(func() {
		_ = sess.Close()
		// Give the process up to 3 s to exit; if not, the ctx cancel kills it.
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	})

	return h
}

// Stderr returns the stderr output captured from the rootcanal process.
func (h *Harness) Stderr() string { return h.stderr.String() }

// Logs returns the MCP notifications/message log lines received from the server.
func (h *Harness) Logs() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.logs...)
}

// ---- tool helpers ----

func (h *Harness) OpenSession(host string) (id string, isErr bool, text string) {
	h.t.Helper()
	res, err := h.sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "ssh_session_open",
		Arguments: map[string]any{"host": host},
	})
	if err != nil {
		h.t.Fatalf("OpenSession protocol error: %v", err)
	}
	if res.IsError {
		return "", true, textOf(res)
	}
	var out struct {
		SessionID string `json:"session_id"`
	}
	decodeStructured(res, &out)
	return out.SessionID, false, textOf(res)
}

func (h *Harness) Send(id, input string, timeoutMs int) SendResult {
	h.t.Helper()
	res, err := h.sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "ssh_session_send",
		Arguments: map[string]any{
			"session_id": id,
			"input":      input,
			"timeout_ms": timeoutMs,
		},
	})
	if err != nil {
		h.t.Fatalf("Send protocol error: %v", err)
	}
	if res.IsError {
		return SendResult{IsError: true, ErrText: textOf(res)}
	}
	var out struct {
		Output    string `json:"output"`
		Truncated bool   `json:"truncated"`
		Closed    bool   `json:"closed"`
	}
	decodeStructured(res, &out)
	return SendResult{Output: out.Output, Truncated: out.Truncated, Closed: out.Closed}
}

func (h *Harness) CloseSession(id string) {
	h.t.Helper()
	res, err := h.sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "ssh_session_close",
		Arguments: map[string]any{"session_id": id},
	})
	if err != nil {
		h.t.Fatalf("CloseSession protocol error: %v", err)
	}
	if res.IsError {
		h.t.Errorf("CloseSession error: %s", textOf(res))
	}
}

func (h *Harness) ListSessions() []struct {
	SessionID string `json:"session_id"`
	Host      string `json:"host"`
} {
	h.t.Helper()
	res, err := h.sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "ssh_session_list",
		Arguments: map[string]any{},
	})
	if err != nil {
		h.t.Fatalf("ListSessions protocol error: %v", err)
	}
	var out struct {
		Sessions []struct {
			SessionID string `json:"session_id"`
			Host      string `json:"host"`
		} `json:"sessions"`
	}
	decodeStructured(res, &out)
	return out.Sessions
}

func (h *Harness) SFTPRead(host, path string, maxBytes int) ReadResult {
	h.t.Helper()
	args := map[string]any{"host": host, "path": path}
	if maxBytes > 0 {
		args["max_bytes"] = maxBytes
	}
	res, err := h.sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "sftp_read",
		Arguments: args,
	})
	if err != nil {
		h.t.Fatalf("SFTPRead protocol error: %v", err)
	}
	if res.IsError {
		return ReadResult{IsError: true, ErrText: textOf(res)}
	}
	var out struct {
		Content string `json:"content"`
		Binary  bool   `json:"binary"`
		Size    int    `json:"size"`
	}
	decodeStructured(res, &out)
	return ReadResult{Content: out.Content, Binary: out.Binary, Size: out.Size}
}

func (h *Harness) SFTPWrite(host, path, content string, binary bool, mode string) WriteResult {
	h.t.Helper()
	args := map[string]any{
		"host":    host,
		"path":    path,
		"content": content,
		"binary":  binary,
	}
	if mode != "" {
		args["mode"] = mode
	}
	res, err := h.sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "sftp_write",
		Arguments: args,
	})
	if err != nil {
		h.t.Fatalf("SFTPWrite protocol error: %v", err)
	}
	if res.IsError {
		return WriteResult{IsError: true, ErrText: textOf(res)}
	}
	return WriteResult{Text: textOf(res)}
}

func (h *Harness) SFTPList(host, path string) ListResult {
	h.t.Helper()
	res, err := h.sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "sftp_list",
		Arguments: map[string]any{"host": host, "path": path},
	})
	if err != nil {
		h.t.Fatalf("SFTPList protocol error: %v", err)
	}
	if res.IsError {
		return ListResult{IsError: true, ErrText: textOf(res)}
	}
	var out struct {
		Path    string      `json:"path"`
		Entries []ListEntry `json:"entries"`
	}
	decodeStructured(res, &out)
	return ListResult{Path: out.Path, Entries: out.Entries}
}

// ---- assertion helpers ----

// RequireToolError fatally fails if the result is not an error or doesn't contain wantSubstr.
func (h *Harness) RequireToolError(isErr bool, text, wantSubstr string) {
	h.t.Helper()
	if !isErr {
		h.t.Fatalf("expected tool error containing %q, got success: %s", wantSubstr, text)
	}
	if !strings.Contains(text, wantSubstr) {
		h.t.Errorf("tool error %q does not contain %q", text, wantSubstr)
	}
}

// ---- internal helpers ----

func textOf(res *mcp.CallToolResult) string {
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			return t.Text
		}
	}
	return ""
}

func decodeStructured(res *mcp.CallToolResult, out any) {
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil || string(raw) == "null" {
		return
	}
	_ = json.Unmarshal(raw, out)
}
