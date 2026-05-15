# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Dev Commands

```sh
task build   # compile binary (outputs rootcanal / rootcanal.exe)
task test    # run all tests
task cover   # run tests + enforce ≥85% line coverage
task lint    # go vet + staticcheck

# Run a single test package or specific test
go test ./internal/session/ -run TestManagerSend -v

# Race detector (requires CGO)
CGO_ENABLED=1 go test ./... -race

# Validate config without connecting
./rootcanal -validate-config -config rootcanal.yaml

# Probe a single host
./rootcanal -probe prod-web -config rootcanal.yaml
```

## Architecture

rootcanal is an SSH MCP server. An MCP client (e.g. Claude Desktop) speaks the stdio MCP protocol; rootcanal proxies commands to pre-declared SSH hosts. Hosts are locked down in config — the LLM can never reach an arbitrary IP.

```
MCP client ──stdio──▶ mcpserver ──▶ session.Manager ──▶ hostpool ──▶ sshconn ──▶ SSH host
                                 └──▶ sftpops ─────────▶ hostpool ──▶ sshconn ──▶ SSH host
```

**`cmd/rootcanal/main.go`** — entry point. Parses CLI flags (`-config`, `-validate-config`, `-probe`, `-version`), wires all components, starts the MCP server over stdio. Also handles the log-handler swap: before MCP handshake logs go to stderr; after handshake they route through `mcp.NewLoggingHandler` to the client.

**`internal/config`** — YAML config loading (`config.Load`), `${ENV_VAR}` interpolation, validation. Passwords and passphrases must come from env vars, never from the config file.

**`internal/sshconn`** — SSH dialing and auth. `Dialer` is an interface; `ProdDialer` is the real impl. Auth strategies: `key` (private key, optional passphrase from env), `agent` (SSH_AUTH_SOCK on Unix, named pipe on Windows), `password` (env var). Host-key verification is always strict (`known_hosts`-based). Platform-specific agent code is split into `agent_unix.go` / `agent_windows.go`.

**`internal/hostpool`** — ref-counted `*ssh.Client` pool. One TCP connection per host, shared across concurrent sessions. Pool entries close automatically after 30 s of zero references. `Get` returns a client + release func; callers must invoke release when done.

**`internal/session`** — PTY-based persistent shell sessions. `Manager` interface exposes `Open / Send / Close / List / Shutdown`. Each session holds a ring buffer (`ringbuf.go`) for output. `Send` writes to stdin, then waits for a 50 ms quiescence gap (or timeout) before draining the buffer and returning output. A background GC goroutine closes idle or aged-out sessions.

**`internal/sftpops`** — SFTP read/write/list on top of `hostpool`. `Ops` interface wraps the three operations; size limits for read/write are enforced from config.

**`internal/mcpserver`** — registers the seven MCP tools (`ssh_session_open`, `ssh_session_send`, `ssh_session_close`, `ssh_session_list`, `sftp_read`, `sftp_write`, `sftp_list`) against `session.Manager` and `sftpops.Ops`. Tool handler files: `tools_session.go`, `tools_sftp.go`.

**`internal/logging`** — `SwapHandler` wraps an `slog.Handler` and lets `main` atomically replace it after the MCP handshake without restarting the logger.

**`internal/version`** — three `var` stubs (`Version`, `Commit`, `Date`) populated via `-ldflags` at build time.

**`tools/covercheck`** — build-time helper invoked by `task cover`. Runs `go tool cover -func` on `coverage.out` and exits non-zero if total coverage is below the configured minimum.

## Testing Patterns

Tests use the `Dialer` interface to inject fake SSH connections without a real server. `session` and `hostpool` tests pass a `newSessionFn` factory; `mcpserver` tests use a mock `Manager`/`Ops`. All tests are table-driven.

