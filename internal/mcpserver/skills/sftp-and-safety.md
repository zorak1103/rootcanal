# SFTP and Safety

rootcanal exposes three SFTP tools. All are gated by host-level configuration — SFTP must be
explicitly enabled per host, and every path is checked against `sftp_allowed_prefixes`.

---

## Tools

### sftp_read

Read a remote file and return its content inline.

```
sftp_read(host="prod", path="/etc/nginx/nginx.conf")
  → { content: "worker_processes auto;\n...", binary: false }
```

Binary files are base64-encoded and returned with `binary: true`. Files larger than 500 KB
consume significant LLM context tokens; files approaching 2 MiB are impractical to reason
about inline. For large files, prefer `ssh_run_once` with filtering (e.g., `grep`, `head`,
`tail`) rather than reading the full content.

**Read limit: 2 MiB.** The server rejects reads before any I/O if the file exceeds this.

### sftp_write

Write content to a remote file.

```
sftp_write(host="prod", path="/etc/app/config.yaml", content="key: value\n")
  → { written: true }
```

By default, `sftp_write` opens the file with `O_TRUNC` — the original is gone immediately
with no backup. Always confirm destructive writes with the user before proceeding.

**Atomic write (`atomic: true`):** rootcanal writes to a temporary file in the same directory,
then renames it over the target. Use this for live configuration files where a partial write
would be catastrophic:

```
sftp_write(host="prod", path="/etc/app/config.yaml", content="...", atomic=true)
```

**Write limit: 25 MiB.** The server rejects writes before any I/O if the content exceeds this.

### sftp_list

List a remote directory.

```
sftp_list(host="prod", path="/var/log/app/")
  → [{ name: "app.log", size: 1048576, is_dir: false, modified: "2024-01-15T12:00:00Z" }]
```

---

## Allowed Prefixes

Every SFTP path is checked against `sftp_allowed_prefixes` configured for the host.

- An empty prefix list (`[]`) denies **all** paths — SFTP is effectively disabled even if
  `sftp_enabled: true`.
- A prefix of `["/"]` permits any absolute path.
- A prefix of `["/var/app", "/etc/app"]` restricts access to those subtrees only.

Paths are cleaned (`path.Clean`) before matching. Non-absolute results are rejected regardless
of prefixes. Call `ssh_host_capabilities` to inspect the configured prefixes for a host before
attempting SFTP operations.

---

## Hard Rules

These are absolute prohibitions. Never do any of the following:

**Never pass a sudo password in conversation or prompt context.** Passwords in chat messages
travel to Anthropic's infrastructure in plaintext and may appear in conversation logs. There is
no safe way to pass credentials through the LLM context layer.

**Never invent host names.** Only use names present in the operator config or returned by
`ssh_list_hosts`. An unknown name returns `"unknown host \"<name>\""` — do not retry with
guesses. Ask the user which host they meant.

**Never blindly refresh known_hosts on a key mismatch.** A host key mismatch means the server
key changed. Before running `ssh-keygen -R` and `ssh-keyscan`, confirm with the user that the
server was rebuilt or reprovisioned. A mismatch you cannot explain may indicate a compromised
server or a man-in-the-middle condition.

**Always call ssh_session_close, even on error paths.** Open sessions hold a connection-pool
slot until the idle GC evicts them (default: 15 min). Leaked sessions consume capacity from
the 32-session and 4-per-host limits.

**Always confirm destructive sftp_write before overwriting.** `sftp_write` is `O_TRUNC` by
default — there is no backup and no undo. Use `atomic: true` when writing live configuration
files to avoid a corrupt intermediate state.

**Never send a new command while still_running: true.** The in-flight command has not finished.
Send empty input (`input=""`) to continue waiting, or wait for `still_running` to clear. Sending
a new command while the session is blocked returns an error.

---

## Error Recovery

| Error message | Cause | Recovery |
|---|---|---|
| `"session not found"` | Session expired or was closed. | Call `ssh_session_list` to inventory open sessions; reopen if needed. |
| `"unknown host \"<name>\""` | Host name not in config. | Call `ssh_list_hosts` to see valid names; do not guess. |
| `"host key mismatch"` | Remote host key changed. | Confirm server identity with user before refreshing `known_hosts`. |
| `"path not allowed"` | Path outside `sftp_allowed_prefixes`. | Call `ssh_host_capabilities` to check configured prefixes. |
| `"sftp not enabled"` | Host has `sftp_enabled: false`. | Inform user; no workaround available without config change. |

For session lifecycle details, see `skill://rootcanal/session-workflow`.
For output and exit code semantics, see `skill://rootcanal/output-cleanliness`.
