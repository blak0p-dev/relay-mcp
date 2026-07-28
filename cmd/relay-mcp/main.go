// Command relay-mcp is the Model Context Protocol server entry point.
//
// It builds the MCP server with the create_terminal tool registered and
// serves it over the stdio transport. SIGINT and SIGTERM trigger a graceful
// shutdown: the context is cancelled and ServeStdio returns once the
// in-flight request (if any) completes.
//
// Run with no arguments; the MCP client drives everything over stdin/stdout:
//
//	relay-mcp < requests.jsonl
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/blak0p/relay-mcp/internal/entrypoint"
	"github.com/blak0p/relay-mcp/internal/server/server"
	"github.com/blak0p/relay-mcp/internal/session/registry"
	"github.com/blak0p/relay-mcp/internal/tui"
)

func main() {
	if err := run(); err != nil {
		writeError(os.Stderr, err)
		os.Exit(1)
	}
}

func writeError(w io.Writer, err error) {
	fmt.Fprintf(w, "relay: %v\n", err)
}

// run selects the interactive path only for a zero-argument dual-TTY launch.
// All other invocations retain the existing MCP stdio runner.
func run() error {
	return runWithTTY(os.Args[1:], isTerminal(os.Stdin), isTerminal(os.Stdout), runInteractive, runMCP)
}

func runWithTTY(args []string, stdinTTY, stdoutTTY bool, interactive, mcp func() error) error {
	if entrypoint.Decide(args, stdinTTY, stdoutTTY) == entrypoint.Interactive {
		return interactive()
	}
	return mcp()
}

func isTerminal(file *os.File) bool {
	if file == nil {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func runInteractive() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}
	return tui.Run(home)
}

// runMCP is the unchanged MCP stdio runner.
func runMCP() error {
	reg := registry.NewRegistry()
	s, err := server.NewServer(reg)
	if err != nil {
		return fmt.Errorf("build server: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := mcpserver.ServeStdio(s); err != nil {
		// If shutdown was triggered by a signal, treat it as a clean exit
		// rather than a failure.
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("serve stdio: %w", err)
	}
	return nil
}
