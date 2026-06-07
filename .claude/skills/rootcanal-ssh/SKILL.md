---
name: rootcanal-ssh
description: >
  SSH session management and SFTP file access via the rootcanal MCP server
  (tools: mcp__rootcanal__ssh_session_open, mcp__rootcanal__ssh_session_send,
  mcp__rootcanal__ssh_session_close, mcp__rootcanal__ssh_session_list,
  mcp__rootcanal__ssh_run_once, mcp__rootcanal__ssh_job_status,
  mcp__rootcanal__ssh_job_cancel, mcp__rootcanal__ssh_list_hosts,
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
All tools are restricted to hosts declared in the operator config — the LLM cannot supply
raw addresses or ports.

Respond to the user in whatever language they write in. Technical tool names stay in English.

---

## Host Discovery

Call `ssh_list_hosts` to discover available hosts. Each entry includes name, address, user,
auth_type, sftp_enabled, and sftp_allowed_prefixes. No credentials are ever returned.

To inspect limits and SFTP capabilities for a specific host, call `ssh_host_capabilities`.

> **Note:** `ssh_list_hosts` reflects the config at startup. If the config file changes after
> rootcanal starts, restart rootcanal to pick up the changes. `ssh_host_capabilities` reads
> from the same in-memory config — neither is "more live" than the other.

---

## For One-shot Reads, Use `ssh_run_once`

`ssh_run_once` is the right tool for any command that doesn't need session state (`df`, `ls`,
`cat`, `docker inspect`).

- No open/close ceremony — one call returns stdout, stderr, and exit_code.
- No PTY: no MOTD, no ANSI, no echo. Output is byte-for-byte what the process wrote.
- Requires a POSIX-compatible remote shell (`sh`, `bash`, `zsh`, `dash`, `busybox` — not `fish`/`csh`).

**Timeout control:** `ssh_run_once` defaults to 60 000 ms. Pass `timeout_ms` to impose a shorter deadline:

```
ssh_run_once(host="prod", command="./slow-query.sh", timeout_ms=30000)
```

Values above 60 000 ms are clamped and reported via `warnings`.

---

## Choosing between `ssh_run_once` and `detach=true`

Pick before you run — a mid-run SIGTERM on a write operation can leave state half-applied.

| Operation | Default | Rationale |
|---|---|---|
| One-shot reads (`df`, `ls`, `cat`, `docker ps`) | `ssh_run_once` | Bounded, fast |
| Script/build with predictable, <45 s duration | `ssh_run_once` | Headroom below the 60 s cap |
| DB export/import (`pg_dump`, `mysqldump`, `expdp`) | **`detach=true`** | Volume-dependent; exceeds 60 s |
| Large file copy / `rsync` | **`detach=true`** | Network + size dependent |
| DDL migrations, index rebuilds | **`detach=true`** | Minutes on large tables |
| Package installs (`apt`, `yum`) | **`detach=true`** | Repo latency is unpredictable |

**When in doubt, detach.** The cost of an unnecessary detach is one extra `ssh_job_status`
call; the cost of a mid-run SIGTERM on a write is a corrupted or half-finished operation.
See the SIGTERM behaviour in the **Background Jobs** section and `references/error-handling.md`.

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

- **Read secret files via `sftp_read`.** `sftp_read` returns file contents inline in the
  conversation context — reading `.env` files, credential stores, or SSH private keys exposes
  every secret to Anthropic's infrastructure. Consume secrets at the shell layer instead so
  they never appear in a tool result:
  ```
  ssh_run_once(host="prod", command='mysql -p"$(grep DB_PASS /app/.env | cut -d= -f2)" -e "SELECT 1;"')
  ```
  See [references/sftp.md](references/sftp.md).

- **Invent host names.** Only use names present in the config or returned by `ssh_list_hosts`.
  An unknown name returns `"unknown host \"<name>\""` — do not retry with guesses; ask the user.

- **Blindly re-trust a host on key mismatch.** A key mismatch means the server key changed.
  Use `ssh_accept_host_key` (preview step first, then confirm with user) — never skip the confirmation.
  Requires `allow_known_hosts_update: true` on the host in config. See [references/error-handling.md](references/error-handling.md).

