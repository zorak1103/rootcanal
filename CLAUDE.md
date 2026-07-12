# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Dev Commands

```sh
task build   # compile binary (outputs rootcanal / rootcanal.exe)
task test    # run all tests
task cover   # run tests + enforce ≥85% line coverage
task lint    # golangci-lint v2 (requires `task lint:install` locally first)

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

**`internal/config`** — YAML config loading (`config.Load`), `${ENV_VAR}` interpolation, validation. Passwords and passphrases must come from env vars, never from the config file. `Host` carries `sftp_enabled`, `sftp_allowed_prefixes`, and v2.0 additions `term`, `clean_output`, `idle_timeout`, `description`; all validated at load time. `Limits` includes v2.0 fields `default_term`, `default_clean_output`, `run_once_max_bytes`, `run_once_max_timeout_ms`, `max_run_once_concurrent`, plus v1 fields `dial_timeout`, `default_send_timeout_ms`, `max_send_timeout_ms`. Also includes `detach_max_duration_ms` (default 86400000 ms / 24 h), which is the wall-clock ceiling for detached jobs (`ssh_run_once` with `detach=true`) — separate from the 60 s synchronous cap.

**`internal/sshconn`** — SSH dialing and auth. `Dialer` is an interface; `ProdDialer` is the real impl. Auth strategies: `key` (private key, optional passphrase from env), `agent` (SSH_AUTH_SOCK on Unix, named pipe on Windows), `password` (env var). Host-key verification is always strict (`known_hosts`-based). Platform-specific agent code is split into `agent_unix.go` / `agent_windows.go`.

**`internal/hostpool`** — ref-counted `*ssh.Client` pool. One TCP connection per host, shared across concurrent sessions. Pool entries close automatically after 30 s of zero references. `Get` returns a client + release func; callers must invoke release when done.

**`internal/session`** — PTY-based persistent shell sessions. `Manager` interface exposes `Open / Send / Close / List / RunOnce / Shutdown`. Each session holds a ring buffer (`ringbuf.go`) for output. `Send` injects a `RC_EXIT_<nonce>_<code>` sentinel marker after the command and waits for it to appear in the output stream, returning `SendResult{Output, ExitCode, StillRunning, ClosedReason, Warnings, Truncated}`. Pass `raw: true` to skip marker injection and ANSI stripping (falls back to 50 ms quiescence). `Open` atomically reserves a session slot before dialing; MOTD is suppressed in `bootSession` (`manager.go`). ANSI stripping is applied by default via `cleanOutput` (`strip.go`, backed by `jimschubert/stripansi`). `RunOnce` uses an SSH exec channel (no PTY) with a `cappedBuffer` for bounded output. A background GC goroutine closes idle or aged-out sessions.

**`internal/sftpops`** — SFTP read/write/list on top of `hostpool`. `Ops` interface wraps the three operations. Every call is gated by `validateSFTPPath`: checks `sftp_enabled` on the host, applies `path.Clean` and rejects non-absolute results, then enforces `sftp_allowed_prefixes` (empty list denies all paths; `["/"]` allows any absolute path). Size limits for read/write are enforced from config.

**`internal/mcpserver`** — registers ten MCP tools against `session.Manager`, `sftpops.Ops`, and `*config.Config`. Session tools: `ssh_session_open`, `ssh_session_send`, `ssh_session_close`, `ssh_session_list`. SFTP tools: `sftp_read`, `sftp_write`, `sftp_list`. Run-once (always registered): `ssh_run_once`. Discovery tools (registered when `cfg != nil`): `ssh_list_hosts`, `ssh_host_capabilities`. Handler files: `tools_session.go`, `tools_sftp.go`, `tools_runonce.go`, `tools_discovery.go`.

**`internal/logging`** — `SwapHandler` wraps an `slog.Handler` and lets `main` atomically replace it after the MCP handshake without restarting the logger.

**`internal/version`** — three `var` stubs (`Version`, `Commit`, `Date`) populated via `-ldflags` at build time.

**`tools/covercheck`** — build-time helper invoked by `task cover`. Runs `go tool cover -func` on `coverage.out` and exits non-zero if total coverage is below the configured minimum.

## Testing Patterns

Tests use the `Dialer` interface to inject fake SSH connections without a real server. `session` and `hostpool` tests pass a `newSessionFn` factory; `mcpserver` tests use a mock `Manager`/`Ops`. All tests are table-driven.

