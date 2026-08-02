# Changelog

## v0.2.1 - 2026-08-02

### Why

This patch release improves the reliability and clarity of Relay's terminal
lifecycle and polling behavior. It focuses on edge cases that affect clients
using bounded terminal reads, intentional session shutdown, and raw PTY input.

The release also documents an important boundary of interactive terminal
control: Relay delivers raw input to the foreground application, but it cannot
inspect or rewrite that application's pending readline state.

### Changed

- Added regression coverage for `read_terminal` drain mode when requests use
  raw JSON arguments, protecting the MCP-facing polling path from regressions.
- Increased the maximum `read_terminal` snapshot wait from one second to five
  seconds, allowing slower commands and environments more time to produce
  output before a bounded read returns.
- Published the updated read-terminal behavior in the MCP tool schema.
- Changed intentional terminal shutdown to report `status: "exited"` and
  `exit_code: 0`, instead of presenting a clean close as an error.
- Added regression coverage for intentional close behavior and forced shutdown
  classification.
- Stabilized close-terminal end-to-end assertions against nondeterministic
  process timing.
- Documented that terminal input is raw PTY input owned by the foreground
  application, including the Tab/Escape limitation and cautious Ctrl+C recovery
  guidance for Bash.

### Compatibility

Existing MCP integrations remain unchanged. Clients using snapshot or drain
mode can now wait up to five seconds for bounded output. Terminal input should
be treated as application-owned raw PTY input.

### Installation

Download the archive for the target operating system and architecture from the
GitHub release, or install through the Homebrew tap after the formula is
published.

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
