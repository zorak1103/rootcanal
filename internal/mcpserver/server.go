package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gitlab.com/zorak1103/rootcanal/internal/config"
	"gitlab.com/zorak1103/rootcanal/internal/session"
	"gitlab.com/zorak1103/rootcanal/internal/sftpops"
	"gitlab.com/zorak1103/rootcanal/internal/version"
)

// New builds a configured *mcp.Server with all session and SFTP tools registered.
//
// cfg, if non-nil, enables the discovery tools (ssh_list_hosts, ssh_host_capabilities).
//
// onInitialized, if non-nil, is called once the MCP session handshake completes
// so the caller can swap in an mcp.NewLoggingHandler to route logs to the client.
func New(mgr session.Manager, ops sftpops.Ops, cfg *config.Config, onInitialized func(*mcp.ServerSession)) *mcp.Server {
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

	// Session tools
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

	// SFTP tools
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "sftp_read",
		Description: "Read a file from a remote host via SFTP. Returns UTF-8 text; binary files are base64-encoded with binary=true in the output.",
	}, handleSFTPRead(ops))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "sftp_write",
		Description: "Write content to a file on a remote host via SFTP. Pass binary=true and base64-encode binary content. Use mode (octal string, e.g. '0644') to set permissions.",
	}, handleSFTPWrite(ops))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "sftp_list",
		Description: "List the contents of a directory on a remote host via SFTP.",
	}, handleSFTPList(ops))

	// Run-once tool (always registered)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "ssh_run_once",
		Description: "Execute a single command on a pre-declared host via SSH exec channel (no PTY). Returns stdout, stderr, and exit_code. Use this for one-shot reads (df, ls, docker inspect, cat) instead of open/send/close. Requires a POSIX-compatible remote shell.",
	}, handleRunOnce(mgr))

	// Discovery tools (available when cfg is provided)
	if cfg != nil {
		mcp.AddTool(srv, &mcp.Tool{
			Name:        "ssh_list_hosts",
			Description: "List all pre-declared SSH hosts with their non-sensitive metadata (name, address, user, auth type, SFTP status). Credentials and key paths are never included.",
		}, handleListHosts(cfg))

		mcp.AddTool(srv, &mcp.Tool{
			Name:        "ssh_host_capabilities",
			Description: "Return what rootcanal can do on a specific host: SSH, SFTP, allowed SFTP path prefixes, session idle timeout, and terminal/output settings.",
		}, handleHostCapabilities(cfg))
	}

	return srv
}
