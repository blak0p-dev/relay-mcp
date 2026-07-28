package main

import (
	"bytes"
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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
	defer terminal.Close()
	if _, err := terminal.Write([]byte("\x1b[24;80R\x1b]11;rgb:0000/0000/0000\x1b\\")); err != nil {
		t.Fatalf("answer TUI terminal queries: %v", err)
	}
	buffer := make([]byte, 4096)
	var output strings.Builder
	for !strings.Contains(output.String(), "Relay installer") {
		read := make(chan error, 1)
		go func() {
			n, err := terminal.Read(buffer)
			if err == nil {
				output.Write(buffer[:n])
			}
			read <- err
		}()
		select {
		case readErr := <-read:
			if readErr != nil {
				t.Fatalf("read TUI output: %v", readErr)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for installer TUI")
		}
	}
	if got := output.String(); !strings.Contains(got, "Relay installer") {
		t.Fatalf("dual-TTY output = %q, want installer marker", got)
	}
	if got := output.String(); !strings.Contains(got, "\x1b[?1049h") {
		t.Fatalf("dual-TTY output = %q, want alt-screen entry", got)
	}
	if _, err := terminal.Write([]byte("q")); err != nil {
		t.Fatalf("send TUI quit key: %v", err)
	}
	for !strings.Contains(output.String(), "\x1b[?1049l") {
		read := make(chan error, 1)
		go func() {
			n, err := terminal.Read(buffer)
			if err == nil {
				output.Write(buffer[:n])
			}
			read <- err
		}()
		select {
		case readErr := <-read:
			if readErr != nil {
				t.Fatalf("read TUI cleanup: %v", readErr)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for TUI alt-screen cleanup")
		}
	}
	if waitErr := cmd.Wait(); waitErr != nil {
		t.Fatalf("dual-TTY launch exit = %v, want success", waitErr)
	}
	if got := output.String(); !strings.Contains(got, "\x1b[?1049l") {
		t.Fatalf("dual-TTY output = %q, want alt-screen cleanup", got)
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
