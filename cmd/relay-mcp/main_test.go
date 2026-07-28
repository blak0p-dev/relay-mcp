package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/creack/pty"
)

func TestRunWithTTYDispatchesWithoutWritingToProtocolOutput(t *testing.T) {
	interactiveErr := errors.New("interactive")
	mcpErr := errors.New("mcp")
	for _, tt := range []struct {
		name                string
		args                []string
		stdinTTY, stdoutTTY bool
		want                error
	}{
		{"dual TTY zero argument launch selects interactive runner", nil, true, true, interactiveErr},
		{"piped stdin selects MCP runner", nil, false, true, mcpErr},
		{"flag invocation selects MCP runner", []string{"--version"}, true, true, mcpErr},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := runWithTTY(tt.args, tt.stdinTTY, tt.stdoutTTY,
				func() error { return interactiveErr },
				func() error { return mcpErr },
			)
			if !errors.Is(got, tt.want) {
				t.Fatalf("runWithTTY() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRelayBinary_DualTTYLaunchSelectsInteractivePath(t *testing.T) {
	if testing.Short() || runtime.GOOS == "windows" {
		t.Skip("PTY compiled-process test is unavailable in this environment")
	}

	cmd := exec.Command(buildRelayBinary(t))
	terminal, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("start relay binary in PTY: %v", err)
	}
	output, readErr := io.ReadAll(terminal)
	if waitErr := cmd.Wait(); waitErr == nil {
		t.Fatal("dual-TTY launch exited successfully, want the temporary interactive seam error")
	}
	if readErr != nil && !errors.Is(readErr, os.ErrClosed) && !errors.Is(readErr, syscall.EIO) {
		t.Fatalf("read PTY output: %v", readErr)
	}
	if !strings.Contains(string(output), "interactive mode is not available yet") {
		t.Fatalf("dual-TTY output = %q, want interactive-path marker", output)
	}
}

func TestWriteError_UsesPublicRelayPrefix(t *testing.T) {
	t.Parallel()
	var stderr bytes.Buffer
	writeError(&stderr, errors.New("serve stdio: broken pipe"))
	if got, want := stderr.String(), "relay: serve stdio: broken pipe\n"; got != want {
		t.Fatalf("writeError output = %q, want %q", got, want)
	}
}

func buildRelayBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "relay")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Stderr = &strings.Builder{}
	if err := build.Run(); err != nil {
		t.Fatalf("go build relay: %v", err)
	}
	return bin
}
