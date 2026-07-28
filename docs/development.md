# Development

## Prerequisites

- Go 1.26+ (see `go.mod` for the exact version)
- A working C compiler (for `creack/pty` — not needed on Linux with `golang.org/x/sys`)

## Quick start

```sh
# Build
go build ./cmd/relay-mcp

# Run interactively (no arguments, with a terminal on both streams)
./relay-mcp

# MCP stdio transport — the MCP client drives this noninteractive path
# and receives only the JSON-RPC stream on stdout.

# Test
go test ./...

# With race detector
go test -race -count=1 ./...
```

## Project layout

```
cmd/relay-mcp/     # Binary entry point
internal/
├── server/        # MCP protocol layer
└── session/       # Terminal session layer
```

Each package owns one responsibility. Namespace parents (e.g. `internal/session/`) contain only docs; sub-packages are the real code.

## Testing

- Unit tests live next to the code they test (`*_test.go` in the same package).
- Integration tests that spawn real PTY sessions are in `internal/session/session/`.
- E2E tests that exercise the full MCP server are in `internal/server/server/`.
- Run with `go test -race -shuffle=on -count=1 ./...` for CI-grade coverage.

## Cross-platform release contract

`cmd/relay-mcp` is the only build target and releases publish the one `relay` binary for Linux, macOS, and Windows on amd64 and arm64. GoReleaser names each archive `relay_<version>_<os>_<arch>`: Linux and macOS use `.tar.gz`; Windows uses `.zip`. Every release also publishes `checksums.txt`.

CI cross-compiles all six target pairs. Tagged releases run the native Windows installer harness, build a GoReleaser snapshot, and verify that the snapshot contains exactly those six archives and one checksum per archive before publishing.

For local package validation, run:

```sh
goreleaser release --snapshot --clean
RELAY_RELEASE_TEST_VERSION="$(git describe --tags --always)" bash scripts/release_test.sh
```

The interactive installer is selected only for a zero-argument launch with terminal stdin and stdout. Piped or MCP-managed launches retain the existing stdio protocol. The shell and PowerShell installers remain the documented manual fallback; Homebrew token remediation is out of scope.

## Before committing

```sh
go vet ./...
go test -race -shuffle=on -count=1 ./...
```

## Commit style

Conventional Commits, no scope parentheticals, no AI attribution:

```
feat: add run_command one-shot tool
fix: prevent race on session close
chore: bump creack/pty to v1.1.22
```

## Related

- [architecture.md](./architecture.md) — system architecture
