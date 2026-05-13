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

Restart Claude Desktop after saving. The seven rootcanal tools appear in the tool picker.

## Claude CLI (MCP mode)

```sh
claude mcp add rootcanal -- rootcanal -config ~/.config/rootcanal/config.yaml
```

## Verifying the connection

Ask the LLM:

> *"List your available tools and tell me which ones are from rootcanal."*

A working rootcanal installation will show `ssh_session_open`, `ssh_session_send`, `ssh_session_close`, `ssh_session_list`, `sftp_read`, `sftp_write`, and `sftp_list`.

## Logs

rootcanal logs at `INFO` level to the MCP client once the session is established. In Claude Desktop, logs appear in the developer console (`Cmd/Ctrl+Shift+J` → Console tab). Look for messages with the logger name `rootcanal`.

Before the session handshake completes, startup logs go to stderr and are visible in the Claude Desktop log file or terminal if you launched rootcanal manually.
