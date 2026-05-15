# rootcanal

**rootcanal** is an SSH MCP server written in Go. It lets an MCP client (Claude Desktop, the Claude CLI, or any MCP host) open persistent shell sessions and perform SFTP file operations on a pre-declared set of remote hosts.

```
Claude Desktop ──(stdio MCP)──▶ rootcanal ──(SSH)──▶ remote hosts
```

## Why rootcanal?

- **Pre-declared hosts only** — the LLM references hosts by name (e.g. `"prod-web"`), never by raw IP. It can only reach what you have explicitly listed in the config.
- **Persistent shell sessions** — `ssh_session_send` keeps the shell alive across calls, so the LLM can run `sudo`, interact with a REPL, or chain multi-step commands naturally.
- **Strict host-key verification** — `known_hosts`-based, no `InsecureIgnoreHostKey` anywhere, ever.
- **No plaintext secrets** — passwords and passphrases come from environment variables, never from the config file.

## Tools exposed

| Tool | Description |
|---|---|
| `ssh_session_open` | Open a persistent shell session; returns a `session_id` |
| `ssh_session_send` | Write to the shell's stdin, return stdout/stderr output |
| `ssh_session_close` | Close the session and release resources |
| `ssh_session_list` | List open sessions with timing metadata |
| `sftp_read` | Read a remote file (UTF-8 or base64 for binary) |
| `sftp_write` | Write a remote file (base64 accepted for binary) |
| `sftp_list` | List a remote directory |

## Installation

### Pre-built binaries

Download the latest release for your platform from the [Releases page](https://gitlab.com/zorak1103/rootcanal/-/releases).

```sh
# Linux / macOS — extract and install
tar -xzf rootcanal_v1.0.0_linux_amd64.tar.gz
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

Create a config file — `~/.config/rootcanal/config.yaml` on Linux/macOS, `%APPDATA%\rootcanal\config.yaml` on Windows — and declare your hosts.

```yaml
# ~/.config/rootcanal/config.yaml
hosts:
  prod-web:
    address: web1.example.com:22
    user: deploy
    known_hosts: ~/.ssh/known_hosts
    auth:
      type: key
      key_path: ~/.ssh/id_ed25519
      passphrase_env: ROOTCANAL_PROD_PASSPHRASE   # optional

  staging:
    address: staging.example.com:22
    user: ops
    known_hosts: ~/.ssh/known_hosts
    auth:
      type: agent   # uses SSH_AUTH_SOCK (Linux/macOS) or OpenSSH agent (Windows)

  legacy:
    address: 10.0.0.7:2222
    user: admin
    known_hosts: ~/.ssh/known_hosts
    auth:
      type: password
      password_env: ROOTCANAL_LEGACY_PASSWORD
```

Annotated example with all options: [`examples/rootcanal.example.yaml`](examples/rootcanal.example.yaml).

**Validate your config** without connecting to anything:

```sh
rootcanal -validate-config -config ~/.config/rootcanal/config.yaml
# → OK: 3 host(s) defined
```

**Test connectivity** to a single host:

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

**Quick smoke test** — ask Claude:

> *"Use rootcanal to open a session on `prod-web` and run `uname -a`."*

Claude will call `ssh_session_open`, then `ssh_session_send("uname -a\n")`, then `ssh_session_close`.

## SSH agent on Windows

rootcanal connects to the **OpenSSH for Windows** agent via its named pipe. Enable it once:

```powershell
# Run as Administrator
Set-Service -Name ssh-agent -StartupType Automatic
Start-Service ssh-agent

# Add your key
ssh-add $env:USERPROFILE\.ssh\id_ed25519
```

PuTTY/Pageant are not supported in v1.0.0.

## Global limits (optional)

```yaml
limits:
  max_sessions_total:    32      # hard cap across all hosts
  max_sessions_per_host:  4      # also limits concurrent SFTP ops
  default_idle_timeout:  15m     # GC closes sessions unused this long
  max_session_age:        4h     # GC closes sessions older than this
  output_buffer_bytes:   1048576 # 1 MiB ring buffer per session
  sftp_max_read_bytes:   5242880 # 5 MiB per sftp_read call
  sftp_max_write_bytes: 26214400 # 25 MiB per sftp_write call
```

## Known limitations

- **Output framing is heuristic.** `ssh_session_send` returns output received within a timeout after a 50 ms quiescence gap. It may split output across two calls for long-running commands; the LLM handles this gracefully by calling `send` with empty input to poll for more.
- **No `ssh_exec` (single-shot exec).** Use `ssh_session_open` + `ssh_session_send` + `ssh_session_close` instead. This is intentional: the persistent session model handles `sudo` prompts, REPLs, and multi-step commands naturally.
- **No port forwarding** in v1.0.0.
- **PuTTY/Pageant not supported** on Windows — use OpenSSH for Windows agent.

## sudo and privilege escalation

rootcanal supports `sudo` on remote hosts through its PTY-based persistent sessions. The LLM sends `sudo <command>` via `ssh_session_send`, receives the password prompt in the output, and can respond with the password in a follow-up call.

> **⚠️ Security warning:** Never pass a `sudo` password to the LLM as prompt context or conversation input. The password would travel to the LLM provider's infrastructure in plaintext, could appear in conversation logs or model responses, and is outside your control once sent.

**Recommended approach: `NOPASSWD` for specific commands only**

Configure sudoers to grant the SSH user passwordless access to exactly the commands that are needed — and nothing more:

```
# /etc/sudoers.d/rootcanal  (always edit with visudo -f)
deploy ALL=(ALL) NOPASSWD: /usr/bin/systemctl restart myapp, /usr/bin/apt-get update
```

Do **not** use `NOPASSWD: ALL`. Restrict to the minimum set of commands the LLM actually needs. This is consistent with rootcanal's overall security model: boundaries are enforced by the operator at the system level, not by the LLM.

If a password prompt appears and no password is provided, the session blocks until `default_send_timeout_ms` elapses and returns the prompt text — the LLM can detect this and surface it to the user.

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

## Security

See [docs/security.md](docs/security.md) for the full threat model and security boundary documentation.

## License

GPL v3 — see [LICENSE](LICENSE).
