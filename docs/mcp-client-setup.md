# Connecting rootcanal to MCP clients

## Claude Desktop

### Linux / macOS

Edit `~/.config/claude/claude_desktop_config.json`:

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

### Windows

Edit `%APPDATA%\Claude\claude_desktop_config.json`:

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

Restart Claude Desktop after saving. The rootcanal tools appear in the tool picker.

## Claude CLI (MCP mode)

```sh
claude mcp add rootcanal -- rootcanal -config ~/.config/rootcanal/config.yaml
```

## Claude Code skill

If you use Claude Code as your MCP client, a companion skill is bundled with rootcanal under
`.claude/skills/rootcanal-ssh/`. It teaches Claude the full rootcanal tool API, output-framing
quirks, SFTP access control, and error-handling patterns so you don't have to explain them
each session.

Install the skill once into your user-level Claude Code config:

```sh
# Linux / macOS
cp -r .claude/skills/rootcanal-ssh ~/.claude/skills/

# Windows (PowerShell)
Copy-Item -Recurse .claude\skills\rootcanal-ssh $env:USERPROFILE\.claude\skills\
```

Or, if you want the skill available only in a specific project that also uses rootcanal, copy
it into that project's `.claude/skills/` directory instead.

Reload Claude Code after copying. Claude picks up the skill automatically for any session
where rootcanal is configured as an MCP server.

## Verifying the connection

Ask the LLM:

> *"List your available tools and tell me which ones are from rootcanal."*

A working rootcanal installation will show `ssh_session_open`, `ssh_session_send`, `ssh_session_close`, `ssh_session_list`, `sftp_read`, `sftp_write`, `sftp_list`, `ssh_run_once`, `ssh_list_hosts`, and `ssh_host_capabilities`.

## Logs

rootcanal logs at `INFO` level to the MCP client once the session is established. In Claude Desktop, logs appear in the developer console (`Cmd/Ctrl+Shift+J` → Console tab). Look for messages with the logger name `rootcanal`.

Before the session handshake completes, startup logs go to stderr and are visible in the Claude Desktop log file or terminal if you launched rootcanal manually.
