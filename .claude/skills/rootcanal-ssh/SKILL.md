---
name: rootcanal-ssh
description: >
  SSH session management and SFTP file access via the rootcanal MCP server
  (tools: mcp__rootcanal__ssh_session_open, mcp__rootcanal__ssh_session_send,
  mcp__rootcanal__ssh_session_close, mcp__rootcanal__ssh_session_list,
  mcp__rootcanal__sftp_read, mcp__rootcanal__sftp_write, mcp__rootcanal__sftp_list).
  Use whenever the user asks to: connect to an SSH host, run remote shell commands,
  check server state, read or write remote files, or whenever any mcp__rootcanal__*
  tool is involved. Hosts come from the config file; read it or ask the user.
  Also triggers on: "SSH session", "remote shell", "SFTP", "remote file",
  "run on server", mcp__rootcanal tool errors.
---

# rootcanal SSH

rootcanal is a stdio MCP server for persistent PTY-based SSH sessions and SFTP file access.
All 7 tools are restricted to hosts declared in the operator config — the LLM cannot supply
raw addresses or ports. There is no single-shot exec; the canonical pattern is open → send → close.

Respond to the user in whatever language they write in. Technical tool names stay in English.

---

## Host Discovery

rootcanal has **no list-hosts tool**. Available hosts come from the config file.

**Config search order:**
1. `./rootcanal.yaml`
2. `$XDG_CONFIG_HOME/rootcanal/config.yaml`
3. `$HOME/.config/rootcanal/config.yaml`

**Procedure:** Read the config file to extract host names, then verify with `ssh_session_open`.
If the config path is unknown, ask the user.

---

## Session Workflow

Always follow this exact sequence:

```
1. ssh_session_open(host)                       → { session_id }
2. ssh_session_send(session_id, "cmd\n", ...)   → { output, truncated?, closed? }
   (repeat with input="" to drain further output if needed)
3. ssh_session_close(session_id)                → always, even after errors
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

## Output Framing

Five facts to internalize before parsing any command output:

1. **Returns on quiesce, not on completion.** `ssh_session_send` returns after 50 ms of silence
   from the remote, or when `timeout_ms` elapses — whichever comes first. Long-running commands
   may return mid-output. Poll by calling `ssh_session_send(id, "")` (empty input) to drain.

2. **PTY echo is ON.** The input you sent appears at the start of output. Skip it when parsing —
   look for the shell prompt (`$`, `#`, or the `user@host:~$` pattern) to find where your output begins.

3. **ANSI codes are preserved.** Color, cursor-movement, and bold escape sequences remain in the
   raw output. Filter with `grep -oP`, or prefer `--no-color` / `TERM=dumb` flags on the remote:
   `git --no-pager log --oneline`, `ls --color=never`, `systemctl --no-pager status`.

4. **`truncated: true` = ring buffer overflow.** The 1 MiB per-session buffer was overrun;
   oldest bytes are lost. Remediation: redirect output to a file on the remote and fetch via
   `sftp_read`, or pipe through `head`/`tail`/`grep` before capture.

5. **`closed: true` = remote shell exited.** The session is dead. Still call `ssh_session_close`
   (required to release the pool slot), then open a new session.

---

## Hard Rules

**NEVER do any of the following:**

- **Pass a sudo password in conversation or prompt context.** It travels to Anthropic's
  infrastructure in plaintext and may appear in conversation logs. See [references/sudo.md](references/sudo.md).

- **Invent host names.** Only use names present in the config. An unknown name returns
  `"unknown host \"<name>\""` — do not retry with guesses; ask the user.

- **Blindly refresh known_hosts without verifying the cause.** A key mismatch means the server
  key changed. Before running `ssh-keygen -R` + `ssh-keyscan`, confirm with the user that the
  server was rebuilt or reprovisioned, not compromised. See [references/error-handling.md](references/error-handling.md).

- **Leave sessions open.** Always call `ssh_session_close` — even on error paths. Open sessions
  hold a connection-pool slot until the idle GC evicts them (default: 15 min).

- **Overwrite production files without user confirmation.** `sftp_write` is `O_TRUNC` — the
  original is gone immediately with no backup. Confirm destructive writes explicitly.

---

## Limits Cheat Sheet

| Resource | Default | Notes |
|---|---|---|
| Sessions total | 32 | Atomic cap across all hosts |
| Sessions per host | 4 | Shared with SFTP operations |
| Send timeout default | 2 000 ms | Override per-call with `timeout_ms` |
| Send timeout max | 30 000 ms | Hard server cap; higher values silently clamped |
| Output buffer | 1 MiB / session | Ring buffer; overflow → `truncated: true` |
| SFTP read limit | 5 MiB | **Silent truncation — no `truncated` flag on sftp_read!** |
| SFTP write limit | 25 MiB | Server-side rejection before any I/O |
| Idle timeout | 15 min | GC closes unused sessions |
| Max session age | 4 h | GC closes regardless of activity |
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
