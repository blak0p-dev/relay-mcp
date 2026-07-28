package entrypoint

import "testing"

func TestDecide(t *testing.T) {
	for _, tt := range []struct {
		name                string
		args                []string
		stdinTTY, stdoutTTY bool
		want                Mode
	}{
		{"zero arguments with dual TTY selects interactive mode", nil, true, true, Interactive},
		{"piped stdin remains MCP", nil, false, true, MCP},
		{"piped stdout remains MCP", nil, true, false, MCP},
		{"flag invocation remains MCP", []string{"--version"}, true, true, MCP},
		{"metacharacter argument remains data and selects MCP", []string{"$(touch pwned); relay"}, true, true, MCP},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := Decide(tt.args, tt.stdinTTY, tt.stdoutTTY); got != tt.want {
				t.Fatalf("Decide(%q, %t, %t) = %v, want %v", tt.args, tt.stdinTTY, tt.stdoutTTY, got, tt.want)
			}
		})
	}
}
