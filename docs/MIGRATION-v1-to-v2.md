# rootcanal v1 → v2 Migration Guide

## Breaking changes

### `ssh_session_send` result shape

| v1 field | v2 replacement |
|---|---|
| `closed: bool` | `closed_reason: string` (`""` \| `"exit"` \| `"lost"` \| `"idle"` \| `"max_age"` \| `"shutdown"`) |
| (no exit code) | `exit_code: *int` (nil only when `still_running=true` or `raw=true`) |
| (no still_running) | `still_running: bool` |
| (no warnings) | `warnings: []string` |

Empty-input `Send` semantics: was "drain idle output"; now "continue waiting for the in-flight command's marker". Use `wait_idle_ms` for the v1 peek behavior.

### `ssh_session_close` result shape

v1: returns no structured output.
v2: returns `{ "closed_reason": "explicit" }`.

### `ssh_session_open` adds optional `name`

```json
{ "host": "mynas", "name": "wud-debug" }
```

### Default terminal

`TERM` defaults to `"dumb"` (was `"xterm-256color"`). Programs that respect `$TERM` will produce plain text by default. Pass `raw: true` if you need ANSI.

### Output is clean by default

Echo, ANSI escape sequences, and the shell prompt are now stripped by the server. `raw: true` restores v1 behavior.

## Migration examples

### Check session closed (v1 → v2)

```python
# v1
if result["closed"]:
    open_new_session()

# v2
if result.get("closed_reason"):
    open_new_session()
```

### One-shot reads (v1 → v2)

```python
# v1
id = open_session(host)
output = send(id, "df -h\n")
close_session(id)

# v2 (preferred)
result = run_once(host, "df -h")
# result.stdout, result.exit_code
```

## New tools

- `ssh_run_once` — exec channel, no PTY, returns stdout/stderr/exit_code
- `ssh_list_hosts` — lists all configured hosts (no credentials)
- `ssh_host_capabilities` — SSH/SFTP caps and session limits for a host
