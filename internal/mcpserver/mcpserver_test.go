package mcpserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gitlab.com/zorak1103/rootcanal/internal/mcpserver"
	"gitlab.com/zorak1103/rootcanal/internal/session"
	"gitlab.com/zorak1103/rootcanal/internal/sftpops"
)

// ---- fake Manager ----

type fakeManager struct {
	openFn  func(ctx context.Context, host string) (string, error)
	sendFn  func(ctx context.Context, id string, input []byte, timeout time.Duration) ([]byte, bool, bool, error)
	closeFn func(ctx context.Context, id string) error
	listFn  func() []session.SessionInfo
}

func (f *fakeManager) Open(ctx context.Context, host string) (string, error) {
	return f.openFn(ctx, host)
}
func (f *fakeManager) Send(ctx context.Context, id string, input []byte, timeout time.Duration) ([]byte, bool, bool, error) {
	return f.sendFn(ctx, id, input, timeout)
}
func (f *fakeManager) Close(ctx context.Context, id string) error {
	return f.closeFn(ctx, id)
}
func (f *fakeManager) List() []session.SessionInfo { return f.listFn() }
func (f *fakeManager) Shutdown(_ context.Context) error { return nil }

// ---- fake Ops ----

type fakeOps struct {
	readFn  func(ctx context.Context, host, path string, maxBytes int) ([]byte, bool, error)
	writeFn func(ctx context.Context, host, path string, content []byte, mode fs.FileMode) error
	listFn  func(ctx context.Context, host, path string) ([]sftpops.Entry, error)
}

func (f *fakeOps) Read(ctx context.Context, host, path string, maxBytes int) ([]byte, bool, error) {
	return f.readFn(ctx, host, path, maxBytes)
}
func (f *fakeOps) Write(ctx context.Context, host, path string, content []byte, mode fs.FileMode) error {
	return f.writeFn(ctx, host, path, content, mode)
}
func (f *fakeOps) List(ctx context.Context, host, path string) ([]sftpops.Entry, error) {
	return f.listFn(ctx, host, path)
}

// ---- test helpers ----

func newTestClient(t *testing.T, mgr session.Manager, ops sftpops.Ops) *mcp.ClientSession {
	t.Helper()
	srv := mcpserver.New(mgr, ops, nil)
	t1, t2 := mcp.NewInMemoryTransports()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	go func() { _ = srv.Run(ctx, t1) }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	sess, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() { sess.Close() })
	return sess
}

// ---- tests ----

func TestToolsList(t *testing.T) {
	mgr := &fakeManager{
		listFn: func() []session.SessionInfo { return nil },
	}
	sess := newTestClient(t, mgr, nil)

	result, err := sess.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	names := make(map[string]bool)
	for _, tool := range result.Tools {
		names[tool.Name] = true
	}
	for _, expected := range []string{
		"ssh_session_open", "ssh_session_send",
		"ssh_session_close", "ssh_session_list",
	} {
		if !names[expected] {
			t.Errorf("missing tool %q", expected)
		}
	}
}

func TestTool_SessionOpen_Success(t *testing.T) {
	mgr := &fakeManager{
		openFn: func(_ context.Context, host string) (string, error) {
			return "s_TEST01", nil
		},
	}
	sess := newTestClient(t, mgr, nil)

	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "ssh_session_open",
		Arguments: map[string]any{"host": "my-host"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Errorf("unexpected tool error: %+v", res.Content)
	}
	// Structured output should contain session_id.
	raw, _ := json.Marshal(res.StructuredContent)
	if string(raw) == "" || !containsStr(raw, "s_TEST01") {
		t.Errorf("structured output missing session_id: %s", raw)
	}
}

func TestTool_SessionOpen_Error(t *testing.T) {
	mgr := &fakeManager{
		openFn: func(_ context.Context, host string) (string, error) {
			return "", context.DeadlineExceeded
		},
	}
	sess := newTestClient(t, mgr, nil)

	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "ssh_session_open",
		Arguments: map[string]any{"host": "bad-host"},
	})
	if err != nil {
		t.Fatalf("CallTool protocol error: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError=true for failed open")
	}
}

func TestTool_SessionSend(t *testing.T) {
	mgr := &fakeManager{
		sendFn: func(_ context.Context, id string, input []byte, _ time.Duration) ([]byte, bool, bool, error) {
			return []byte("$ " + string(input)), false, false, nil
		},
	}
	sess := newTestClient(t, mgr, nil)

	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "ssh_session_send",
		Arguments: map[string]any{"session_id": "s_TEST01", "input": "ls\n"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Errorf("unexpected tool error: %+v", res.Content)
	}
}

