package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gitlab.com/zorak1103/rootcanal/internal/session"
	"gitlab.com/zorak1103/rootcanal/internal/version"
)

// New builds a configured *mcp.Server with all session tools registered.
//
// onInitialized, if non-nil, is called once the MCP session handshake
// completes and the *ServerSession is available. Use it to install an
// mcp.NewLoggingHandler so that subsequent logs are routed to the client.
func New(mgr session.Manager, onInitialized func(*mcp.ServerSession)) *mcp.Server {
	opts := &mcp.ServerOptions{}
	if onInitialized != nil {
		opts.InitializedHandler = func(_ context.Context, req *mcp.InitializedRequest) {
			onInitialized(req.Session)
		}
	}

	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "rootcanal",
		Version: version.Version,
	}, opts)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "ssh_session_open",
		Description: "Open a persistent interactive shell session on a pre-declared host. Returns a session_id for use with ssh_session_send and ssh_session_close.",
	}, handleSessionOpen(mgr))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "ssh_session_send",
		Description: "Write input to an open shell session's stdin and return any output received within the timeout. Send an empty input string to just poll for output.",
	}, handleSessionSend(mgr))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "ssh_session_close",
		Description: "Close an open shell session and release its resources.",
	}, handleSessionClose(mgr))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "ssh_session_list",
		Description: "List all currently open shell sessions with their host and timing metadata.",
	}, handleSessionList(mgr))

	return srv
}
