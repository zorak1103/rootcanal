# rootcanal Tool Reference

All tools return errors as a `CallToolResult` with `IsError: true` — the Go-level error is always nil.
Check for `IsError` (or error-pattern text) to distinguish success from failure. Error strings
are deterministic; sub-string matching is safe.

---

## ssh_list_hosts

Lists all configured hosts with non-sensitive metadata.

No parameters.

**Success:**
```json
{
  "hosts": [
    {
      "name": "prod-web",
      "description": "Production web server",
      "address": "prod-web.example.com:22",
      "user": "deploy",
      "auth_type": "key",
      "sftp_enabled": true
    }
  ]
}
```
`description` is omitted when the host config has no `description` field.

Hosts are sorted alphabetically by name. Credentials never appear.

---

## ssh_host_capabilities

Returns SSH/SFTP capabilities and session limits for a specific host.

| Parameter | Type | Required | Notes |
|---|---|---|---|
| `host` | string | yes | Config key (e.g. `"prod-web"`). |

**Success:**
```json
{
  "ssh": true,
  "sftp": false,
  "sftp_allowed_prefixes": [],
  "idle_timeout_ms": 900000,
  "max_session_age_ms": 14400000,
  "term": "dumb",
  "clean_output": true
}
```
`term` and `clean_output` are omitted only when both match the global defaults (unusual).

**Errors:** `"unknown host \"<name>\""` if not in config.

---

## ssh_run_once

Executes a command on the remote host via a non-PTY exec channel. No session open/close required.

| Parameter | Type | Required | Notes |
|---|---|---|---|
| `host` | string | yes | Config key (e.g. `"prod-web"`). Not a raw IP or hostname. |
| `command` | string | yes | Shell command string passed to the remote shell. Requires POSIX-compatible shell (`sh`, `bash`, `zsh`, `dash`, `busybox`). |
| `stdin` | string | no | Data to pipe to the command's stdin. |
| `env` | object | no | Key/value environment variables. May be silently rejected by remote `AcceptEnv` policy. |
| `timeout_ms` | int | no | 0 → server default (config `run_once_max_timeout_ms`, default 60 000 ms). Values above the server cap are clamped and reported via `warnings`. |

**Success:**
```json
{
  "stdout": "Filesystem      Size  Used Avail Use% Mounted on\n...",
  "stderr": "",
  "exit_code": 0,
  "signal": "",
  "truncated": false,
  "warnings": []
}
```

- `exit_code`: always present. `0` = success, non-zero = failure, `-1` = killed by signal.
- `signal`: non-empty when process was killed by a signal (e.g. `"TERM"`).
- `truncated`: `true` if stdout OR stderr hit the per-stream cap (default 1 MiB each).
- `warnings`: advisory messages, e.g. `"timeout_ms clamped from 60001 to 60000"`, `"setenv FOO: server rejected"`.

**Errors:**
- `"unknown host \"<name>\""` — name not in config
- SSH handshake / knownhosts errors (see error-handling.md)

**No PTY:** no MOTD, no ANSI escape sequences, no echo. Output is byte-for-byte what the process wrote.

---

## ssh_session_open

Opens a persistent PTY shell on the configured host.

| Parameter | Type | Required | Notes |
|---|---|---|---|
| `host` | string | yes | Config key (e.g. `"prod-web"`). Not a raw IP or hostname. |
| `name` | string | no | Client-supplied session name. Must match `^[a-z0-9][a-z0-9._-]{0,62}$`. Must not start with `s_`. |

**Success:**
```json
{ "session_id": "s_XXXXXXXX" }
```
If `name` was supplied, `session_id` contains the client-supplied name instead of an auto-generated ID.

**Errors:**
- `"unknown host \"<name>\""` — name not in config
- SSH handshake / knownhosts errors (see error-handling.md)
- `"global session limit of N reached"` — max_sessions_total hit
- `"host \"<name>\": per-host session limit of N reached"` — max_sessions_per_host hit
- `"manager is shutting down"`

**Internals:** Allocates a PTY (`xterm-256color`, 40×120), requests a shell. The `$TERM` environment variable advertised to the remote shell is set from the host's `term` config field (default `"dumb"` — not to be confused with the PTY type). Auth and known_hosts verification happen here — not on send. Connection is pooled per host (30 s idle timer after all sessions for that host are closed). MOTD is suppressed automatically on open.

---

## ssh_session_send

Writes input to the shell's stdin and waits for output.

| Parameter | Type | Required | Notes |
|---|---|---|---|
| `session_id` | string | yes | From `ssh_session_open` |
| `input` | string | yes | Command to send (include `\n`). Pass `""` to continue waiting for the in-flight command's marker. |
| `timeout_ms` | int | no | 0 → server default (2 000 ms). Hard cap: 30 000 ms. Values above cap are clamped and reported via `warnings`. |
| `raw` | bool | no | Default `false`. Pass `true` to skip marker injection, ANSI stripping, and echo removal. |
| `wait_idle_ms` | int | no | Peek mode: return after this many ms of silence (TUI/REPL use). Mutually exclusive with non-empty `input`. |

**Success:**
```json
{
  "output": "...",
  "exit_code": 0,
  "still_running": false,
  "truncated": false,
  "closed_reason": "",
  "warnings": []
}
```