func TestTool_SessionClose(t *testing.T) {
	closed := false
	mgr := &fakeManager{
		closeFn: func(_ context.Context, id string) error {
			closed = true
			return nil
		},
	}
	sess := newTestClient(t, mgr, nil)

	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "ssh_session_close",
		Arguments: map[string]any{"session_id": "s_TEST01"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Errorf("unexpected error: %+v", res.Content)
	}
	if !closed {
		t.Error("expected manager.Close to be called")
	}
}

func TestTool_SessionSend_Error(t *testing.T) {
	mgr := &fakeManager{
		sendFn: func(_ context.Context, _ string, _ []byte, _ time.Duration) ([]byte, bool, bool, error) {
			return nil, false, false, errors.New("session gone")
		},
	}
	sess := newTestClient(t, mgr, nil)

	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "ssh_session_send",
		Arguments: map[string]any{"session_id": "s_DEAD", "input": "ls\n"},
	})
	if err != nil {
		t.Fatalf("protocol error: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError=true for send error")
	}
}

func TestTool_SessionClose_Error(t *testing.T) {
	mgr := &fakeManager{
		closeFn: func(_ context.Context, _ string) error {
			return errors.New("session not found")
		},
	}
	sess := newTestClient(t, mgr, nil)

	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "ssh_session_close",
		Arguments: map[string]any{"session_id": "s_GONE"},
	})
	if err != nil {
		t.Fatalf("protocol error: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError=true for close error")
	}
}

func TestTool_SessionList_Empty(t *testing.T) {
	mgr := &fakeManager{listFn: func() []session.SessionInfo { return nil }}
	sess := newTestClient(t, mgr, nil)

	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "ssh_session_list",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Errorf("unexpected error: %+v", res.Content)
	}
}

func TestTool_SessionList(t *testing.T) {
	now := time.Now()
	mgr := &fakeManager{
		listFn: func() []session.SessionInfo {
			return []session.SessionInfo{
				{ID: "s_AAA", Host: "srv1", OpenedAt: now, LastUsedAt: now},
			}
		},
	}
	sess := newTestClient(t, mgr, nil)

	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "ssh_session_list",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Errorf("unexpected error: %+v", res.Content)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	if !containsStr(raw, "s_AAA") {
		t.Errorf("expected session s_AAA in list output: %s", raw)
	}
}

// ---- SFTP tool tests ----

func TestTool_SFTPRead_Text(t *testing.T) {
	ops := &fakeOps{
		readFn: func(_ context.Context, _, _ string, _ int) ([]byte, bool, error) {
			return []byte("hello world\n"), false, nil
		},
	}
	sess := newTestClient(t, &fakeManager{}, ops)

	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "sftp_read",
		Arguments: map[string]any{"host": "h", "path": "/etc/hosts"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Errorf("unexpected error: %+v", res.Content)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	if !containsStr(raw, "hello world") {
		t.Errorf("expected content in output: %s", raw)
	}
}

func TestTool_SFTPRead_Error(t *testing.T) {
	ops := &fakeOps{
		readFn: func(_ context.Context, _, _ string, _ int) ([]byte, bool, error) {
			return nil, false, errors.New("file not found")
		},
	}
	sess := newTestClient(t, &fakeManager{}, ops)

	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "sftp_read",
		Arguments: map[string]any{"host": "h", "path": "/missing"},
	})
	if err != nil {
		t.Fatalf("CallTool protocol error: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError=true")
	}
}

func TestTool_SFTPWrite_Success(t *testing.T) {
	written := false
	ops := &fakeOps{
		writeFn: func(_ context.Context, _, _ string, _ []byte, _ fs.FileMode) error {
			written = true
			return nil
		},
	}
	sess := newTestClient(t, &fakeManager{}, ops)

	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "sftp_write",
		Arguments: map[string]any{"host": "h", "path": "/tmp/f.txt", "content": "data"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Errorf("unexpected error: %+v", res.Content)
	}
	if !written {
		t.Error("expected write to be called")
	}
}

func TestTool_SFTPWrite_InvalidMode(t *testing.T) {
	ops := &fakeOps{
		writeFn: func(_ context.Context, _, _ string, _ []byte, _ fs.FileMode) error {
			return nil
		},
	}
	sess := newTestClient(t, &fakeManager{}, ops)

	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "sftp_write",
		Arguments: map[string]any{"host": "h", "path": "/tmp/f", "content": "x", "mode": "notoctal"},
	})
	if err != nil {
		t.Fatalf("CallTool protocol error: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError=true for invalid mode")
	}
}

