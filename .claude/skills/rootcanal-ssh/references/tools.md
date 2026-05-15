# rootcanal Tool Reference

All tools return errors as a `CallToolResult` with `IsError: true` — the Go-level error is always nil.
Check for `IsError` (or error-pattern text) to distinguish success from failure. Error strings
are deterministic; sub-string matching is safe.

---

## ssh_session_open

Opens a persistent PTY shell on the configured host.

| Parameter | Type | Required | Notes |
|---|---|---|---|
| `host` | string | yes | Config key (e.g. `"prod-web"`). Not a raw IP or hostname. |

**Success:**
```json
{ "session_id": "s_XXXXXXXX" }
```
Also returns text: `"Session opened: s_XXXXXXXX"`

**Errors:**
- `"unknown host \"<name>\""` — name not in config
- SSH handshake / knownhosts errors (see error-handling.md)
- `"global session limit of N reached"` — max_sessions_total hit
- `"host \"<name>\": per-host session limit of N reached"` — max_sessions_per_host hit
- `"manager is shutting down"`

**Internals:** Allocates a PTY (`xterm-256color`, 40×120), requests a shell. Auth and
known_hosts verification happen here — not on send. Connection is pooled per host (30 s
idle timer after all sessions for that host are closed).

---

## ssh_session_send

Writes input to the shell's stdin and waits for output.

| Parameter | Type | Required | Notes |
|---|---|---|---|
| `session_id` | string | yes | From `ssh_session_open` |
| `input` | string | yes | Pass `""` to poll without sending. Include `\n` to submit commands. |
| `timeout_ms` | int | no | 0 → server default (2000 ms). Negative or > 86 400 000 → coerced to 0. Hard cap: 30 000 ms. |

**Success:**
```json
{ "output": "...", "truncated": false, "closed": false }
```
- `output`: All bytes received during the quiesce window. Invalid UTF-8 → U+FFFD. ANSI codes preserved.
- `truncated: true`: Ring buffer (default 1 MiB) overflowed; oldest bytes lost.
- `closed: true`: Remote shell has exited. Session is dead — close and open a new one.

**Returns when:** 50 ms of silence from remote (quiesce) OR `timeout_ms` elapsed OR context cancelled.

**Errors:** `"session \"<id>\" not found"` (expired or already closed)

**Note:** Send is serialised per session — concurrent sends to the same `session_id` are queued.

---

## ssh_session_close

Closes a session and releases the connection pool slot.

| Parameter | Type | Required | Notes |
|---|---|---|---|
| `session_id` | string | yes | |

**Success:** text `"Session <id> closed."`

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
      "host": "prod-web",
      "opened_at": "2026-05-15T10:00:00Z",
      "last_used_at": "2026-05-15T10:05:00Z"
    }
  ]
}
```

Use to identify leaked or stale sessions when approaching session limits or after
unexpected "session not found" errors.

---

## sftp_read

Reads a file from the remote host via SFTP.

| Parameter | Type | Required | Notes |
|---|---|---|---|
| `host` | string | yes | Must have `sftp_enabled: true` in config |
| `path` | string | yes | Absolute Unix path (e.g. `/srv/app/config.yaml`) |
| `max_bytes` | int | no | Capped by server at `sftp_max_read_bytes` (default 5 MiB). 0 → server cap. |

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
