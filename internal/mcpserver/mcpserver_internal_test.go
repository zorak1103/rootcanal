package mcpserver

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestSanitizeOutput_ValidUTF8(t *testing.T) {
	in := []byte("hello\nworld\n")
	got := sanitizeOutput(in)
	if got != "hello\nworld\n" {
		t.Errorf("valid UTF-8 must pass through unchanged, got %q", got)
	}
}

func TestSanitizeOutput_InvalidUTF8(t *testing.T) {
	in := []byte{0xff, 0xfe, 'x'}
	got := sanitizeOutput(in)
	if got == string(in) {
		t.Error("invalid UTF-8 must be replaced")
	}
	// Result must be valid UTF-8.
	for _, r := range got {
		if r == '�' {
			return // replacement found — good
		}
	}
}

func TestFormatSessionList_Empty(t *testing.T) {
	got := formatSessionList(nil)
	if got != "No open sessions." {
		t.Errorf("empty list: got %q, want 'No open sessions.'", got)
	}
}

func TestRejectDiscoverMiddleware_RejectsDiscover(t *testing.T) {
	nextCalled := false
	mw := rejectDiscoverMiddleware()
	handler := mw(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		nextCalled = true
		return nil, nil
	})

	_, err := handler(context.Background(), "server/discover", &mcp.CallToolRequest{})
	if err == nil {
		t.Fatal("expected server/discover to be rejected")
	}
	if nextCalled {
		t.Error("next should not be called for server/discover")
	}
}

func TestRejectDiscoverMiddleware_PassesThroughOtherMethods(t *testing.T) {
	nextCalled := false
	mw := rejectDiscoverMiddleware()
	handler := mw(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		nextCalled = true
		return nil, nil
	})

	_, err := handler(context.Background(), "tools/call", &mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("unexpected error for non-discover method: %v", err)
	}
	if !nextCalled {
		t.Error("next should be called for methods other than server/discover")
	}
}
