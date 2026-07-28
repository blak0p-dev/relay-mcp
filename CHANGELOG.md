# Changelog

## v0.1.2 - 2026-07-27

### Why

This release makes Relay available through Homebrew so users can install the
same release binary with the package manager they already use. It also tightens
the terminal write contract and stabilizes streamed terminal reads before
expanding distribution.

### Changed

- Added GoReleaser publishing for the `relay` formula in
  `blak0p/homebrew-tap`.
- Added Homebrew installation and MCP client setup documentation for Claude
  Code, Codex, OpenCode, and Pi.
- Made `ensure_newline` explicit for `write_terminal`, so callers choose
  whether input should receive a trailing newline instead of Relay silently
  pressing Enter.
- Stabilized the `read_terminal` streaming E2E flow so progress output is
  observed before the shell receives its exit input.

### Installation

```sh
brew install blak0p/tap/relay
```

Homebrew installs the binary but does not register it with an MCP client. See
the README for the client-specific setup commands.