- `output`: Clean text (echo/ANSI stripped) by default; raw bytes if `raw: true`.
- `exit_code`: The shell exit code of the completed command. `nil`/absent when `still_running: true` or `raw: true`.
- `still_running: true`: `timeout_ms` elapsed before the command completed. Send empty input to continue waiting.
- `truncated: true`: Ring buffer (default 1 MiB) overflowed; oldest bytes lost.
- `closed_reason`: Non-empty string means the session is dead. Values: `"exit"` `"lost"` `"idle"` `"max_age"` `"shutdown"`. Call `ssh_session_close` then open a new session.
- `warnings`: Advisory messages, e.g. `"timeout_ms clamped from 60001 to 30000"`.

**Returns when:** Completion marker received (default) OR `timeout_ms` elapsed OR context cancelled.

**Errors:**
- `"session \"<id>\" not found"` — expired or already closed
- `"command still in flight; send empty input to continue waiting"` — attempted to send a new command while `still_running: true`

**Note:** Send is serialised per session — concurrent sends to the same `session_id` are queued.

**BREAKING from v1:** `closed: bool` removed — use `closed_reason != ""`. Empty input now
continues waiting for the in-flight command's marker (not drain-idle). Use `wait_idle_ms` for
the v1 peek behavior.

---

## ssh_session_close

Closes a session and releases the connection pool slot.

| Parameter | Type | Required | Notes |
|---|---|---|---|
| `session_id` | string | yes | |

**Success:**
```json
{ "closed_reason": "explicit" }
```

**Errors:** None (close is best-effort; calling on an already-closed session is safe).

**Internals:** Closes stdin + SSH session, decrements per-host refcount. The underlying SSH
client lingers for ~30 s after refcount hits 0 before the TCP connection is dropped.

---

## ssh_session_list

Lists all currently open sessions.

No parameters.

**Success:**
```json
{
  "sessions": [
    {
      "session_id": "s_XXXXXXXX",
      "name": "mytest",
      "host": "prod-web",
      "opened_at": "2026-05-15T10:00:00Z",
      "last_used_at": "2026-05-15T10:05:00Z",
      "last_exit_code": 0,
      "still_running": false
    }
  ]
}
```

- `name`: Client-supplied name if set on open; otherwise absent.
- `last_exit_code`: Most recent `ssh_session_send` exit code.
- `still_running`: `true` if a `Send` is currently in flight for this session.

Use to identify leaked or stale sessions when approaching session limits or after
unexpected "session not found" errors.

---

## sftp_read

Reads a file from the remote host via SFTP.

| Parameter | Type | Required | Notes |
|---|---|---|---|
| `host` | string | yes | Must have `sftp_enabled: true` in config |
| `path` | string | yes | Absolute Unix path (e.g. `/srv/app/config.yaml`) |
| `max_bytes` | int | no | Capped by server at `sftp_max_read_bytes` (default 2 MiB). 0 → server cap. |

**Success:**
```json
{ "content": "file text here", "size": 1234 }
// Binary files:
{ "content": "<base64>", "binary": true, "size": 1234 }
```

⚠️ **Silent truncation:** If the file exceeds `max_bytes` / `sftp_max_read_bytes`, content is
silently cut off — there is **no `truncated` flag**. Always check size via `sftp_list` first
for files that might be large. See [sftp.md](sftp.md) for full guidance.

**Errors:** Access control violations (see error-handling.md for SFTP-specific errors).

---

## sftp_write

Creates or overwrites a file on the remote host via SFTP.

| Parameter | Type | Required | Notes |
|---|---|---|---|
| `host` | string | yes | Must have `sftp_enabled: true` in config |
| `path` | string | yes | Absolute Unix path |
| `content` | string | yes | UTF-8 text, or base64-encoded when `binary: true` |
| `binary` | bool | no | Default `false`. Set `true` for binary content (base64 `content`). |
| `mode` | string | no | Octal permission string: `"0644"`, `"755"`. Applied via `Chmod` after write. |

**Success:** text `"Written N bytes to host:path"`

**Errors:**
- Content > `sftp_max_write_bytes` (25 MiB) → rejected before any I/O
- `"invalid mode \"...\": ..."` — bad octal format
- `"setuid/setgid/sticky bits not permitted"` — mode has setuid (04000), setgid (02000), or sticky (01000) bit set
- Access control violations (see error-handling.md)

⚠️ Write uses `O_WRONLY|O_CREATE|O_TRUNC` — existing files are **overwritten immediately with
no backup**. Confirm destructive writes with the user before proceeding.

---

## sftp_list

Lists directory contents on the remote host via SFTP.

| Parameter | Type | Required | Notes |
|---|---|---|---|
| `host` | string | yes | Must have `sftp_enabled: true` in config |
| `path` | string | yes | Absolute Unix path to directory |

**Success:**
```json
{
  "path": "/srv/app",
  "entries": [
    { "name": "config.yaml", "size": 4096, "mode": "-rw-r--r--", "mod_time": "2026-05-15T09:00:00Z" },
    { "name": "logs", "size": 0, "mode": "drwxr-xr-x", "mod_time": "2026-05-15T09:00:00Z", "is_dir": true }
  ]
}
```

Use `size` from entries to check file sizes before `sftp_read` to avoid silent truncation.
