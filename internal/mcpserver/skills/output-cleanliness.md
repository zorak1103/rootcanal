# Output Cleanliness

Understanding how rootcanal delivers command output prevents misreading results and helps
diagnose unexpected behavior.

---

## Marker-Based Completion

`ssh_session_send` is deterministic, not timer-based. After injecting the command, rootcanal
appends a sentinel marker (`RC_EXIT_<nonce>_<code>`) to the shell input. Output is buffered
and returned the moment the marker appears in the stream. There is no polling delay or 50 ms
quiescence window — the response arrives as soon as the shell acknowledges the command finished.

This means:
- Commands that finish quickly return immediately.
- Commands that take minutes block until completion (or until `timeout_ms` expires and
  `still_running: true` is returned).
- Output is byte-accurate up to the point the marker arrived.

---

## Default Stripping

By default, the following are removed from output before it reaches the caller:

- **PTY echo** — the shell echoes back the input line; rootcanal strips it.
- **ANSI escape sequences** — color codes, cursor movement, bold/underline markers.
- **Shell prompt** — the trailing prompt line that appears after each command.

The result is clean, machine-readable output suitable for parsing.

---

## Raw Mode

Pass `raw: true` to `ssh_session_send` to receive unfiltered bytes. Use this when:

- You need exact ANSI output (e.g., capturing a terminal recording).
- You are debugging unexpected stripping behaviour.
- The remote command's output is being mangled by the strip pass.

```
ssh_session_send(session_id="s_abc123", input="ls --color=always\n", raw=true)
  → { output: "\x1b[0;32mfile.txt\x1b[0m\n..." }
```

---

## exit_code

When the completion marker is present, the response includes `exit_code` (integer). A value of
`0` means success; any non-zero value means the command failed. The exit code is extracted
from the sentinel itself, so it is reliable even if the command produces no output.

If `still_running: true`, there is no `exit_code` yet — it will appear in the continuation
response when the marker finally arrives.

---

## truncated

Each session maintains a 1 MiB ring buffer. If a command produces more output than the buffer
holds, older bytes are discarded and `truncated: true` appears in the response. The most
recent output is preserved.

If truncation is a concern, pipe the command's output to a file and read it via
`skill://rootcanal/sftp-and-safety`, or filter the command's output server-side (e.g., `| tail -100`).

---

## closed_reason

A non-empty `closed_reason` means the session is dead and cannot accept further sends. Possible
values:

| Value | Meaning |
|---|---|
| `"exit"` | The remote shell process exited normally. |
| `"lost"` | The underlying SSH connection was lost. |
| `"idle"` | The session was idle for longer than `idle_timeout` (default: 15 min). |
| `"max_age"` | The session exceeded `max_age` (default: 4 h) regardless of activity. |
| `"shutdown"` | rootcanal is shutting down. |
| `"explicit"` | `ssh_session_close` was called. |

When `closed_reason` is set, call `ssh_session_list` to confirm the session is gone, then
reopen if needed. See `skill://rootcanal/session-workflow` for the open/send/close lifecycle.
