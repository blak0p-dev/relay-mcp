package server

import (
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/blak0p/relay-mcp/internal/server/description"
	"github.com/blak0p/relay-mcp/internal/server/handler"
	"github.com/blak0p/relay-mcp/internal/session/registry"
)

// NewServer builds a *mcpserver.MCPServer with every available terminal tool
// registered. reg must be non-nil; the same registry instance must be shared
// with every handler (single-session invariant).
//
// All tool metadata (name, summary, description) and server metadata (name,
// version, instructions) are sourced from the description package
// (REQ-008: single source of truth).
func NewServer(reg *registry.Registry) (*mcpserver.MCPServer, error) {
	if reg == nil {
		return nil, fmt.Errorf("server: NewServer requires a non-nil registry")
	}
	s := mcpserver.NewMCPServer(description.ServerName, description.ServerVersion,
		mcpserver.WithToolCapabilities(false),
		mcpserver.WithInstructions(description.ServerInstructions),
	)

	tool := mcp.NewTool(
		description.CreateTerminalName,
		mcp.WithDescription(description.CreateTerminalDescription),
	)
	s.AddTool(tool, handler.New(reg))

	// write_terminal: inject raw bytes into the active session's PTY. The
	// schema declares one required string parameter "data" (REQ-WT-008).
	// Two explicit AddTool calls — obvious, grep-friendly, zero abstraction
	// overhead for 2 tools (see design: "two explicit calls over a loop").
	writeTool := mcp.NewTool(
		description.WriteTerminalName,
		mcp.WithDescription(description.WriteTerminalDescription),
		mcp.WithString("data",
			mcp.Required(),
			mcp.Description("Bytes to inject into the terminal session (max 1 MiB)."),
		),
		mcp.WithBoolean("ensure_newline", mcp.Required(), mcp.Description("Append one trailing LF when data does not already end in LF.")),
	)
	s.AddTool(writeTool, handler.NewWriteTerminalHandler(reg))

	controlTool := mcp.NewTool(
		description.SendControlName,
		mcp.WithDescription(description.SendControlDescription),
		mcp.WithString("key",
			mcp.Required(),
			mcp.Description("Allowlisted terminal control key, such as ctrl+c, up, enter, or escape."),
		),
	)
	s.AddTool(controlTool, handler.NewSendControlHandler(reg))

	readTool := mcp.NewTool(
		description.ReadTerminalName,
		mcp.WithDescription(description.ReadTerminalDescription),
		mcp.WithString("mode",
			mcp.Enum("stream", "snapshot", "drain"),
			mcp.Description("Read mode. Defaults to stream; snapshot and drain return bounded polling results."),
		),
		mcp.WithInteger("cursor",
			mcp.Min(0),
			mcp.Description("Absolute output cursor. Omit to begin at the oldest retained byte."),
		),
		mcp.WithInteger("max_bytes",
			mcp.Min(1),
			mcp.Max(65536),
			mcp.Description("Maximum bytes returned by snapshot or drain (1 through 65536)."),
		),
		mcp.WithInteger("wait_ms",
			mcp.Min(0),
			mcp.Max(5000),
			mcp.Description("Maximum snapshot or drain wait in milliseconds (0 through 5000)."),
		),
	)
	s.AddTool(readTool, handler.NewReadTerminalHandler(reg))

	closeTool := mcp.NewTool(
		description.CloseTerminalName,
		mcp.WithDescription(description.CloseTerminalDescription),
		mcp.WithString("session_id",
			mcp.Required(),
			mcp.Description("Identifier returned by create_terminal for the session to close."),
		),
	)
	s.AddTool(closeTool, handler.NewCloseTerminalHandler(reg))

	return s, nil
}
