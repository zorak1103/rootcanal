---
name: rootcanal-ssh
description: >
  SSH session management and SFTP file access via the rootcanal MCP server
  (tools: mcp__rootcanal__ssh_session_open, mcp__rootcanal__ssh_session_send,
  mcp__rootcanal__ssh_session_close, mcp__rootcanal__ssh_session_list,
  mcp__rootcanal__ssh_run_once, mcp__rootcanal__ssh_list_hosts,
  mcp__rootcanal__ssh_host_capabilities, mcp__rootcanal__sftp_read,
  mcp__rootcanal__sftp_write, mcp__rootcanal__sftp_list).
  Use whenever the user asks to: connect to an SSH host, run remote shell commands,
  check server state, read or write remote files, or whenever any mcp__rootcanal__*
  tool is involved. Hosts come from the config file; read it or ask the user.
  Also triggers on: "SSH session", "remote shell", "SFTP", "remote file",
  "run on server", mcp__rootcanal tool errors.
---

# rootcanal SSH

rootcanal is a stdio MCP server for persistent PTY-based SSH sessions and SFTP file access.
All 10 tools are restricted to hosts declared in the operator config — the LLM cannot supply
raw addresses or ports.

Respond to the user in whatever language they write in. Technical tool names stay in English.

---

## Host Discovery

Call `ssh_list_hosts` to discover available hosts. Each entry includes name, address, user,
auth_type, and sftp_enabled. No credentials are ever returned.

To inspect limits and SFTP capabilities for a specific host, call `ssh_host_capabilities`.

---

## For One-shot Reads, Use `ssh_run_once`

`ssh_run_once` is the right tool for any command that doesn't need session state (`df`, `ls`,
`cat`, `docker inspect`).

- No open/close ceremony — one call returns stdout, stderr, and exit_code.
- No PTY: no MOTD, no ANSI, no echo. Output is byte-for-byte what the process wrote.
- Requires a POSIX-compatible remote shell (`sh`, `bash`, `zsh`, `dash`, `busybox` — not `fish`/`csh`).

---

## Session Workflow

Use persistent sessions when you need interactive state (environment variables, working directory,
long-running commands, TUI programs). Always follow this exact sequence:

```
1. ssh_session_open(host)                         → { session_id }
2. ssh_session_send(session_id, "cmd\n", ...)     → { output, exit_code?, still_running?, truncated?, closed_reason?, warnings? }
   (repeat with input="" to continue waiting for an in-flight command)
3. ssh_session_close(session_id)                  → always, even after errors
```

**Trailing `\n` is required** — the shell does not execute without it.

**Parallel pattern for N hosts (use this, it's much faster):**
```
Message 1: N × ssh_session_open   — all in parallel
Message 2: N × ssh_session_send   — all in parallel
Message 3: N × ssh_session_close  — all in parallel
```

**Session inventory:** Use `ssh_session_list` to inspect open sessions when approaching
session limits or after a "session not found" error.

→ Full tool parameter reference: [references/tools.md](references/tools.md)

---

## Output Cleanliness

1. **Returns when the completion marker arrives**, not on 50 ms quiescence. Output is deterministic.
2. **Output is clean by default** — PTY echo, ANSI escapes, and the shell prompt are stripped.
   Pass `raw: true` to receive unfiltered bytes.

---

## Long-running Commands

Use `still_running` + continuation to drive commands that exceed `timeout_ms`:

```
Send("./build.sh\n", timeout_ms=5000)  → { still_running: true, output: "..." }
Send("", timeout_ms=60000)             → { exit_code: 0, output: "Build done." }
```

Empty input with no `wait_idle_ms` continues waiting for the in-flight command's marker.
Do NOT send a new command while `still_running: true` — you'll receive an error.
For TUI peeking (vim, top), use `wait_idle_ms` instead of empty input.

---

## Hard Rules

**NEVER do any of the following:**

- **Pass a sudo password in conversation or prompt context.** It travels to Anthropic's
  infrastructure in plaintext and may appear in conversation logs. See [references/sudo.md](references/sudo.md).

- **Invent host names.** Only use names present in the config or returned by `ssh_list_hosts`.
  An unknown name returns `"unknown host \"<name>\""` — do not retry with guesses; ask the user.

- **Blindly refresh known_hosts without verifying the cause.** A key mismatch means the server
  key changed. Before running `ssh-keygen -R` + `ssh-keyscan`, confirm with the user that the
  server was rebuilt or reprovisioned, not compromised. See [references/error-handling.md](references/error-handling.md).

- **Leave sessions open.** Always call `ssh_session_close` — even on error paths. Open sessions
  hold a connection-pool slot until the idle GC evicts them (default: 15 min).

- **Overwrite production files without user confirmation.** `sftp_write` is `O_TRUNC` — the
  original is gone immediately with no backup. Confirm destructive writes explicitly.

- **Never send a new command while `still_running: true`.** Wait for the in-flight command to
  complete first (send empty input).

---

## Limits Cheat Sheet

| Resource | Default | Notes |
|---|---|---|
| Sessions total | 32 | Atomic cap across all hosts |
| Sessions per host | 4 | Shared with SFTP operations |
| Send timeout default | 2 000 ms | Override per-call with `timeout_ms` |
| Send timeout max | 30 000 ms | Hard server cap; higher values reported via `warnings` |
| run_once timeout max | 60 000 ms | Hard server cap; higher values reported via `warnings` |
| Output buffer | 1 MiB / session | Ring buffer; overflow → `truncated: true` |
| run_once stdout/stderr cap | 1 MiB each | `truncated: true` if either stream hits the cap |
| SFTP read limit | 5 MiB | **Silent truncation — no `truncated` flag on sftp_read!** |
| SFTP write limit | 25 MiB | Server-side rejection before any I/O |
| Idle timeout | 15 min | GC closes unused sessions |
| Max session age | 4 h | GC closes regardless of activity |
| `closed_reason` values | `"exit"` `"lost"` `"idle"` `"max_age"` `"shutdown"` `"explicit"` | Non-empty = session is dead |
| Pool idle (after close) | 30 s | Underlying SSH client lingers briefly |

---

## Reference Files

Load these only when the situation calls for it:

| Situation | File |
|---|---|
| Looking up tool parameters or return shapes | [references/tools.md](references/tools.md) |
| Handling a specific error message | [references/error-handling.md](references/error-handling.md) |
| Using any sftp_* tool | [references/sftp.md](references/sftp.md) |
| Handling sudo or privilege escalation | [references/sudo.md](references/sudo.md) |
