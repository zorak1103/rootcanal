# rootcanal — Security Model

This document describes the security boundaries, threat model, and design decisions that rootcanal makes to run SSH operations safely in the context of an LLM-driven tool.

## Threat model

The principal threat is: a confused or jailbroken LLM sends tool arguments that cause rootcanal to do something the operator did not intend.

rootcanal is not designed to resist a malicious *operator* (the person who writes the config file). If you add a host to the config, you are granting full filesystem and shell access at that host's user level. The security model protects against unintended LLM behaviour. A hostile operator is out of scope.

## Security boundaries

### 1. Host allowlist

The LLM passes a host **name** (e.g. `"prod-web"`) to every tool. rootcanal looks up the name in the config and uses the stored address; the LLM has no way to supply a raw network address or port. This removes a broad class of misuse: the LLM being convinced to connect to an arbitrary endpoint.

An unknown host name returns a structured tool error visible to the LLM.

### 2. Strict host-key verification

Every host must have a `known_hosts` entry. rootcanal uses `golang.org/x/crypto/ssh/knownhosts` with no fallback. The two possible error conditions are surfaced distinctly:

- **Host key mismatch**: the server presented a key that does not match the stored entry. rootcanal refuses to connect and returns a structured error. This detects MITM attacks or key rotation that was not acknowledged by the operator.
- **Host not in known_hosts**: the host's key has never been stored. rootcanal refuses to connect. There is no "trust on first use" (TOFU) mode.

`InsecureIgnoreHostKey` is not exposed as an option.

### 3. No plaintext secrets in config

The config schema has `password_env` and `passphrase_env` fields (environment variable *names*), not `password` or `passphrase` fields (values). The YAML decoder is configured with `KnownFields(true)` so any attempt to add a `password: secret` key is rejected at parse time with an explicit error.

OS keyring (`go-keyring`) support is planned for v1.1, with backends for Windows Credential Manager, macOS Keychain and Linux Secret Service. The config shape is already reserved as `password_keyring: rootcanal/<host>` to avoid breaking changes.

### 4. Resource caps

The config `limits` section enforces:

| Limit | Purpose |
|---|---|
| `max_sessions_total` | Caps goroutines and memory (one ring buffer per session) |
| `max_sessions_per_host` | Prevents one host monopolising the connection pool |
| `sftp_max_read_bytes` | Caps memory allocated per `sftp_read` call |
| `sftp_max_write_bytes` | Prevents the LLM writing arbitrarily large files |

Both `max_sessions_total` and `max_sessions_per_host` are enforced atomically: the limit check and the slot reservation happen under the same mutex, before the SSH dial begins. Concurrent `ssh_session_open` calls cannot collectively bypass the cap even when the dial takes hundreds of milliseconds.

The idle GC (`default_idle_timeout`, `max_session_age`) reclaims resources from abandoned sessions without operator intervention.

### 5. Bounded output buffer

Each session has a ring buffer (`output_buffer_bytes`, default 1 MiB). If the remote shell produces output faster than it is consumed, oldest bytes are overwritten and the `truncated: true` flag is set in the `ssh_session_send` response so the LLM knows output was lost.

### 6. UTF-8 enforcement

MCP uses JSON, which requires valid UTF-8. All session output is passed through `strings.ToValidUTF8` before being returned; invalid byte sequences are replaced with U+FFFD. ANSI escape codes and other control characters are **preserved** because stripping them would misrepresent what the remote shell actually sent.

SFTP binary files are base64-encoded and returned with `binary: true` so they can be transferred through the JSON transport without modification.

### 7. stdio discipline

rootcanal uses the stdio MCP transport. The MCP client reads **stdout** and writes to **stdin**. Writing anything to stdout from the server side would corrupt the JSON-RPC stream.

rootcanal enforces this at two levels:
- All logging before the MCP session is established goes to **stderr**.
- Once the session handshake completes, logging is routed through `mcp.NewLoggingHandler` which sends `notifications/message` events to the client.

No `fmt.Println` or `os.Stdout` write exists outside `cmd/rootcanal/main.go`, where they are gated on flags (`-version`, `-validate-config`, `-probe`) that exit before the MCP transport is started.

### 8. SFTP access control

SFTP is disabled per-host by default and requires three explicit operator decisions before the LLM can read or write any file.

**Layer 1 — per-host opt-in (`sftp_enabled`)**

Each host in the config must carry `sftp_enabled: true` before any SFTP tool call is accepted. Hosts without this field (or with `sftp_enabled: false`) return a structured "SFTP not enabled" error to the LLM regardless of what path is requested.

**Layer 2 — path normalisation and absolute-path enforcement**

Every LLM-supplied path is passed through `path.Clean` (Unix semantics, independent of the OS rootcanal runs on) before being forwarded to the remote SFTP server. The cleaned path must be absolute (start with `/`). Relative paths and traversal sequences such as `../` are rejected with a structured error.

**Layer 3 — per-host path allowlist (`sftp_allowed_prefixes`)**

`sftp_allowed_prefixes` is a required list of absolute Unix path prefixes. The cleaned path must equal one of the prefixes or be a direct descendant of it. An empty list (or an absent field) denies **all** paths — even when `sftp_enabled: true`. This is intentional: requiring the operator to enumerate allowed paths prevents an accidentally broad grant.

```yaml
hosts:
  prod-web:
    sftp_enabled: true
    sftp_allowed_prefixes:
      - /srv/app        # LLM can read/write anything under /srv/app
      - /var/log/nginx  # and under /var/log/nginx
    # /etc/passwd, /root/.ssh/authorized_keys, etc. are rejected
```

The prefix check uses an exact separator boundary: prefix `/srv/app` matches `/srv/app/config.json` and `/srv/app` itself, but not `/srv/apple/secret`.

To grant access to the entire remote filesystem, set `sftp_allowed_prefixes: ["/"]`. This is a deliberate escape hatch that must be written explicitly.

Server-side controls (e.g. `ChrootDirectory` in `sshd_config`) remain valid defence-in-depth and are not replaced by the above — they are complementary.

### 9. Agent auth security

On Linux/macOS, rootcanal dials `$SSH_AUTH_SOCK`. On Windows, it dials the OpenSSH named pipe `\\.\pipe\openssh-ssh-agent`. The agent performs all cryptographic operations; rootcanal never has access to private key material.

PuTTY/Pageant use a different protocol and are not supported in v1.0.0. Support would require a separate implementation of the Pageant protocol.

## What rootcanal does not protect against

- A malicious config file: the operator has full control.
- An operator who writes keys with no passphrase to disk: that is an OS-level concern.
- Network-level attacks other than host-key mismatch (e.g. traffic monitoring): use the SSH transport's encryption, which is always on.
- Privilege escalation on the remote host: rootcanal authenticates as a specific user; what that user can do is a matter of remote host configuration.
- A compromised LLM with operator-granted unrestricted tool access: rootcanal is a tool boundary; auditing is the operator's responsibility.

## Reporting security issues

Please report vulnerabilities to the repository owner privately via GitLab's confidential issue feature, or by email. Do not open a public issue for security bugs.
