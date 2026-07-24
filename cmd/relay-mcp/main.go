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

	"github.com/blak0p/relay-mcp/internal/server/server"
	"github.com/blak0p/relay-mcp/internal/session/registry"
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

// run is the testable body of main: it wires the registry, the MCP server,
// the stdio transport, and the signal handler. Splitting it out of main lets
// future tests drive the wiring without exec'ing the binary.
func run() error {
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
