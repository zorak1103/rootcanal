# Session Workflow

Persistent PTY-based sessions are the right tool when you need interactive state: environment
variables, working directory, long-running commands, or TUI programs. For stateless one-shot
commands, see `skill://rootcanal/runonce-vs-session`.

---

## Lifecycle

Every session follows this exact three-step sequence:

```
ssh_session_open(host="<name>")
  → { session_id: "s_abc123" }

ssh_session_send(session_id="s_abc123", input="your command\n", timeout_ms=5000)
  → { output: "...", exit_code: 0, still_running: false, truncated: false }

ssh_session_close(session_id="s_abc123")
  → always call, even on error paths
```

**Trailing `\n` is required.** The shell does not execute a line without it.

---

## Parallel N-Host Pattern

When running the same operation across multiple hosts, open all sessions first, then send
in parallel, then close in parallel. This is significantly faster than sequential host-by-host
execution.

```
Message 1 (parallel): ssh_session_open × N hosts
Message 2 (parallel): ssh_session_send × N sessions
Message 3 (parallel): ssh_session_close × N sessions
```

---

## still_running Continuation

When a command exceeds `timeout_ms`, the response includes `still_running: true`. Continue
waiting by sending empty input — do **not** send a new command while `still_running` is true,
as this returns an error.

```
ssh_session_send(session_id="s_abc123", input="./build.sh\n", timeout_ms=5000)
  → { still_running: true, output: "Compiling..." }

ssh_session_send(session_id="s_abc123", input="", timeout_ms=30000)
  → { exit_code: 0, output: "Build complete." }
```

Empty input with no `wait_idle_ms` continues waiting for the in-flight command's completion
marker. The session blocks further sends until the marker arrives.

---

## TUI / REPL Peeking

For interactive programs like `vim` or `top` that never emit the completion marker, use
`wait_idle_ms` instead of empty input. This returns output after the specified milliseconds
of quiescence rather than waiting for a marker:

```
ssh_session_send(session_id="s_abc123", input="top\n", wait_idle_ms=500)
  → { output: "<rendered top screen>", still_running: true }
```

---

## Session Inventory

Call `ssh_session_list` to inspect all currently open sessions. Use this when approaching
session limits (32 total, 4 per host) or after a "session not found" error to determine
whether a session needs to be reopened.

```
ssh_session_list()
  → [{ session_id, host, open_duration_s, idle_s, last_exit_code }]
```

For output behaviour, stripping, exit codes, and `closed_reason` values, see
`skill://rootcanal/output-cleanliness`.
