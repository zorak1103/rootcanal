package mcpserver_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gitlab.com/zorak1103/rootcanal/internal/mcpserver"
	"gitlab.com/zorak1103/rootcanal/internal/session"
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

// ---- test helpers ----

func newTestClient(t *testing.T, mgr session.Manager) *mcp.ClientSession {
	t.Helper()
	srv := mcpserver.New(mgr, nil)
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
	sess := newTestClient(t, mgr)

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
	sess := newTestClient(t, mgr)

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
	sess := newTestClient(t, mgr)

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
	sess := newTestClient(t, mgr)

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
	sess := newTestClient(t, mgr)

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

func TestTool_SessionList(t *testing.T) {
	now := time.Now()
	mgr := &fakeManager{
		listFn: func() []session.SessionInfo {
			return []session.SessionInfo{
				{ID: "s_AAA", Host: "srv1", OpenedAt: now, LastUsedAt: now},
			}
		},
	}
	sess := newTestClient(t, mgr)

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

func TestOnInitialized_IsCalled(t *testing.T) {
	called := make(chan struct{}, 1)
	mgr := &fakeManager{listFn: func() []session.SessionInfo { return nil }}

	srv := mcpserver.New(mgr, func(_ *mcp.ServerSession) {
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
