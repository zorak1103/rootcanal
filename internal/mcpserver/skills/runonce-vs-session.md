# RunOnce vs Session

Choose the right tool upfront — switching mid-task wastes time and open sessions.

---

## Use ssh_run_once When

- The command is stateless: `df`, `ls`, `cat`, `docker inspect`, `systemctl status`.
- You do not need environment variables or working-directory state carried between calls.
- The command is POSIX-compatible (`sh`, `bash`, `zsh`, `dash`, `busybox sh`). `fish` and `csh`
  are not supported.
- You want clean output with no PTY noise: `ssh_run_once` returns byte-for-byte stdout and
  stderr with no echo, no ANSI, no prompt.

```
ssh_run_once(host="prod", command="df -h /")
  → { stdout: "Filesystem  Size  Used ...", stderr: "", exit_code: 0 }
```

Default timeout is 60 000 ms. Pass `timeout_ms` only to impose a **shorter** deadline; values
above 60 000 ms are clamped and reported in `warnings`.

## Use ssh_session_open When

- You need interactive state: `cd`, `export`, `source`, activating virtualenvs, etc.
- The command is a long-running process or TUI program (`vim`, `top`, `htop`, interactive REPL).
- You are running a sequence of commands where earlier commands affect later ones.

See `skill://rootcanal/session-workflow` for the full open/send/close lifecycle.

---

## Detach Mode (Background Jobs)

For jobs that run longer than 60 s, use `detach=true` on `ssh_run_once`. rootcanal starts the
process via an SSH exec channel and returns a `job_id` immediately.

**Start:**
```
ssh_run_once(host="prod", command="./backup.sh", detach=true)
  → { job_id: "j_abc123f4d2e1", host: "prod" }
```

**Poll:**
```
ssh_job_status(job_id="j_abc123f4d2e1")
  → { running: true, elapsed_s: 47, stdout_tail: "Exporting table 3 of 12..." }

ssh_job_status(job_id="j_abc123f4d2e1")
  → { running: false, elapsed_s: 183, exit_code: 0, stdout_tail: "Backup complete." }
```

**Cancel:**
```
ssh_job_cancel(job_id="j_abc123f4d2e1")
  → { canceled: true, was_running: true }
```

Jobs expire 1 hour after completion. After expiry, `ssh_job_status` returns "not found".

---

## Nohup Fallback (Survives rootcanal Restart)

The in-memory job registry does not survive rootcanal restarts. For long-running processes
that must outlive the MCP server, use nohup via a persistent session:

```
ssh_session_open(host="prod")
  → { session_id: "s_xyz789" }

ssh_session_send(session_id="s_xyz789", input="nohup ./long-task.sh &> /tmp/task.log &\n")
ssh_session_send(session_id="s_xyz789", input="echo $!\n")
  → { output: "12345" }   ← PID for later monitoring

ssh_session_close(session_id="s_xyz789")
```

Poll progress via `ssh_run_once` reading `/tmp/task.log`, or open a new session to check
process state.

---

## Resource Limits

| Resource | Default | Notes |
|---|---|---|
| Sessions total | 32 | Atomic cap across all hosts |
| Sessions per host | 4 | Shared with SFTP operations |
| `ssh_session_send` timeout default | 2 000 ms | Override per-call with `timeout_ms` |
| `ssh_session_send` timeout max | 30 000 ms | Hard cap; clamped with `warnings` |
| `ssh_run_once` timeout default | 60 000 ms | Same as max — pass `timeout_ms` only to shorten |
| `ssh_run_once` timeout max | 60 000 ms | Hard cap; clamped with `warnings` |
| Output buffer | 1 MiB / session | Ring buffer; overflow → `truncated: true` |
| `ssh_run_once` stdout/stderr cap | 1 MiB per stream | Each stream is independently capped at `run_once_max_bytes`; `truncated: true` if either hits the cap |
| SFTP read limit | 2 MiB | Content returned inline in LLM context |
| SFTP write limit | 25 MiB | Server-side rejection before any I/O |
| Idle timeout | 15 min | GC closes unused sessions |
| Max session age | 4 h | GC closes regardless of activity |
| Pool idle (after close) | 30 s | Underlying SSH client lingers briefly |

For SFTP-specific limits and safety rules, see `skill://rootcanal/sftp-and-safety`.
