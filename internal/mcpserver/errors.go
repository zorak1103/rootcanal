package mcpserver

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// toolErr returns a CallToolResult with IsError set and the error as text content.
func toolErr(err error) (*mcp.CallToolResult, any, error) {
	res := &mcp.CallToolResult{}
	res.SetError(err)
	return res, nil, nil
}
