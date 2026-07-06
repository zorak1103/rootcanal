package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zorak1103/rootcanal/internal/jobs"
	"github.com/zorak1103/rootcanal/internal/session"
)

type runOnceIn struct {
	Host      string            `json:"host"                 jsonschema:"pre-declared host name from rootcanal config"`
	Command   string            `json:"command"              jsonschema:"shell command to execute; requires POSIX-compatible remote shell"`
	Stdin     string            `json:"stdin,omitempty"      jsonschema:"bytes piped to the command stdin"`
	Env       map[string]string `json:"env,omitempty"        jsonschema:"environment variables (may be rejected by server AcceptEnv policy)"`
	TimeoutMs int               `json:"timeout_ms,omitempty" jsonschema:"max milliseconds; clamped to server config maximum"`
	Detach    bool              `json:"detach,omitempty"     jsonschema:"if true, start in background and return job_id; poll status via ssh_job_status"`
}

type runOnceOut struct {
	Stdout    string   `json:"stdout"`
	Stderr    string   `json:"stderr"`
	ExitCode  int      `json:"exit_code"`
	Signal    string   `json:"signal,omitempty"`
	Truncated bool     `json:"truncated,omitempty"`
	Warnings  []string `json:"warnings,omitempty"`
}

func handleRunOnce(mgr session.Manager, reg *jobs.Registry) func(context.Context, *mcp.CallToolRequest, runOnceIn) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in runOnceIn) (*mcp.CallToolResult, any, error) {
		if in.Detach && reg != nil {
			jobID, err := mgr.Detach(ctx, in.Host, session.RunOnceInput{
				Command:   in.Command,
				Stdin:     in.Stdin,
				Env:       in.Env,
				TimeoutMs: in.TimeoutMs,
			}, reg)
			if err != nil {
				r, _, _ := toolErr(err)
				return r, nil, nil
			}
			type detachOut struct {
				JobID string `json:"job_id"`
				Host  string `json:"host"`
			}
			out := detachOut{JobID: jobID, Host: in.Host}
			b, _ := json.Marshal(out)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, out, nil
		}

		// Non-detach path (standard synchronous RunOnce).
		res, err := mgr.RunOnce(ctx, in.Host, session.RunOnceInput{
			Command:   in.Command,
			Stdin:     in.Stdin,
			Env:       in.Env,
			TimeoutMs: in.TimeoutMs,
		})
		if err != nil {
			r, _, _ := toolErr(err)
			return r, runOnceOut{}, nil
		}
		out := runOnceOut{
			Stdout:    res.Stdout,
			Stderr:    res.Stderr,
			ExitCode:  res.ExitCode,
			Signal:    res.Signal,
			Truncated: res.Truncated,
			Warnings:  res.Warnings,
		}
		b, jsonErr := json.Marshal(out)
		if jsonErr != nil {
			r, _, _ := toolErr(fmt.Errorf("marshal response: %w", jsonErr))
			return r, out, nil
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: string(b)}},
		}, out, nil
	}
}
