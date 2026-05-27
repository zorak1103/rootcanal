package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gitlab.com/zorak1103/rootcanal/internal/jobs"
)

// ---- ssh_job_status ----

type jobStatusIn struct {
	JobID string `json:"job_id" jsonschema:"job ID returned by ssh_run_once with detach=true"`
}

type jobStatusOut struct {
	Running    bool   `json:"running"`
	ElapsedS   int    `json:"elapsed_s"`
	ExitCode   *int   `json:"exit_code,omitempty"`
	StdoutTail string `json:"stdout_tail,omitempty"`
	StderrTail string `json:"stderr_tail,omitempty"`
}

func handleJobStatus(reg *jobs.Registry) func(context.Context, *mcp.CallToolRequest, jobStatusIn) (*mcp.CallToolResult, jobStatusOut, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, in jobStatusIn) (*mcp.CallToolResult, jobStatusOut, error) {
		job, ok := reg.Get(in.JobID)
		if !ok {
			r, _, _ := toolErr(fmt.Errorf("job %q not found (expired or never existed)", in.JobID))
			return r, jobStatusOut{}, nil
		}
		out := jobStatusOut{
			Running:    job.Running(),
			ElapsedS:   job.ElapsedSeconds(),
			ExitCode:   job.ExitCode(),
			StdoutTail: job.StdoutTail(4096),
			StderrTail: job.StderrTail(4096),
		}
		b, err := json.Marshal(out)
		if err != nil {
			r, _, _ := toolErr(fmt.Errorf("marshal: %w", err))
			return r, out, nil
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, out, nil
	}
}

// ---- ssh_job_cancel ----

type jobCancelIn struct {
	JobID string `json:"job_id" jsonschema:"job ID returned by ssh_run_once with detach=true"`
}

type jobCancelOut struct {
	Canceled   bool `json:"canceled"`
	WasRunning bool `json:"was_running"`
}

func handleJobCancel(reg *jobs.Registry) func(context.Context, *mcp.CallToolRequest, jobCancelIn) (*mcp.CallToolResult, jobCancelOut, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, in jobCancelIn) (*mcp.CallToolResult, jobCancelOut, error) {
		job, ok := reg.Get(in.JobID)
		if !ok {
			r, _, _ := toolErr(fmt.Errorf("job %q not found", in.JobID))
			return r, jobCancelOut{}, nil
		}
		wasRunning := job.Running()
		if wasRunning {
			reg.Cancel(in.JobID)
		}
		out := jobCancelOut{Canceled: wasRunning, WasRunning: wasRunning}
		b, err := json.Marshal(out)
		if err != nil {
			r, _, _ := toolErr(fmt.Errorf("marshal: %w", err))
			return r, out, nil
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, out, nil
	}
}
