# rootcanal

An SSH MCP server in Go. Lets an MCP client (e.g. Claude Desktop) drive SSH operations against a pre-declared set of remote hosts.

> **Status:** Under active development — see milestones in the project plan.

## Features (v1.0.0)

- Persistent shell sessions (`ssh_session_open` / `ssh_session_send` / `ssh_session_close` / `ssh_session_list`)
- SFTP file operations (`sftp_read` / `sftp_write` / `sftp_list`)
- Pre-declared host allowlist — the LLM never reaches arbitrary IPs
- Auth: public key, ssh-agent (cross-platform), password (env var)
- Strict host-key verification via `known_hosts`
- MCP stdio transport (Claude Desktop compatible)

## Installation

```sh
go install gitlab.com/zorak1103/rootcanal/cmd/rootcanal@latest
```

Or build from source:

```sh
git clone https://gitlab.com/zorak1103/rootcanal.git
cd rootcanal
task build
```

## Configuration

See [`examples/rootcanal.example.yaml`](examples/rootcanal.example.yaml) for an annotated config showing all auth types.

## Claude Desktop integration

Add to `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "rootcanal": {
      "command": "/path/to/rootcanal",
      "args": ["-config", "/path/to/rootcanal.yaml"]
    }
  }
}
```

## Development

Requires [Task](https://taskfile.dev) (`go install github.com/go-task/task/v3/cmd/task@latest`).

```sh
task build    # build binary
task test     # run tests with race detector
task cover    # run tests + enforce ≥85% coverage
task lint     # go vet + staticcheck
task run      # run locally (pass args after --)
```

## License

MIT — see [LICENSE](LICENSE).