func TestTool_SFTPRead_Binary(t *testing.T) {
	ops := &fakeOps{
		readFn: func(_ context.Context, _, _ string, _ int) ([]byte, bool, error) {
			return []byte{0x00, 0x01, 0x02}, true, nil // binary data
		},
	}
	sess := newTestClient(t, &fakeManager{}, ops)

	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "sftp_read",
		Arguments: map[string]any{"host": "h", "path": "/bin/data"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Errorf("unexpected error: %+v", res.Content)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	if !containsStr(raw, "binary") {
		t.Errorf("expected binary field in output: %s", raw)
	}
}

func TestTool_SFTPWrite_Binary(t *testing.T) {
	var written []byte
	ops := &fakeOps{
		writeFn: func(_ context.Context, _, _ string, content []byte, _ fs.FileMode) error {
			written = append([]byte{}, content...)
			return nil
		},
	}
	sess := newTestClient(t, &fakeManager{}, ops)

	// "hello" base64-encoded = "aGVsbG8="
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "sftp_write",
		Arguments: map[string]any{
			"host": "h", "path": "/tmp/bin", "content": "aGVsbG8=", "binary": true,
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Errorf("unexpected error: %+v", res.Content)
	}
	if string(written) != "hello" {
		t.Errorf("expected decoded 'hello', got %q", written)
	}
}

func TestTool_SFTPWrite_BadBase64(t *testing.T) {
	ops := &fakeOps{
		writeFn: func(_ context.Context, _, _ string, _ []byte, _ fs.FileMode) error { return nil },
	}
	sess := newTestClient(t, &fakeManager{}, ops)

	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "sftp_write",
		Arguments: map[string]any{
			"host": "h", "path": "/tmp/f", "content": "NOT!!!VALID!!!BASE64!!!", "binary": true,
		},
	})
	if err != nil {
		t.Fatalf("protocol error: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError=true for invalid base64")
	}
}

func TestTool_SFTPList_Error(t *testing.T) {
	ops := &fakeOps{
		listFn: func(_ context.Context, _, _ string) ([]sftpops.Entry, error) {
			return nil, errors.New("permission denied")
		},
	}
	sess := newTestClient(t, &fakeManager{}, ops)

	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "sftp_list",
		Arguments: map[string]any{"host": "h", "path": "/root"},
	})
	if err != nil {
		t.Fatalf("protocol error: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError=true for list error")
	}
}

func TestTool_SFTPList_Empty(t *testing.T) {
	ops := &fakeOps{
		listFn: func(_ context.Context, _, _ string) ([]sftpops.Entry, error) {
			return []sftpops.Entry{}, nil
		},
	}
	sess := newTestClient(t, &fakeManager{}, ops)

	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "sftp_list",
		Arguments: map[string]any{"host": "h", "path": "/empty"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Errorf("unexpected error: %+v", res.Content)
	}
}

func TestTool_SFTPList_Success(t *testing.T) {
	ops := &fakeOps{
		listFn: func(_ context.Context, _, _ string) ([]sftpops.Entry, error) {
			return []sftpops.Entry{
				{Name: "readme.txt", Size: 100},
				{Name: "src", IsDir: true},
			}, nil
		},
	}
	sess := newTestClient(t, &fakeManager{}, ops)

	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "sftp_list",
		Arguments: map[string]any{"host": "h", "path": "/home/user"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Errorf("unexpected error: %+v", res.Content)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	if !containsStr(raw, "readme.txt") {
		t.Errorf("expected listing in output: %s", raw)
	}
}

func TestOnInitialized_IsCalled(t *testing.T) {
	called := make(chan struct{}, 1)
	mgr := &fakeManager{listFn: func() []session.SessionInfo { return nil }}

	srv := mcpserver.New(mgr, nil, func(_ *mcp.ServerSession) {
		called <- struct{}{}
	})
	t1, t2 := mcp.NewInMemoryTransports()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = srv.Run(ctx, t1) }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-init"}, nil)
	sess, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer sess.Close()

	select {
	case <-called:
		// onInitialized was invoked as expected
	case <-time.After(2 * time.Second):
		t.Error("onInitialized callback was not called within 2s")
	}
}

func containsStr(b []byte, s string) bool {
	return strings.Contains(string(b), s)
}
