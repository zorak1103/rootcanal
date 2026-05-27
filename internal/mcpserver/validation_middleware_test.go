package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestLevenshtein(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"timeout", "timeout_ms", 3},
		{"timeout_ms", "timeout_ms", 0},
		{"xyzzy", "timeout_ms", 10},
		{"", "abc", 3},
		{"abc", "", 3},
	}
	for _, tt := range tests {
		t.Run(tt.a+"/"+tt.b, func(t *testing.T) {
			got := levenshtein(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("levenshtein(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestSuggestField(t *testing.T) {
	known := []string{"host", "command", "timeout_ms", "stdin", "env"}
	tests := []struct {
		input string
		want  string
	}{
		{"timeout", "timeout_ms"},
		{"timeoutms", "timeout_ms"},
		{"commnd", "command"}, // 2 edits away
		{"xyzzy", ""},         // no match within threshold
		{"host", "host"},      // exact match
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := suggestField(tt.input, known)
			if got != tt.want {
				t.Errorf("suggestField(%q, %v) = %q, want %q", tt.input, known, got, tt.want)
			}
		})
	}
}

func TestFieldSuggestionError_KnownTool(t *testing.T) {
	// "timeout" on ssh_run_once should suggest "timeout_ms"
	err := checkUnknownFields("ssh_run_once", map[string]any{"timeout": 5000})
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
	if !strings.Contains(err.Error(), "timeout_ms") {
		t.Errorf("error should suggest timeout_ms, got: %v", err)
	}
}

func TestFieldSuggestionError_NoMatch(t *testing.T) {
	err := checkUnknownFields("ssh_run_once", map[string]any{"xyzzy": 1})
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
	if strings.Contains(err.Error(), "did you mean") {
		t.Errorf("should not suggest when no close match, got: %v", err)
	}
}

func TestFieldSuggestionError_ValidArgs_NilError(t *testing.T) {
	err := checkUnknownFields("ssh_run_once", map[string]any{"host": "srv", "command": "ls"})
	if err != nil {
		t.Errorf("expected no error for valid args, got: %v", err)
	}
}

func TestFieldSuggestionError_UnknownTool_NilError(t *testing.T) {
	err := checkUnknownFields("unknown_tool", map[string]any{"anything": 1})
	if err != nil {
		t.Errorf("unknown tool should pass through without error, got: %v", err)
	}
}

// TestFieldSuggestionMiddleware_Integration passes a real *mcp.CallToolRequest
// through the middleware to confirm the type assertion in fieldSuggestionMiddleware
// is correct and the middleware actually fires (not a silent pass-through).
func TestFieldSuggestionMiddleware_Integration(t *testing.T) {
	// mcp.CallToolRequest = mcp.ServerRequest[*mcp.CallToolParamsRaw]
	req := &mcp.CallToolRequest{}
	req.Params = &mcp.CallToolParamsRaw{
		Name:      "ssh_run_once",
		Arguments: json.RawMessage(`{"host":"srv","timeout":5000}`),
	}

	mw := fieldSuggestionMiddleware()
	handler := mw(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		t.Error("next should not be called when middleware returns error")
		return nil, nil
	})

	_, err := handler(context.Background(), "tools/call", req)
	if err == nil {
		t.Fatal("expected error for unknown field 'timeout'")
	}
	if !strings.Contains(err.Error(), "timeout_ms") {
		t.Errorf("error should suggest timeout_ms, got: %v", err)
	}
}

func TestFieldSuggestionMiddleware_ValidArgs_CallsNext(t *testing.T) {
	req := &mcp.CallToolRequest{}
	req.Params = &mcp.CallToolParamsRaw{
		Name:      "ssh_run_once",
		Arguments: json.RawMessage(`{"host":"srv","command":"ls"}`),
	}

	nextCalled := false
	mw := fieldSuggestionMiddleware()
	handler := mw(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		nextCalled = true
		return nil, nil
	})

	_, err := handler(context.Background(), "tools/call", req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !nextCalled {
		t.Error("next should be called for valid args")
	}
}
