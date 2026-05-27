package mcpserver_test

import (
	"context"
	"io/fs"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestSFTPWrite_SpecialModeBitsRejected verifies that the MAN-009 fix prevents
// setuid/setgid/sticky bits from being forwarded to the remote SFTP server.
func TestSFTPWrite_SpecialModeBitsRejected(t *testing.T) {
	tests := []struct {
		mode    string
		wantErr bool
		desc    string
	}{
		{"0644", false, "regular perms accepted"},
		{"0755", false, "execute bit accepted"},
		{"04755", true, "setuid rejected"},
		{"02755", true, "setgid rejected"},
		{"01777", true, "sticky rejected"},
		{"06755", true, "setuid+setgid rejected"},
		{"07777", true, "all special bits rejected"},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			var capturedMode fs.FileMode
			ops := &fakeOps{
				writeFn: func(_ context.Context, _, _ string, _ []byte, mode fs.FileMode, _ bool) error {
					capturedMode = mode
					return nil
				},
			}
			sess := newTestClient(t, nil, ops, nil)

			args := map[string]any{
				"host":    "any",
				"path":    "/tmp/test.txt",
				"content": "hello",
			}
			if tt.mode != "" {
				args["mode"] = tt.mode
			}

			res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{
				Name:      "sftp_write",
				Arguments: args,
			})
			if err != nil {
				t.Fatalf("CallTool error: %v", err)
			}

			if tt.wantErr {
				if !res.IsError {
					t.Errorf("mode %q: expected IsError=true, got false (special bits passed through)", tt.mode)
				}
				// The error message should indicate the problem.
				for _, c := range res.Content {
					if tc, ok := c.(*mcp.TextContent); ok {
						if !strings.Contains(strings.ToLower(tc.Text), "setuid") &&
							!strings.Contains(strings.ToLower(tc.Text), "setgid") &&
							!strings.Contains(strings.ToLower(tc.Text), "sticky") &&
							!strings.Contains(strings.ToLower(tc.Text), "special") &&
							!strings.Contains(strings.ToLower(tc.Text), "not permitted") {
							t.Errorf("mode %q: error message should mention special bits, got: %q", tt.mode, tc.Text)
						}
					}
				}
			} else {
				if res.IsError {
					var msg string
					for _, c := range res.Content {
						if tc, ok := c.(*mcp.TextContent); ok {
							msg = tc.Text
						}
					}
					t.Errorf("mode %q: unexpected error: %s", tt.mode, msg)
				}
				_ = capturedMode // ops.Write was invoked for non-error cases
			}
		})
	}
}
