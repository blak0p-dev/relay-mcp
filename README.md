# relay-mcp

[![Go](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go)](https://go.dev)
[![CI](https://github.com/blak0p/relay-mcp/actions/workflows/ci.yml/badge.svg)](https://github.com/blak0p/relay-mcp/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

**Interactive terminal MCP server for AI agents.**

Relay lets AI agents spawn real PTY sessions — bash, python, lazygit, whatever — and interact with them through MCP tools. The agent delegates commands, you execute them in a real terminal, and the agent reads the output. Like a relay.

## Tools

| Tool | What it does |
|---|---|
| `create_terminal` | Spawn a new PTY session (bash by default) |
| `write_terminal` | Write input with required `ensure_newline`; true appends one LF, false preserves bytes |
| `read_terminal` | Read output incrementally (stream, snapshot, or drain) |
| `send_control` | Send control sequences: Ctrl+C, arrows, Tab, etc. |
| `close_terminal` | Kill the session and free its resources |

5 tools, well made. Covers 90% of real agent-terminal interaction.

## Quick start

1. Install `relay` with the command for your platform.
2. Open a terminal and run `relay`.
3. In the full-screen installer, select **Configure clients** and confirm the preselected clients.

That is the normal setup path. You do not need to manually add Relay to Claude Code, Codex, OpenCode, or Pi when the interactive installer detects and configures them.

## Install a release

Install a verified, no-sudo release for your platform. The installers support Linux and macOS on amd64 or arm64, and Windows on amd64 or arm64. They place `relay` in `$GOBIN` when set, otherwise in `$HOME/go/bin` (`$HOME\go\bin` on Windows).

### Linux and macOS

```sh
curl --fail --show-error --location --proto '=https' \
  https://raw.githubusercontent.com/blak0p/relay-mcp/main/scripts/install.sh | bash
```

### Windows

In PowerShell:

```powershell
Invoke-WebRequest https://raw.githubusercontent.com/blak0p/relay-mcp/main/scripts/install.ps1 -OutFile .\install-relay.ps1
.\install-relay.ps1
Remove-Item .\install-relay.ps1
```

For a specific release, run `.\install-relay.ps1 -Version v0.2.0` instead. Both installers download the matching archive and `checksums.txt`, verify SHA-256 before replacement, and keep an existing binary unchanged if verification fails. They never invoke `sudo`.

### PATH

The installer reports when its destination is not on `PATH`; add that destination and open a new shell before running `relay`. It does not edit shell profiles or system PATH settings automatically.

## Interactive installer

Run the installed `relay` binary with no arguments from a terminal to open the interactive installer. It provides a full-screen terminal UI for updating Relay and configuring supported clients without adding a second executable.

Choose an action with the arrow keys or `j`/`k`, then press Enter. In the client screen, use Space to toggle selections. Relay detects Claude Code, Codex, OpenCode, and Pi from their local configuration files and preselects the clients it finds:

| Client | Detection path |
|---|---|
| Claude Code | `~/.claude.json` |
| Codex | `~/.codex/config.toml` |
| OpenCode | `~/.config/opencode/opencode.json` |
| Pi | `~/.pi/agent/settings.json` |

Interactive mode starts only when both stdin and stdout are terminals. Every other invocation keeps the MCP stdio transport unchanged, so an MCP client can continue to launch `relay` over stdin/stdout without TUI output or extra flags.

### Manual fallback

The release scripts above install the binary. If you have a terminal, run `relay` afterwards and let the interactive installer configure your detected clients. Use the client-specific commands in [Client setup and remediation](#client-setup-and-remediation) only when a terminal is unavailable or a client is not detected.

## Install with Homebrew

The release workflow publishes a formula to the `blak0p/homebrew-tap` tap:

```sh
brew install blak0p/tap/relay
```

This installs the `relay` binary. Then run `relay` in a terminal to configure detected MCP clients through the interactive installer. If that is not available, register the installed binary manually, for example:

```sh
claude mcp add --scope user relay -- "$(brew --prefix)/bin/relay"
```

The formula is updated automatically when a version tag is released.

After installation, Homebrew displays a reminder to register `relay` with your MCP client. The formula intentionally does not modify client configuration automatically. Homebrew token remediation is explicitly out of scope for this change.

## Client setup and remediation

The interactive installer detects available clients, preselects those with an existing local configuration file, and configures one global/user registration named `relay`. Re-running it replaces that registration rather than adding duplicates. Missing clients are left unselected; their exact command is printed for manual setup.

| Client | Automatic setup | Manual remediation |
|---|---|---|
| Claude Code | User scope | `claude mcp add --scope user relay -- /absolute/path/to/relay` |
| Codex | User configuration | `codex mcp add relay -- /absolute/path/to/relay` |
| OpenCode | User configuration | `opencode mcp add relay -- /absolute/path/to/relay` |
| Pi | Updates `~/.pi/agent/settings.json` | `pi mcp add relay -- /absolute/path/to/relay` |

For Pi, the installer updates only `mcpServers.relay` in `~/.pi/agent/settings.json`; existing Pi settings are preserved. If you already manage that file, keep its other entries and add or update the Relay command to the installed binary. Restart or reload a client after changing its MCP configuration.

With Homebrew, add this entry to the existing `mcpServers` object in `~/.pi/agent/settings.json`:

```sh
  brew --prefix
```

```json
{
  "mcpServers": {
    "relay": {
      "command": "relay"
    }
  }
}
```

The installer records `relay`, so ensure the Homebrew bin directory is on `PATH`. Preserve any other entries already in `mcpServers`.

## Build from source

```sh
git clone https://github.com/blak0p/relay-mcp.git
cd relay-mcp
go build -o relay ./cmd/relay-mcp
./relay
```

The repository, Go module/import path, and `cmd/relay-mcp` source directory intentionally retain their existing names. Release binaries and MCP initialization identify the server as `relay`.

## Roll back or migrate

To remove a release, delete the installed `relay` binary and unregister it from any client you configured:

```sh
claude mcp remove --scope user relay
codex mcp remove relay
opencode mcp remove relay
```

On Windows, remove `$env:GOBIN\relay.exe` when `GOBIN` is set, otherwise remove `$HOME\go\bin\relay.exe`. For Pi, remove only `mcpServers.relay` from `~/.pi/agent/settings.json`; do not delete unrelated settings. Reinstall a known-good version with the versioned installer command above if you need to roll back to a prior release.

## Architecture

```
AI agent ──► relay-mcp ──► creack/pty ──► process (bash / python / ...)
                 │
                 ├── create_terminal     spawn PTY
                 ├── write_terminal      send input
                 ├── read_terminal       read output
                 ├── send_control        Ctrl+C, arrows, Tab
                 └── close_terminal      kill session
```

Each session runs in an isolated process group. Close kills the whole process tree. Output is buffered in a ring buffer — no data loss on slow readers.

See [docs/architecture.md](docs/architecture.md) for the full picture.

## Configuration

None. relay-mcp is a stdio MCP server with zero configuration. Point your MCP client at the binary and it works.

## Documentation

- [docs/architecture.md](docs/architecture.md) — system architecture and design decisions
- [docs/development.md](docs/development.md) — building, testing, contributing

## License

MIT. See [LICENSE](LICENSE).
