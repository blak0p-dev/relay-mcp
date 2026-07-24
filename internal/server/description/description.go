package description

// ServerName is the MCP server implementation name advertised in the
// initialize response. It identifies the product to MCP clients.
const ServerName = "relay"

// ServerVersion is the advertised MCP server version. Release builds override
// this variable with the linker while source builds retain the development
// version.
var ServerVersion = "dev"

// ServerInstructions is the global server description sent to the client in the
// initialize response. It explains what Relay is and when to use it.
const ServerInstructions = "Relay does not replace your shell — it gives you what your shell alone cannot: interactive, persistent terminal sessions. Install packages and answer prompts without workarounds. SSH into servers and keep the session alive between turns. Run long-lived processes. You have eyes on a real PTY — use standard tools when they suffice, and reach for Relay when you need interactivity."

// create_terminal constants are the single source of truth for the tool's
// client-facing metadata.
const (
	// CreateTerminalName is the MCP tool name clients invoke.
	CreateTerminalName = "create_terminal"

	// CreateTerminalSummary is the one-line summary shown in tool listings.
	CreateTerminalSummary = "Spawn a persistent interactive bash session in a PTY."

	// CreateTerminalDescription is the full description sent in the tool
	// manifest. It tells the client what the tool does and how it behaves.
	CreateTerminalDescription = "Spawns a real bash process inside a PTY and returns a session handle. The session persists until close_terminal is called. If a session is already active, this call returns an error."
)

// write_terminal constants. These are the single source of truth for the
// tool name, summary, and full description registered with the MCP server
// (REQ-WT-007). The description states the two non-obvious contracts: the
// 1 MiB cap and the raw-byte (no auto-Enter) rule.
const (
	// WriteTerminalName is the MCP tool name clients invoke.
	WriteTerminalName = "write_terminal"

	// WriteTerminalSummary is the one-line summary shown in tool listings.
	WriteTerminalSummary = "Inject raw bytes into a running terminal session."

	// WriteTerminalDescription is the full description sent in the tool
	// manifest. It tells the client what the tool does and how it behaves.
	WriteTerminalDescription = "Writes raw bytes to the PTY of an active terminal session. The data is injected verbatim — no auto-Enter, no transformation. Maximum 1 MiB per call. Returns bytes_written and the current session state. Partial writes are not retried; the agent must resend the remainder."
)

// send_control constants are the single source of truth for the finite control
// key tool's client-facing metadata.
const (
	// SendControlName is the MCP tool name clients invoke.
	SendControlName = "send_control"

	// SendControlSummary is the one-line summary shown in tool listings.
	SendControlSummary = "Send an allowlisted control key to the active terminal session."

	// SendControlDescription describes the finite key allowlist and its active
	// session-only delivery contract.
	SendControlDescription = "Sends one allowlisted terminal control key to the active session. Keys are normalized for case and surrounding whitespace; arbitrary bytes, key combinations, and session targeting are not accepted. Returns the canonical key and bytes_sent. Partial writes fail and are not retried."
)

// read_terminal constants are the single source of truth for the tool's
// client-facing metadata. The description states the default streaming
// contract and the explicit polling alternatives.
const (
	// ReadTerminalName is the MCP tool name clients invoke.
	ReadTerminalName = "read_terminal"

	// ReadTerminalSummary is the one-line summary shown in tool listings.
	ReadTerminalSummary = "Read retained output from the active terminal session."

	// ReadTerminalDescription is the full description sent in the tool
	// manifest. It tells clients how to select streaming or bounded reads.
	ReadTerminalDescription = "Streams terminal output through MCP progress notifications by default. Stream mode requires _meta.progressToken; use snapshot or drain for bounded polling reads. Cursor values are absolute byte positions in the retained 1 MiB output tail."
)

// close_terminal constants are the single source of truth for the tool's
// client-facing metadata.
const (
	// CloseTerminalName is the MCP tool name clients invoke.
	CloseTerminalName = "close_terminal"

	// CloseTerminalSummary is the one-line summary shown in tool listings.
	CloseTerminalSummary = "Close a persistent terminal session by its session id."

	// CloseTerminalDescription explains that matching closes release the sole
	// session slot and that repeated or mismatched calls are harmless no-ops.
	CloseTerminalDescription = "Stops and releases the terminal session identified by session_id. Returns its final status and exit code when closed. Unknown, mismatched, or already-released session ids return closed:false."
)
