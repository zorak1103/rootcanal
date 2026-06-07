# Changelog

## [Unreleased]

### Added
- `ssh_accept_host_key` MCP tool: preview and re-trust a changed SSH host key after a server rebuild. Requires `allow_known_hosts_update: true` per-host. Two-step flow: preview returns both fingerprints; confirm (with `expected_fingerprint` echo) rewrites the known_hosts entry atomically. Closes #14.
- Per-host `allow_known_hosts_update` config field (default `false`). Enables `ssh_accept_host_key` for that host.
- Key-mismatch error from `ProdDialer` now includes a hint pointing to `ssh_accept_host_key` and `allow_known_hosts_update`.

---

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