- **Leave sessions open.** Always call `ssh_session_close` — even on error paths. Open sessions
  hold a connection-pool slot until the idle GC evicts them (default: 15 min).

- **Overwrite production files without user confirmation.** `sftp_write` is `O_TRUNC` by default — the
  original is gone immediately with no backup. Confirm destructive writes explicitly.
  Use `atomic: true` to write to a temp file first and rename atomically — safe for live config files.

- **Never send a new command while `still_running: true`.** Wait for the in-flight command to
  complete first (send empty input).

---

## Limits Cheat Sheet

| Resource | Default | Notes |
|---|---|---|
| Sessions total | 32 | Atomic cap across all hosts |
| Sessions per host | 4 | Shared with SFTP operations |
| `ssh_session_send` timeout default | 2 000 ms | Override per-call with `timeout_ms` |
| `ssh_session_send` timeout max | 30 000 ms | Hard server cap; clamped with `warnings` |
| `ssh_run_once` timeout default | 60 000 ms | Same as max — set `timeout_ms` only to impose a **shorter** deadline |
| `ssh_run_once` timeout max | 60 000 ms | Hard server cap; clamped with `warnings` |
| Output buffer | 1 MiB / session | Ring buffer; overflow → `truncated: true` |
| run_once stdout/stderr cap | 1 MiB each | `truncated: true` if either stream hits the cap |
| SFTP read limit | 2 MiB | ⚠️ Returns content inline in LLM context. Binary = base64 (+33% size). Files >500 KB consume significant tokens; >2 MB are impractical. Use `ssh_run_once` + scp for large transfers. |
| SFTP write limit | 25 MiB | Server-side rejection before any I/O |
| Idle timeout | 15 min | GC closes unused sessions |
| Max session age | 4 h | GC closes regardless of activity |
| `closed_reason` values | `"exit"` `"lost"` `"idle"` `"max_age"` `"shutdown"` `"explicit"` | Non-empty = session is dead |
| Pool idle (after close) | 30 s | Underlying SSH client lingers briefly |

---

## Background Jobs

### Native detach (recommended)

For jobs that run longer than `ssh_run_once`'s 60 s maximum, use detach mode:

**Step 1 — start in background:**
```
ssh_run_once(host="prod", command="./backup.sh", detach=true)
→ { "job_id": "j_abc123f4d2e1", "host": "prod" }
```

**Step 2 — poll for completion:**
```
ssh_job_status(job_id="j_abc123f4d2e1")
→ { "running": true, "elapsed_s": 47, "stdout_tail": "Exporting table 3 of 12..." }

ssh_job_status(job_id="j_abc123f4d2e1")
→ { "running": false, "elapsed_s": 183, "exit_code": 0, "stdout_tail": "Backup complete." }
```

**Cancel a running job:**
```
ssh_job_cancel(job_id="j_abc123f4d2e1")
→ { "canceled": true, "was_running": true }
```

Jobs expire 1 hour after completion. After expiry, `ssh_job_status` returns "not found".

### Fallback: nohup pattern (survives rootcanal restart)

The in-memory job registry does not survive rootcanal restarts. For long-running processes that must outlive the MCP server, use the nohup pattern via a persistent session:

```
ssh_session_open(host="prod")                         → { session_id }
ssh_session_send(session_id, "nohup ./long-task.sh &> /tmp/task.log &\n")
ssh_session_send(session_id, "echo $!\n")             → PID
ssh_session_close(session_id)
```

Poll progress via `ssh_run_once` or a new session reading `/tmp/task.log`.

---

## Reference Files

Load these only when the situation calls for it:

| Situation | File |
|---|---|
| Looking up tool parameters or return shapes | [references/tools.md](references/tools.md) |
| Handling a specific error message | [references/error-handling.md](references/error-handling.md) |
| Using any sftp_* tool | [references/sftp.md](references/sftp.md) |
| Handling sudo or privilege escalation | [references/sudo.md](references/sudo.md) |
