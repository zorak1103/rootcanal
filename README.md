# rootcanal

[![Pipeline](https://gitlab.com/zorak1103/rootcanal/badges/main/pipeline.svg)](https://gitlab.com/zorak1103/rootcanal/-/pipelines)
[![Coverage](https://gitlab.com/zorak1103/rootcanal/badges/main/coverage.svg)](https://gitlab.com/zorak1103/rootcanal/-/graphs/main/charts)
[![Release](https://img.shields.io/gitlab/v/release/zorak1103%2Frootcanal)](https://gitlab.com/zorak1103/rootcanal/-/releases)
[![License](https://img.shields.io/gitlab/license/zorak1103%2Frootcanal)](https://gitlab.com/zorak1103/rootcanal/-/blob/main/LICENSE)
[![Go Report Card](https://goreportcard.com/badge/gitlab.com/zorak1103/rootcanal)](https://goreportcard.com/report/gitlab.com/zorak1103/rootcanal)
[![Go Version](https://img.shields.io/badge/go-1.26-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![Renovate](https://gitlab.com/zorak1103/rootcanal/badges/main/pipeline.svg?job=renovate)](https://gitlab.com/zorak1103/rootcanal/-/pipeline_schedules)

**rootcanal** is an SSH MCP server written in Go. It lets an MCP client (Claude Desktop, the Claude CLI, or any MCP host) open persistent shell sessions and perform SFTP file operations on a pre-declared set of remote hosts.

```
MCP client ──(stdio MCP)──▶ rootcanal ──(SSH sessions)──▶ remote hosts
                                       └──(SFTP)──────────▶ remote hosts
```

## Why rootcanal?

- Pre-declared hosts only: the LLM references hosts by name (e.g. `"prod-web"`). It can only reach what you have explicitly listed in the config.
- Persistent shell sessions: `ssh_session_send` keeps the shell alive across calls, so the LLM can run `sudo` or interact with a REPL across multiple commands.
- Strict host-key verification: `known_hosts`-based; `InsecureIgnoreHostKey` is not exposed.
- No plaintext secrets: passwords and passphrases come from environment variables.

## Tools exposed

| Tool | Description |
|---|---|
| `ssh_session_open` | Open a persistent shell session; returns a `session_id` |
| `ssh_session_send` | Write to the shell's stdin, return stdout/stderr output |
| `ssh_session_close` | Close the session and release resources |
| `ssh_session_list` | List open sessions with timing metadata |
| `sftp_read` | Read a remote file (UTF-8 or base64 for binary) — requires `sftp_enabled: true` on the host |
| `sftp_write` | Write a remote file (base64 accepted for binary) — requires `sftp_enabled: true` on the host |
| `sftp_list` | List a remote directory — requires `sftp_enabled: true` on the host |
| `ssh_run_once` | Execute a single command via SSH exec channel (no PTY); returns stdout, stderr, exit_code |
| `ssh_list_hosts` | List all pre-declared hosts with non-sensitive metadata (no credentials) |
| `ssh_host_capabilities` | Return SSH/SFTP caps, session limits, and terminal settings for a host |

## Installation

### Pre-built binaries

Download the latest release for your platform from the [Releases page](https://gitlab.com/zorak1103/rootcanal/-/releases).

```sh
# Linux / macOS — extract and install
tar -xzf rootcanal_v2.0.0_linux_amd64.tar.gz
sudo mv rootcanal /usr/local/bin/

# Windows — extract rootcanal.exe from the zip and add to PATH
```

### Build from source

Requires **Go 1.26+**.

```sh
git clone https://gitlab.com/zorak1103/rootcanal.git
cd rootcanal
go install github.com/go-task/task/v3/cmd/task@latest   # build tool
task build                                               # → ./rootcanal (or rootcanal.exe)
```

Or with plain `go`:

```sh
go build -o rootcanal ./cmd/rootcanal
```

## Configuration

Create a config file (`~/.config/rootcanal/config.yaml` on Linux/macOS, `%APPDATA%\rootcanal\config.yaml` on Windows) and declare your hosts.

```yaml
# ~/.config/rootcanal/config.yaml
hosts:
  prod-web:
    address: web1.example.com:22
    user: deploy
    known_hosts: ~/.ssh/known_hosts
    description: "Production web server"          # optional label for ssh_list_hosts
    idle_timeout: 10m                              # override global default_idle_timeout
    term: dumb                                     # $TERM for this host (default: dumb)
    clean_output: true                             # strip ANSI/echo (default: true)
    auth:
      type: key
      key_path: ~/.ssh/id_ed25519
      passphrase_env: ROOTCANAL_PROD_PASSPHRASE   # optional
    # SFTP is disabled by default. Enable it and restrict paths explicitly.
    sftp_enabled: true
    sftp_allowed_prefixes:
      - /srv/app
      - /var/log/nginx

  staging:
    address: staging.example.com:22
    user: ops
    known_hosts: ~/.ssh/known_hosts
    auth:
      type: agent   # uses SSH_AUTH_SOCK (Linux/macOS) or OpenSSH agent (Windows)
    # No sftp_enabled → all sftp_* tool calls on this host are rejected.

  legacy:
    address: 10.0.0.7:2222
    user: admin
    known_hosts: ~/.ssh/known_hosts
    auth:
      type: password
      password_env: ROOTCANAL_LEGACY_PASSWORD
```

Annotated example with all options: [`examples/rootcanal.example.yaml`](examples/rootcanal.example.yaml).

Validate your config without connecting to anything:

```sh
rootcanal -validate-config -config ~/.config/rootcanal/config.yaml
# → OK: 3 host(s) defined
```

Test connectivity to a single host:

```sh
rootcanal -probe prod-web -config ~/.config/rootcanal/config.yaml
# → OK: connected to web1.example.com:22 as deploy
```

## Claude Desktop integration

Add rootcanal to your `claude_desktop_config.json`:

**Linux / macOS** (`~/.config/claude/claude_desktop_config.json`):

```json
{
  "mcpServers": {
    "rootcanal": {
      "command": "/usr/local/bin/rootcanal",
      "args": ["-config", "/home/you/.config/rootcanal/config.yaml"]
    }
  }
}
```

**Windows** (`%APPDATA%\Claude\claude_desktop_config.json`):

```json
{
  "mcpServers": {
    "rootcanal": {
      "command": "C:\\tools\\rootcanal.exe",
      "args": ["-config", "C:\\Users\\you\\AppData\\Roaming\\rootcanal\\config.yaml"]
    }
  }
}
```

Restart Claude Desktop after saving. The rootcanal tools appear in the tool list.

For a smoke test, ask Claude:

> *"Use rootcanal to open a session on `prod-web` and run `uname -a`."*

Claude will call `ssh_session_open`, then `ssh_session_send("uname -a\n")`, then `ssh_session_close`.

For Claude Code users, a companion skill is bundled under `.claude/skills/rootcanal-ssh/`.
Install it once to give Claude deep knowledge of all rootcanal tools, error messages, and SFTP
workflows without repeating context each session. See [docs/mcp-client-setup.md](docs/mcp-client-setup.md)
for installation instructions.

## SSH agent on Windows

rootcanal connects to the **OpenSSH for Windows** agent via its named pipe. Enable it once:

```powershell
# Run as Administrator
Set-Service -Name ssh-agent -StartupType Automatic
Start-Service ssh-agent

# Add your key
ssh-add $env:USERPROFILE\.ssh\id_ed25519
```

PuTTY/Pageant are not supported; use the OpenSSH for Windows agent instead.

## Global limits (optional)

```yaml
limits:
  max_sessions_total:       32       # hard cap across all hosts
  max_sessions_per_host:     4       # also limits concurrent SFTP ops
  default_idle_timeout:     15m      # GC closes sessions unused this long
  max_session_age:           4h      # GC closes sessions older than this
  output_buffer_bytes:    1048576    # 1 MiB ring buffer per session
  dial_timeout:             10s      # SSH TCP connect timeout
  default_send_timeout_ms:  2000     # ssh_session_send default timeout
  max_send_timeout_ms:     30000     # ssh_session_send hard cap
  sftp_max_read_bytes:    2097152    # 2 MiB per sftp_read call (default; raise or lower per your needs)
  sftp_max_write_bytes:  26214400    # 25 MiB per sftp_write call
  # v2.0 additions
  default_term:          dumb        # $TERM advertised to remote shell
  default_clean_output:  true        # strip ANSI/escape codes by default
  run_once_max_bytes:    1048576     # 1 MiB cap per stream for ssh_run_once
  run_once_max_timeout_ms: 60000     # ssh_run_once hard timeout cap
  max_run_once_concurrent:  16       # concurrent ssh_run_once calls
```

## SFTP access control

SFTP access is **disabled by default** and controlled by two per-host config fields:

| Field | Type | Default | Meaning |
|---|---|---|---|
| `sftp_enabled` | bool | `false` | Must be `true` for any SFTP tool call to succeed on this host |
| `sftp_allowed_prefixes` | list of strings | `[]` | Absolute Unix paths the LLM may access. Empty list denies all paths. |

Three validation layers are applied to every `sftp_read`, `sftp_write`, and `sftp_list` call:

1. **Host opt-in** — the host must have `sftp_enabled: true`, otherwise the call is rejected immediately.
2. **Path normalisation** — `path.Clean` is applied and the result must be an absolute Unix path (starts with `/`). Traversal sequences such as `../` are rejected after cleaning.
3. **Allowlist check** — the cleaned path must equal one of the configured prefixes or be a descendant of it. A prefix of `/srv/app` matches `/srv/app/config.json` but not `/srv/apple/secret`.

**Explicit "allow all" escape hatch:** set `sftp_allowed_prefixes: ["/"]` to permit any absolute path — this must be written deliberately; it does not happen by default.

Hosts without `sftp_enabled: true` have all `sftp_*` calls rejected, even if SFTP credentials would otherwise permit access.

## Known limitations

- **Output framing uses sentinel markers.** `ssh_session_send` injects a `RC_EXIT_<nonce>_<code>` marker after each command and waits for it to appear in the output. For raw mode (`raw: true`) or REPL/TUI use, use `wait_idle_ms` instead; output is then returned after that many milliseconds of silence.
- **Long-running commands:** If `timeout_ms` elapses before the marker arrives, `still_running: true` is returned. Send empty input to keep waiting.
- **`ssh_run_once` vs persistent sessions:** Use `ssh_run_once` for one-shot reads (`df`, `cat`, `docker inspect`). Use `ssh_session_open` + `ssh_session_send` for interactive work, `sudo`, or REPLs that require PTY.
- No port forwarding.
- PuTTY/Pageant not supported on Windows: use OpenSSH for Windows agent.

## sudo and privilege escalation

rootcanal supports `sudo` on remote hosts through its PTY-based persistent sessions. The LLM sends `sudo <command>` via `ssh_session_send`, receives the password prompt in the output, and can respond with the password in a follow-up call.

> Security warning: never pass a `sudo` password to the LLM as prompt context or conversation input. The password would travel to the LLM provider's infrastructure in plaintext and may appear in conversation logs.

### Recommended: `NOPASSWD` for specific commands only

Configure sudoers to grant the SSH user passwordless access to exactly the commands that are needed:

```
# /etc/sudoers.d/rootcanal  (always edit with visudo -f)
deploy ALL=(ALL) NOPASSWD: /usr/bin/systemctl restart myapp, /usr/bin/apt-get update
```

Do **not** use `NOPASSWD: ALL`. Restrict to the minimum set of commands the LLM actually needs.

If a password prompt appears and no password is provided, the session blocks until `default_send_timeout_ms` elapses and returns the prompt text. The LLM can detect this and surface it to the user.

## Development

```sh
task build    # compile binary
task test     # run all tests
task cover    # enforce ≥85% coverage
task lint     # go vet + staticcheck
task run      # run locally (pass args after --)
```

Race detector (requires CGO):

```sh
CGO_ENABLED=1 go test ./... -race
```

### End-to-end tests

The `e2e/` directory contains end-to-end tests that run the real `rootcanal` binary against an openssh-server Docker container. They are excluded from the CI pipeline (via the `//go:build e2e` tag) and are intended for local use only.

**Requirements:** Docker (Desktop or Engine) must be running.

```sh
task e2e   # build binary, start container, run ~40 tests, teardown
```

The tests exercise the full stack: real SSH PTY sessions, SFTP file operations, auth strategies (key, passphrase, password), host-key strict pinning, session/SFTP limits, MCP logging, and graceful shutdown. Because the container is ephemeral and easily restored, tests may modify files inside it freely.

## Security

See [docs/security.md](docs/security.md) for the full threat model and security boundary documentation.

## License

GPL v3 — see [LICENSE](LICENSE).
