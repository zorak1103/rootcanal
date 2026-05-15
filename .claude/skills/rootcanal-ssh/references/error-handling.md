# rootcanal Error Handling

All errors appear in the `CallToolResult` body with `IsError: true`. Match on sub-strings —
the full format may include wrapping like `"connecting to \"<host>\": SSH handshake failed: %w"`.

---

## Session / Connection Errors

### `"unknown host \"<name>\""`
**Cause:** The host name is not in the rootcanal config.
**Fix:** Check the config file for the correct name. Do not guess alternatives. Ask the user if uncertain.

---

### `"SSH handshake failed: ... knownhosts: key mismatch"`
**Cause:** The server presented a host key that differs from the stored `known_hosts` entry.
**What it means:** The server was rebuilt (new key), the IP was reassigned, or — less likely — a MITM.

**Required action:**
1. **Ask the user** to confirm the cause before modifying `known_hosts`.
2. If confirmed server rebuild/reprovision:
   ```bash
   ssh-keygen -R <host>                         # remove old entry
   ssh-keyscan -H <host> >> ~/.ssh/known_hosts  # accept new key
   ```
3. Retry `ssh_session_open`.

Never run `ssh-keyscan` without user confirmation — accepting a malicious key would be silent.

---

### `"loading known_hosts ..."` / host key absent from known_hosts
**Cause:** The host was never added to `known_hosts`, or the file cannot be read.
**Fix:** `ssh-keyscan -H <host> >> ~/.ssh/known_hosts` — but confirm with the user first (see MITM note above).

---

### `"env var \"...\" is empty or unset"` (password or passphrase)
**Cause:** The environment variable referenced by `password_env` or `passphrase_env` in the
config is not set in the rootcanal process environment.
**Fix:** The user must set the env var and restart the MCP server. Do not ask the user to
share the secret value with you.

---

### `"global session limit of N reached"`
**Cause:** `max_sessions_total` has been hit (default: 32).
**Fix:**
1. Call `ssh_session_list` to find all open sessions.
2. Close sessions that are no longer needed with `ssh_session_close`.
3. Retry `ssh_session_open`.

---

### `"host \"<name>\": per-host session limit of N reached"`
**Cause:** `max_sessions_per_host` hit for that specific host (default: 4; shared with SFTP ops).
**Fix:** Same as above — list and close unused sessions for that host specifically.

---

### `"session \"<id>\" not found"`
**Cause:** The session was explicitly closed, evicted by the idle GC (15 min default),
or reached `max_session_age` (4 h default).
**Fix:** Open a new session. Do not retry with the same `session_id`.
**Tip:** Use `ssh_session_list` to verify which sessions are still live before assuming expiry.

---

### `closed: true` in ssh_session_send response
**Cause:** The remote shell process exited (command error, explicit `exit`, or disconnect).
This is a **flag in the success response** — not an `IsError` error.
**Fix:** Still call `ssh_session_close` (required to release the pool slot), then open a new session.

---

### `truncated: true` in ssh_session_send response
**Cause:** The 1 MiB ring buffer per session overflowed; the oldest bytes were lost.
**Fix options:**
- Redirect verbose output to a remote file: `command > /tmp/out.txt 2>&1\n`, then `sftp_read` it.
- Pipe through `head`/`tail`/`grep` on the remote side to limit what's captured.
- Add `--no-pager` or `--no-color` to reduce verbosity.
- Increase `output_buffer_bytes` in the rootcanal config (per-session; affects all hosts).

---

### `"manager is shutting down"`
**Cause:** The rootcanal server process is stopping.
**Fix:** Restart the MCP server (restart Claude Code or reload the MCP config in settings).

---

## SFTP Errors

### `"host \"<name>\": SFTP not enabled"`
**Cause:** The host config does not have `sftp_enabled: true`.
**Fix:** The operator must update the rootcanal config. Cannot be changed at runtime via MCP.

---

### `"path \"<p>\" must be absolute"`
**Cause:** The supplied path does not start with `/` after `path.Clean`.
**Fix:** Use absolute Unix paths only. Relative paths and `../` traversals are always rejected.

---

### `"path \"<p>\" is not under any allowed prefix"`
**Cause:** The path is outside all configured `sftp_allowed_prefixes` for this host.
An empty `sftp_allowed_prefixes` list denies **all** paths (even with `sftp_enabled: true`).
**Fix:** Ask the user to extend `sftp_allowed_prefixes` in the config, or use a path
that is already under an allowed prefix.

---

### `"content size N exceeds SFTP write limit of M bytes"`
**Cause:** Write content exceeds `sftp_max_write_bytes` (default 25 MiB).
**Fix:** Split the transfer, or use `ssh_session_send` + remote tools (`tee`, `base64 -d`) to write the file.

---

### `"base64 decode: ..."` on sftp_write
**Cause:** `binary: true` was set but the `content` is not valid base64.
**Fix:** Re-encode the content as base64 before passing it, or set `binary: false` for text files.
