package mcpserver

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zorak1103/rootcanal/internal/session"
)

// ---- ssh_session_open ----

type sessionOpenIn struct {
	Host string `json:"host" jsonschema:"pre-declared host name from rootcanal config"`
	Name string `json:"name,omitempty" jsonschema:"optional client-supplied session name"`
}

type sessionOpenOut struct {
	SessionID string `json:"session_id" jsonschema:"opaque session identifier; pass to ssh_session_send and ssh_session_close"`
}

func handleSessionOpen(mgr session.Manager) func(context.Context, *mcp.CallToolRequest, sessionOpenIn) (*mcp.CallToolResult, sessionOpenOut, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in sessionOpenIn) (*mcp.CallToolResult, sessionOpenOut, error) {
		id, err := mgr.Open(ctx, in.Host, in.Name)
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
	SessionID  string `json:"session_id"`
	Input      string `json:"input"`
	TimeoutMs  int    `json:"timeout_ms,omitempty"`
	Raw        bool   `json:"raw,omitempty"          jsonschema:"skip echo/ANSI stripping and marker injection"`
	WaitIdleMs int    `json:"wait_idle_ms,omitempty" jsonschema:"peek mode: return after this many ms of silence; mutually exclusive with input"`
}

type sessionSendOut struct {
	Output       string   `json:"output"`
	ExitCode     *int     `json:"exit_code,omitempty"`
	StillRunning bool     `json:"still_running,omitempty"`
	Truncated    bool     `json:"truncated,omitempty"`
	ClosedReason string   `json:"closed_reason,omitempty"`
	Warnings     []string `json:"warnings,omitempty"`
}

func handleSessionSend(mgr session.Manager) func(context.Context, *mcp.CallToolRequest, sessionSendIn) (*mcp.CallToolResult, sessionSendOut, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in sessionSendIn) (*mcp.CallToolResult, sessionSendOut, error) {
		res, err := mgr.Send(ctx, in.SessionID, session.SendInput{
			Input:      in.Input,
			TimeoutMs:  in.TimeoutMs,
			Raw:        in.Raw,
			WaitIdleMs: in.WaitIdleMs,
		})
		if err != nil {
			r, _, _ := toolErr(err)
			return r, sessionSendOut{}, nil
		}
		out := sessionSendOut{
			Output:       res.Output,
			ExitCode:     res.ExitCode,
			StillRunning: res.StillRunning,
			Truncated:    res.Truncated,
			ClosedReason: res.ClosedReason,
			Warnings:     res.Warnings,
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: res.Output}},
		}, out, nil
	}
}

// ---- ssh_session_close ----

type sessionCloseIn struct {
	SessionID string `json:"session_id" jsonschema:"session ID to close"`
}

type sessionCloseOut struct {
	ClosedReason string `json:"closed_reason"`
}

func handleSessionClose(mgr session.Manager) func(context.Context, *mcp.CallToolRequest, sessionCloseIn) (*mcp.CallToolResult, sessionCloseOut, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in sessionCloseIn) (*mcp.CallToolResult, sessionCloseOut, error) {
		reason, err := mgr.Close(ctx, in.SessionID)
		if err != nil {
			r, _, _ := toolErr(err)
			return r, sessionCloseOut{}, nil
		}
		out := sessionCloseOut{ClosedReason: reason}
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: "Session " + in.SessionID + " closed (" + reason + ")."},
			},
		}, out, nil
	}
}

// ---- ssh_session_list ----

type sessionListOut struct {
	Sessions []sessionSummary `json:"sessions"`
}

type sessionSummary struct {
	SessionID    string `json:"session_id"`
	Name         string `json:"name,omitempty"`
	Host         string `json:"host"`
	OpenedAt     string `json:"opened_at"`
	LastUsedAt   string `json:"last_used_at"`
	LastExitCode *int   `json:"last_exit_code,omitempty"`
	StillRunning bool   `json:"still_running,omitempty"`
	ClosedReason string `json:"closed_reason,omitempty"`
}

func handleSessionList(mgr session.Manager) func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, sessionListOut, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, sessionListOut, error) {
		infos := mgr.List()
		summaries := make([]sessionSummary, len(infos))
		for i := range infos {
			info := &infos[i]
			summaries[i] = sessionSummary{
				SessionID:    info.ID,
				Name:         info.Name,
				Host:         info.Host,
				OpenedAt:     info.OpenedAt.UTC().Format(time.RFC3339),
				LastUsedAt:   info.LastUsedAt.UTC().Format(time.RFC3339),
				LastExitCode: info.LastExitCode,
				StillRunning: info.StillRunning,
				ClosedReason: info.ClosedReason,
			}
		}
		out := sessionListOut{Sessions: summaries}
		text := formatSessionList(summaries)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, out, nil
	}
}

// sanitizeOutput replaces invalid UTF-8 sequences with U+FFFD so the JSON
// transport is always valid.
func sanitizeOutput(raw []byte) string {
	if utf8.Valid(raw) {
		return string(raw)
	}
	return strings.ToValidUTF8(string(raw), "�")
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
