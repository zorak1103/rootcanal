package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Field names repeated across multiple tools' known-field lists below.
const (
	fieldHost      = "host"
	fieldTimeoutMs = "timeout_ms"
)

// toolFields maps tool name → known input field names.
// Keep in sync with tool input structs in tools_*.go files.
var toolFields = map[string][]string{
	"ssh_session_open":      {fieldHost, "name"},
	"ssh_session_send":      {"session_id", "input", fieldTimeoutMs, "wait_idle_ms", "raw"},
	"ssh_session_close":     {"session_id"},
	"ssh_session_list":      {},
	"sftp_read":             {fieldHost, "path", "max_bytes"},
	"sftp_write":            {fieldHost, "path", "content", "binary", "mode", "atomic"},
	"sftp_list":             {fieldHost, "path"},
	"ssh_run_once":          {fieldHost, "command", "stdin", "env", fieldTimeoutMs, "detach"},
	"ssh_list_hosts":        {},
	"ssh_host_capabilities": {fieldHost},
	"ssh_job_status":        {"job_id"},
	"ssh_job_cancel":        {"job_id"},
}

// levenshtein returns the edit distance between a and b (case-insensitive).
func levenshtein(a, b string) int {
	a = strings.ToLower(a)
	b = strings.ToLower(b)
	la, lb := len(a), len(b)
	dp := make([][]int, la+1)
	for i := range dp {
		dp[i] = make([]int, lb+1)
		dp[i][0] = i
	}
	for j := range dp[0] {
		dp[0][j] = j
	}
	for i := 1; i <= la; i++ {
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			dp[i][j] = min(dp[i-1][j]+1, dp[i][j-1]+1, dp[i-1][j-1]+cost)
		}
	}
	return dp[la][lb]
}

// suggestField returns the closest known field name within edit distance 3,
// or "" if none qualifies. Comparison is case-insensitive.
func suggestField(input string, known []string) string {
	best, bestDist := "", 4
	for _, k := range known {
		if d := levenshtein(input, k); d < bestDist {
			bestDist = d
			best = k
		}
	}
	return best
}

// checkUnknownFields validates that all keys in args are known fields for
// the given tool. Returns a friendly error with a suggestion if not.
// Returns nil for unknown tools (pass-through).
func checkUnknownFields(toolName string, args map[string]any) error {
	known, ok := toolFields[toolName]
	if !ok {
		return nil
	}
	knownSet := make(map[string]bool, len(known))
	for _, k := range known {
		knownSet[k] = true
	}
	for key := range args {
		if !knownSet[key] {
			if suggestion := suggestField(key, known); suggestion != "" {
				return fmt.Errorf("unexpected property %q — did you mean %q?", key, suggestion)
			}
			return fmt.Errorf("unexpected property %q — known fields for %s: %s",
				key, toolName, strings.Join(known, ", "))
		}
	}
	return nil
}

// fieldSuggestionMiddleware returns a receiving middleware that pre-validates
// tool call argument keys before the SDK's schema validator runs.
func fieldSuggestionMiddleware() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != "tools/call" {
				return next(ctx, method, req)
			}

			// CallToolRequest = ServerRequest[*CallToolParamsRaw]; assert on the
			// concrete request type, not on the Params interface value.
			ctr, ok := req.(*mcp.CallToolRequest)
			if !ok || ctr.Params == nil || len(ctr.Params.Arguments) == 0 {
				return next(ctx, method, req)
			}

			var args map[string]any
			if err := json.Unmarshal(ctr.Params.Arguments, &args); err != nil {
				return next(ctx, method, req)
			}

			if err := checkUnknownFields(ctr.Params.Name, args); err != nil {
				return nil, err
			}

			return next(ctx, method, req)
		}
	}
}
