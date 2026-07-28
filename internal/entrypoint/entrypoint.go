// Package entrypoint decides whether Relay starts its interactive experience or
// its MCP stdio server.
package entrypoint

// Mode identifies the selected startup path.
type Mode uint8

const (
	// MCP preserves Relay's protocol-only stdio behavior.
	MCP Mode = iota
	// Interactive starts the human-facing maintenance experience.
	Interactive
)

// Decide selects Interactive only when no CLI arguments were supplied and both
// standard streams are terminals. Arguments are inspected only for presence;
// they are never parsed or executed here.
func Decide(args []string, stdinTTY, stdoutTTY bool) Mode {
	if len(args) == 0 && stdinTTY && stdoutTTY {
		return Interactive
	}
	return MCP
}
