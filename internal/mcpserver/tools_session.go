package mcpserver

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gitlab.com/zorak1103/rootcanal/internal/session"
)

// ---- ssh_session_open ----

type sessionOpenIn struct {
	Host string `json:"host" jsonschema:"pre-declared host name from rootcanal config"`
}

type sessionOpenOut struct {
	SessionID string `json:"session_id" jsonschema:"opaque session identifier; pass to ssh_session_send and ssh_session_close"`
}

func handleSessionOpen(mgr session.Manager) func(context.Context, *mcp.CallToolRequest, sessionOpenIn) (*mcp.CallToolResult, sessionOpenOut, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in sessionOpenIn) (*mcp.CallToolResult, sessionOpenOut, error) {
		id, err := mgr.Open(ctx, in.Host)
		if err != nil {
			r, _, _ := toolErr(err)
			return r, sessionOpenOut{}, nil
		}
		out := sessionOpenOut{SessionID: id}
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: "Session opened: " + id},
			},
		}, out, nil
	}
}

// ---- ssh_session_send ----

type sessionSendIn struct {
	SessionID string `json:"session_id" jsonschema:"session ID returned by ssh_session_open"`
	Input     string `json:"input"      jsonschema:"text to write to the shell's stdin; include a trailing \\n to submit a command"`
	TimeoutMs int    `json:"timeout_ms,omitempty" jsonschema:"max milliseconds to wait for output (default: server default, max: server max)"`
}

type sessionSendOut struct {
	Output    string `json:"output"               jsonschema:"text received from the shell since the last Send"`
	Truncated bool   `json:"truncated,omitempty"  jsonschema:"true if output was dropped due to buffer overflow"`
	Closed    bool   `json:"closed,omitempty"     jsonschema:"true if the remote shell has exited"`
}

func handleSessionSend(mgr session.Manager) func(context.Context, *mcp.CallToolRequest, sessionSendIn) (*mcp.CallToolResult, sessionSendOut, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in sessionSendIn) (*mcp.CallToolResult, sessionSendOut, error) {
		timeout := time.Duration(in.TimeoutMs) * time.Millisecond

		raw, truncated, closed, err := mgr.Send(ctx, in.SessionID, []byte(in.Input), timeout)
		if err != nil {
			r, _, _ := toolErr(err)
			return r, sessionSendOut{}, nil
		}

		text := sanitizeOutput(raw)
		out := sessionSendOut{Output: text, Truncated: truncated, Closed: closed}
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: text},
			},
		}, out, nil
	}
}

// ---- ssh_session_close ----

type sessionCloseIn struct {
	SessionID string `json:"session_id" jsonschema:"session ID to close"`
}

func handleSessionClose(mgr session.Manager) func(context.Context, *mcp.CallToolRequest, sessionCloseIn) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in sessionCloseIn) (*mcp.CallToolResult, any, error) {
		if err := mgr.Close(ctx, in.SessionID); err != nil {
			return toolErr(err)
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: "Session " + in.SessionID + " closed."},
			},
		}, nil, nil
	}
}

// ---- ssh_session_list ----

type sessionListOut struct {
	Sessions []sessionSummary `json:"sessions"`
}

type sessionSummary struct {
	SessionID  string `json:"session_id"`
	Host       string `json:"host"`
	OpenedAt   string `json:"opened_at"`
	LastUsedAt string `json:"last_used_at"`
}

func handleSessionList(mgr session.Manager) func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, sessionListOut, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, sessionListOut, error) {
		infos := mgr.List()
		summaries := make([]sessionSummary, len(infos))
		for i, info := range infos {
			summaries[i] = sessionSummary{
				SessionID:  info.ID,
				Host:       info.Host,
				OpenedAt:   info.OpenedAt.UTC().Format(time.RFC3339),
				LastUsedAt: info.LastUsedAt.UTC().Format(time.RFC3339),
			}
		}
		out := sessionListOut{Sessions: summaries}
		text := formatSessionList(summaries)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, out, nil
	}
}

func formatSessionList(ss []sessionSummary) string {
	if len(ss) == 0 {
		return "No open sessions."
	}
	var b strings.Builder
	for _, s := range ss {
		b.WriteString(s.SessionID + "  " + s.Host + "  opened=" + s.OpenedAt + "\n")
	}
	return b.String()
}

// sanitizeOutput replaces invalid UTF-8 sequences so the JSON transport never
// breaks. ANSI/control chars are preserved — the LLM handles them correctly.
func sanitizeOutput(raw []byte) string {
	if utf8.Valid(raw) {
		return string(raw)
	}
	return strings.ToValidUTF8(string(raw), "�")
}
