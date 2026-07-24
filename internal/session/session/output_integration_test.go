package session

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/blak0p/relay-mcp/internal/session/output"
	"github.com/creack/pty"
)

func TestSessionOutput_SingleReaderRetainsTailBeforeExit(t *testing.T) {
	if testing.Short() {
		t.Skip("requires a real PTY")
	}

	s := startOutputTestSession(t, "printf tail")
	t.Cleanup(func() { _ = s.Close() })

	s.StartOutput()
	s.StartOutput()

	snapshot := waitForTerminalOutput(t, s, output.StatusExited)
	if got := string(snapshot.Output); !strings.Contains(got, "tail") {
		t.Fatalf("retained output = %q, want tail", got)
	}
	if snapshot.Status != output.StatusExited {
		t.Fatalf("status = %q, want %q", snapshot.Status, output.StatusExited)
	}
}

func TestSessionOutput_CloseUnblocksReader(t *testing.T) {
	if testing.Short() {
		t.Skip("requires a real PTY")
	}

	s := startOutputTestSession(t, "sleep 10")
	t.Cleanup(func() {
		if s.Cmd.Process != nil {
			_ = s.Cmd.Process.Kill()
			_ = s.Cmd.Wait()
		}
	})
	s.StartOutput()

	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	snapshot := waitForTerminalOutput(t, s, output.StatusClosed)
	if snapshot.Status != output.StatusClosed {
		t.Fatalf("status = %q, want %q", snapshot.Status, output.StatusClosed)
	}
	select {
	case <-s.outputDone:
	case <-time.After(time.Second):
		t.Fatal("PTY reader remained blocked after Close")
	}
}

func TestSessionOutput_RetainsOutputAfterTerminalError(t *testing.T) {
	if testing.Short() {
		t.Skip("requires a real PTY")
	}

	s := startOutputTestSession(t, "printf failure; exit 1")
	t.Cleanup(func() { _ = s.Close() })
	s.StartOutput()

	snapshot := waitForTerminalOutput(t, s, output.StatusError)
	if got := string(snapshot.Output); !strings.Contains(got, "failure") {
		t.Fatalf("retained output = %q, want failure", got)
	}
	if snapshot.Status != output.StatusError {
		t.Fatalf("status = %q, want %q", snapshot.Status, output.StatusError)
	}
}

func startOutputTestSession(t *testing.T, script string) *Session {
	t.Helper()
	cmd := exec.Command("bash", "-c", script)
	ptyFile, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty.Start: %v", err)
	}

	s := New(cmd, ptyFile)
	s.PID = cmd.Process.Pid
	return s
}

func waitForTerminalOutput(t *testing.T, s *Session, want output.Status) output.Snapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cursor := int64(0)
	var retained []byte
	for {
		snapshot, err := s.Output.Snapshot(ctx, cursor, output.MaxReadBytes, 100*time.Millisecond)
		if err != nil {
			t.Fatalf("Output.Snapshot: %v", err)
		}
		retained = append(retained, snapshot.Output...)
		cursor = snapshot.NextCursor
		if snapshot.Status == want {
			snapshot.Output = retained
			return snapshot
		}
	}
}
