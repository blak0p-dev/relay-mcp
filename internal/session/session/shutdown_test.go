package session

import (
	"bufio"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	serror "github.com/blak0p/relay-mcp/internal/session/error"
	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

func TestSessionShutdown_TerminatesEntireProcessGroup(t *testing.T) {
	if testing.Short() {
		t.Skip("requires a real PTY process group")
	}

	s, childPID := startShutdownTestSession(t, "sleep 30 & echo $!; wait")
	if _, err := s.Shutdown(100 * time.Millisecond); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	waitForPIDExit(t, childPID)
	waitForPIDExit(t, s.PID)
}

func TestSessionShutdown_ForceKillsProcessGroupAfterGrace(t *testing.T) {
	if testing.Short() {
		t.Skip("requires a real PTY process group")
	}

	s, _ := startShutdownTestSession(t, "trap '' HUP; while :; do sleep 1; done & echo $!; wait")
	type shutdownResult struct {
		result CloseResult
		err    error
	}
	done := make(chan shutdownResult, 1)
	t.Cleanup(func() {
		_ = unix.Kill(-s.PID, unix.SIGKILL)
		select {
		case <-done:
		case <-time.After(time.Second):
		}
	})

	go func() {
		result, err := s.Shutdown(100 * time.Millisecond)
		done <- shutdownResult{result: result, err: err}
	}()

	select {
	case outcome := <-done:
		if outcome.err != nil {
			t.Fatalf("Shutdown() error = %v", outcome.err)
		}
		if outcome.result.State != StateError || outcome.result.ExitCode != -1 {
			t.Fatalf("Shutdown() result = %#v, want StateError with exit code -1", outcome.result)
		}
	case <-time.After(time.Second):
		t.Fatal("Shutdown() did not force-kill the process group after the grace period")
	}

	waitForPIDExit(t, s.PID)
}

func TestSessionShutdown_SharesOneWaitWithOutputReader(t *testing.T) {
	if testing.Short() {
		t.Skip("requires a real PTY process group")
	}

	s, _ := startShutdownTestSession(t, "sleep 30 & echo $!; wait")
	s.StartOutput()

	result, err := s.Shutdown(100 * time.Millisecond)
	if err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if result.State != StateExited || result.ExitCode != 0 {
		t.Fatalf("Shutdown() result = %#v, want clean exit after SIGTERM", result)
	}
	select {
	case <-s.outputDone:
	case <-time.After(time.Second):
		t.Fatal("output reader did not finish after Shutdown")
	}
	if got := s.waitCalls.Load(); got != 1 {
		t.Fatalf("Cmd.Wait calls = %d, want 1", got)
	}
}

func TestSessionShutdown_ReturnsCleanupErrorWhenGroupSignalFails(t *testing.T) {
	if testing.Short() {
		t.Skip("requires a real PTY process group")
	}

	s, _ := startShutdownTestSession(t, "sleep 30 & echo $!; wait")
	t.Cleanup(func() {
		_ = unix.Kill(-s.PID, unix.SIGKILL)
		_ = s.Cmd.Wait()
	})
	s.setSignalGroupForTest(func(int, syscall.Signal) error {
		return errors.New("injected signal failure")
	})

	_, err := s.Shutdown(100 * time.Millisecond)
	if !errors.Is(err, serror.ErrSessionCleanup) {
		t.Fatalf("Shutdown() error = %v, want errors.Is(_, ErrSessionCleanup)", err)
	}
	if !s.closed.Load() {
		t.Fatal("Shutdown() left the PTY open after cleanup failure")
	}
}

func TestSessionShutdown_HandlesProcessExitBeforeTerminateSignal(t *testing.T) {
	if testing.Short() {
		t.Skip("requires a real PTY process group")
	}

	s, _ := startShutdownTestSession(t, "echo 123; exit 0")
	s.setSignalGroupForTest(func(int, syscall.Signal) error {
		return syscall.ESRCH
	})

	result, err := s.Shutdown(100 * time.Millisecond)
	if err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if result.State != StateExited || result.ExitCode != 0 {
		t.Fatalf("Shutdown() result = %#v, want clean exit after process disappeared", result)
	}
}

func startShutdownTestSession(t *testing.T, script string) (*Session, int) {
	t.Helper()
	cmd := exec.Command("bash", "-c", script)
	ptyFile, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty.Start: %v", err)
	}

	line, err := bufio.NewReader(ptyFile).ReadString('\n')
	if err != nil {
		_ = ptyFile.Close()
		t.Fatalf("read child pid: %v", err)
	}
	childPID, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		_ = ptyFile.Close()
		t.Fatalf("parse child pid %q: %v", line, err)
	}

	s := New(cmd, ptyFile)
	s.PID = cmd.Process.Pid
	t.Cleanup(func() { _ = s.Close() })
	return s, childPID
}

func waitForPIDExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if err := unix.Kill(pid, 0); err == unix.ESRCH {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("pid %d remained alive", pid)
}
