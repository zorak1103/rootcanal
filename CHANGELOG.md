# Changelog

## [2.0.0] — 2026-05-20

### Breaking changes

- `ssh_session_send`: `closed: bool` replaced by `closed_reason: string`
- `ssh_session_send`: empty-input now continues waiting for in-flight marker (not drain-idle); use `wait_idle_ms` for peek
- `TERM` defaults to `"dumb"` (was `"xterm-256color"`)
- Output is clean by default (echo/ANSI stripped); pass `raw: true` to opt out

### New tools

- `ssh_run_once` — one-shot SSH exec channel; returns stdout, stderr, exit_code. No PTY, no MOTD.
- `ssh_list_hosts` — list all configured hosts with non-sensitive metadata
- `ssh_host_capabilities` — SSH/SFTP capabilities and session limits for a specific host

### Improvements

- Exit codes available on every `ssh_session_send` result
- MOTD suppressed automatically on session open
- Long-running commands: `still_running: true` + empty-input continuation
- Timeout clamping reported via `warnings` field
- `closed_reason` distinguishes `"exit"` / `"lost"` / `"idle"` / `"max_age"` / `"shutdown"` / `"explicit"`
- Optional client-supplied session names on `ssh_session_open`
- `ssh_session_list` includes `name`, `last_exit_code`, `still_running` per session
