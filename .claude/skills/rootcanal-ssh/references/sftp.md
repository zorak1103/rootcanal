# rootcanal SFTP Guide

SFTP is **disabled by default** for every host. Three explicit operator decisions in the config
must be made before the LLM can read or write any file.

---

## Three-Layer Access Control

Every `sftp_read`, `sftp_write`, and `sftp_list` call passes these checks in order:

### Layer 1 — Per-host opt-in (`sftp_enabled`)

The host config must have `sftp_enabled: true`. Without it, all SFTP calls are rejected
immediately with `"host \"<name>\": SFTP not enabled"`, regardless of the path.

### Layer 2 — Path normalisation + absolute enforcement

The LLM-supplied path is passed through `path.Clean` (Unix semantics, independent of the OS
rootcanal runs on). The cleaned path must start with `/`. Relative paths and traversal
sequences (`../`) are rejected with `"path \"...\" must be absolute"`.

### Layer 3 — Prefix allowlist (`sftp_allowed_prefixes`)

The cleaned path must equal one of the configured prefixes exactly, or be a descendant of it.
The check uses a strict separator boundary: prefix `/srv/app` matches `/srv/app` and
`/srv/app/config.yaml`, but **not** `/srv/apple/secret`.

An empty `sftp_allowed_prefixes` list (or absent field) **denies all paths** — even when
`sftp_enabled: true`. This is intentional; the operator must enumerate allowed paths explicitly.

To allow any path: `sftp_allowed_prefixes: ["/"]` — this must be written deliberately.

---

## ⚠️ sftp_read: Silent Truncation

`sftp_read` silently truncates at `sftp_max_read_bytes` (default 5 MiB) using `io.LimitReader`.
There is **no `truncated` flag** in the response — the content just ends.

**Always check file size before reading files that could be large:**
```
sftp_list(host, "/path/to/dir")   → inspect "size" in entries
# or
ssh_session_send: "wc -c /path/to/file\n"
```

**If the file is larger than 5 MiB, use the shell instead:**
```
ssh_session_send: "head -200 /path/to/file\n"
ssh_session_send: "tail -200 /path/to/file\n"
ssh_session_send: "grep -n 'pattern' /path/to/file\n"
```

Or redirect to a smaller temp file and read that:
```
ssh_session_send: "tail -1000 /var/log/app.log > /tmp/recent.log\n"
sftp_read(host, "/tmp/recent.log")
```

---

## Binary Files

**Reading:** If a file contains a NUL byte or invalid UTF-8, `sftp_read` returns:
```json
{ "content": "<base64-encoded-content>", "binary": true, "size": 1234 }
```
Decode the base64 content for further processing.

**Writing binary content:**
1. base64-encode the content (e.g. via Python: `import base64; base64.b64encode(data).decode()`)
2. Pass `binary: true` and the base64 string as `content`

Text files are always safer. Prefer text formats (YAML, TOML, JSON) for config where possible.

---

## File Permissions (`mode`)

The `mode` parameter on `sftp_write` is an **octal string**:
- `"0644"` → `-rw-r--r--` (typical config file)
- `"755"` → `-rwxr-xr-x` (executable script)
- `"0600"` → `-rw-------` (private key, sensitive file)

Leading `0` is stripped internally before parsing. `mode` is applied via `Chmod` **after** the
write succeeds. If `mode` is omitted, the file gets the remote SFTP server's default umask (usually `0644`).

Setuid/setgid and sticky bits (`04000`, `02000`, `01000`) are **rejected** with `"setuid/setgid/sticky bits not permitted"`. Use only standard permission bits (`0777` and below).

---

## SFTP and Session Limits

SFTP operations share the `max_sessions_per_host` counter (default: 4) with SSH sessions.
If 4 SSH sessions are open to a host, SFTP calls to that host will fail with a per-host
session limit error. Close unused SSH sessions first, then retry the SFTP call.

---

## Safe Edit Workflow

```
1. sftp_list(host, "/srv/app")
      → inspect entries, confirm file exists and check size

2. sftp_read(host, "/srv/app/config.yaml")
      → read current content

3. [modify content in context]

4. [optional but recommended: create backup via ssh_session_send]
      "cp /srv/app/config.yaml /srv/app/config.yaml.bak\n"

5. sftp_write(host, "/srv/app/config.yaml", new_content, mode="0644")
      → overwrites immediately (O_TRUNC, no automatic backup)

6. [verify via ssh_session_send]
      "systemctl restart myapp\n"  or  "cat /srv/app/config.yaml\n"
```
