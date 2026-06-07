package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gitlab.com/zorak1103/rootcanal/internal/config"
	"gitlab.com/zorak1103/rootcanal/internal/hostkeys"
	"gitlab.com/zorak1103/rootcanal/internal/jobs"
	"gitlab.com/zorak1103/rootcanal/internal/session"
	"gitlab.com/zorak1103/rootcanal/internal/sftpops"
	"gitlab.com/zorak1103/rootcanal/internal/version"
)

// New builds a configured *mcp.Server with all session, SFTP, and skill tools
// registered, plus skill:// resources for each embedded guidance document.
//
// Total tools: 13 always-registered (session ×4, SFTP ×3, ssh_run_once, get_skill)
// plus 2 optional discovery tools (ssh_list_hosts, ssh_host_capabilities) when cfg
// is non-nil, and 2 optional job tools (ssh_job_status, ssh_job_cancel) when reg
// is non-nil. skill:// resources are always registered.
//
// cfg, if non-nil, enables the discovery tools (ssh_list_hosts, ssh_host_capabilities).
//
// reg, if non-nil, enables the job tools (ssh_job_status, ssh_job_cancel) and
// the detach mode for ssh_run_once.
//
// hk, if non-nil, enables the ssh_accept_host_key tool (also requires cfg != nil).
//
// onInitialized, if non-nil, is called once the MCP session handshake completes
// so the caller can swap in an mcp.NewLoggingHandler to route logs to the client.
func New(mgr session.Manager, ops sftpops.Ops, cfg *config.Config, reg *jobs.Registry, hk hostkeys.Refresher, onInitialized func(*mcp.ServerSession)) *mcp.Server {
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

	srv.AddReceivingMiddleware(fieldSuggestionMiddleware())

	// Session tools
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "ssh_session_open",
		Description: "Open a persistent interactive shell session on a pre-declared host. Returns a session_id for use with ssh_session_send and ssh_session_close.",
	}, handleSessionOpen(mgr))

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "ssh_session_send",
		Description: "Write input to an open shell session's stdin and wait for the command to complete (marker-based). Send empty input to continue waiting for an in-flight command. Use wait_idle_ms for raw/REPL mode.",
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
		Description: "Execute a single command on a pre-declared host via SSH exec channel (no PTY). Returns stdout, stderr, and exit_code. Use detach=true to run in background and get a job_id. Use this for one-shot reads (df, ls, docker inspect, cat) instead of open/send/close. Requires a POSIX-compatible remote shell.",
	}, handleRunOnce(mgr, reg))

	// Job tools (available when reg is provided)
	if reg != nil {
		mcp.AddTool(srv, &mcp.Tool{
			Name:        "ssh_job_status",
			Description: "Poll the status and output tail of a detached job started with ssh_run_once(detach=true). Returns running state, elapsed time, exit code, and stdout/stderr tails.",
		}, handleJobStatus(reg))

		mcp.AddTool(srv, &mcp.Tool{
			Name:        "ssh_job_cancel",
			Description: "Cancel a running detached job by job_id. Sends SIGTERM to the remote process.",
		}, handleJobCancel(reg))
	}

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

	// Host-key refresh tool (requires both cfg and hk)
	if cfg != nil && hk != nil {
		mcp.AddTool(srv, &mcp.Tool{
			Name: "ssh_accept_host_key",
			Description: "Inspect or re-trust a changed SSH host key after a server rebuild. " +
				"Call without confirm to preview the current and new key fingerprints. " +
				"Call with confirm=true and expected_fingerprint=<new_fingerprint> to rewrite " +
				"the known_hosts entry. Requires allow_known_hosts_update: true on the host. " +
				"Only use after a human has confirmed the server was legitimately rebuilt.",
		}, handleAcceptHostKey(hk))
	}

	// Skill resources and tool (always registered)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_skill",
		Description: "Access embedded skill guidance docs. Use action=list to see available skills, action=read with skill=<slug> to fetch a specific doc. Prefer reading skill:// resources directly if your client supports them.",
	}, handleGetSkill())
	registerSkillResources(srv)

	return srv
}
