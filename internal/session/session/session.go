package session

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/blak0p/relay-mcp/internal/idgen"
	serror "github.com/blak0p/relay-mcp/internal/session/error"
	"github.com/blak0p/relay-mcp/internal/session/liveness"
	"github.com/blak0p/relay-mcp/internal/session/output"
)

// MaxWriteBytes is the maximum number of bytes accepted in a single Write
// call. The cap is a hard rejection (REQ-WT-003), not a chunking primitive.
const MaxWriteBytes = 1 << 20 // 1 MiB

// ptyWriter is the write surface of the PTY master. *os.File satisfies it.
// It exists as an interface so tests can inject a stub (partial writes,
// blocking writes) without touching the real FD.
type ptyWriter interface {
	Write([]byte) (int, error)
}

// SessionState is the lifecycle state of a Session.
type SessionState string

const (
	// StateRunning is the state at creation. The stored status remains
	// "running" after bash dies until a session operation runs the lazy
	// liveness check (REQ-007).
	StateRunning SessionState = "running"

	// StateExited means bash exited cleanly (exit code 0) and the next
	// liveness check reconciled the stored state.
	StateExited SessionState = "exited"

	// StateError means bash died from a signal or non-zero exit and the
	// next liveness check reconciled the stored state.
	StateError SessionState = "error"
)

// CloseResult describes the final process state observed during Shutdown.
type CloseResult struct {
	State    SessionState
	ExitCode int
}

// Session represents one PTY-backed bash process.
type Session struct {
	ID        string
	PTY       *os.File  // master end of the PTY
	Cmd       *exec.Cmd // bash process handle
	PID       int       // cached for liveness checks
	StartedAt time.Time
	State     SessionState
	// Output retains terminal bytes for future read_terminal consumers. Exactly
	// one reader appends to it for the lifetime of the session.
	Output *output.Broker

	closed atomic.Bool // guards Close against double-close; atomic for lock-free reads from Write
	mu     sync.Mutex  // guards State and the double-close idempotency check in Close

	writeMu       sync.Mutex // serializes concurrent Write calls so byte streams do not interleave in the PTY (REQ-WT-005)
	ptyWriter     ptyWriter  // write target; defaults to PTY. Overridden only by tests via setPtyWriterForTest.
	outputOnce    sync.Once  // ensures no consumer competes with the PTY output reader
	outputStarted atomic.Bool
	outputDone    chan struct{}
	waitOnce      sync.Once // makes Cmd.Wait authoritative for both output and shutdown paths
	waitDone      chan struct{}
	waitErr       error
	waitCalls     atomic.Int32 // invariant counter: Cmd.Wait must execute exactly once
	signalGroup   func(int, syscall.Signal) error
}

// New constructs a Session from a started (or about-to-start) command and its
// PTY master file. The id is generated via idgen.New(); the state is
// StateRunning; StartedAt is time.Now(). New does NOT start the process or
// touch the PTY — that is the caller's responsibility (creack/pty in the
// handler).
func New(cmd *exec.Cmd, pty *os.File) *Session {
	return &Session{
		ID:         idgen.New(),
		PTY:        pty,
		Cmd:        cmd,
		StartedAt:  time.Now(),
		State:      StateRunning,
		Output:     output.New(output.DefaultCapacity),
		ptyWriter:  pty, // default write target is the real PTY; tests override via setPtyWriterForTest
		outputDone: make(chan struct{}),
		waitDone:   make(chan struct{}),
		signalGroup: func(pgid int, signal syscall.Signal) error {
			return syscall.Kill(-pgid, signal)
		},
	}
}

// StartOutput begins the session's sole PTY reader. It is safe to call more
// than once; only the first call starts a goroutine so output is never split
// between competing consumers.
func (s *Session) StartOutput() {
	s.outputOnce.Do(func() {
		s.outputStarted.Store(true)
		go s.readOutput()
	})
}

func (s *Session) readOutput() {
	defer close(s.outputDone)
	if s.PTY == nil {
		s.finishOutput(output.StatusError, StateError)
		return
	}

	buffer := make([]byte, output.DefaultReadBytes)
	for {
		n, err := s.PTY.Read(buffer)
		if n > 0 {
			s.Output.Append(buffer[:n])
		}
		if err == nil {
			continue
		}
		if s.closed.Load() {
			return
		}
		if isTerminalReadEnd(err) {
			s.finishOutputAfterExit()
			return
		}
		s.finishOutput(output.StatusError, StateError)
		return
	}
}

func isTerminalReadEnd(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, syscall.EIO)
}

func (s *Session) finishOutputAfterExit() {
	state := StateExited
	if s.Cmd != nil && s.Cmd.Process != nil {
		if err := s.wait(); err != nil {
			state = StateError
		}
	}
	if state == StateExited {
		s.finishOutput(output.StatusExited, state)
		return
	}
	s.finishOutput(output.StatusError, state)
}

func (s *Session) wait() error {
	s.waitOnce.Do(func() {
		s.waitCalls.Add(1)
		if s.Cmd == nil || s.Cmd.Process == nil {
			s.waitErr = fmt.Errorf("wait session: missing process")
		} else {
			s.waitErr = s.Cmd.Wait()
		}
		close(s.waitDone)
	})
	<-s.waitDone
	return s.waitErr
}

func (s *Session) setSignalGroupForTest(signal func(int, syscall.Signal) error) {
	s.signalGroup = signal
}

func (s *Session) finishShutdown(result CloseResult) (CloseResult, error) {
	s.mu.Lock()
	s.State = result.State
	s.mu.Unlock()
	if err := s.Close(); err != nil {
		return result, errors.Join(serror.ErrSessionCleanup, fmt.Errorf("close PTY: %w", err))
	}
	return result, nil
}

func (s *Session) failShutdown(result CloseResult, cause error) (CloseResult, error) {
	if err := s.Close(); err != nil {
		return result, errors.Join(serror.ErrSessionCleanup, cause, fmt.Errorf("close PTY: %w", err))
	}
	return result, errors.Join(serror.ErrSessionCleanup, cause)
}

func (s *Session) finishOutput(status output.Status, state SessionState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed.Load() {
		return
	}
	s.State = state
	s.Output.SetStatus(status)
}

// closeClosed tracks whether Close has already run. The flag is an
// atomic.Bool so Write can read it lock-free (no need to acquire s.mu to
// observe closed). The idempotent double-close check itself is still
// guarded by s.mu.
//
// Close releases the PTY master file descriptor. It does NOT kill or wait on
// the bash process — process teardown is the caller's responsibility (e.g.
// creack/pty or the close_terminal handler). Close is idempotent: subsequent
// calls return nil without touching the (already-closed) FD.
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed.Load() {
		return nil
	}
	s.closed.Store(true)
	if s.Output != nil {
		s.Output.SetStatus(output.StatusClosed)
	}
	if s.PTY != nil {
		return s.PTY.Close()
	}
	return nil
}

// Shutdown terminates the session's complete process group and reaps its
// leader. It gives SIGTERM the supplied grace period before sending SIGKILL.
func (s *Session) Shutdown(grace time.Duration) (CloseResult, error) {
	s.mu.Lock()
	state := s.State
	s.mu.Unlock()
	result := CloseResult{State: state, ExitCode: -1}
	if s.Cmd == nil || s.Cmd.Process == nil {
		return s.failShutdown(result, errors.New("missing process"))
	}
	if !s.outputStarted.Load() && s.Cmd.ProcessState != nil {
		result.State = classifyExit(s.Cmd)
		result.ExitCode = s.Cmd.ProcessState.ExitCode()
		return s.finishShutdown(result)
	}
	// Cmd.Wait mutates ProcessState. Only inspect it after the shared wait
	// completes so Shutdown cannot race with the output reader.
	select {
	case <-s.waitDone:
		result.State = classifyExit(s.Cmd)
		if s.Cmd.ProcessState != nil && s.Cmd.ProcessState.Exited() {
			result.ExitCode = s.Cmd.ProcessState.ExitCode()
		}
		return s.finishShutdown(result)
	default:
	}
	pid := s.PID
	if pid == 0 {
		pid = s.Cmd.Process.Pid
	}
	if err := s.signalGroup(pid, syscall.SIGTERM); err != nil {
		return s.failShutdown(result, fmt.Errorf("terminate process group %d: %w", pid, err))
	}
	waitDone := make(chan error, 1)
	go func() { waitDone <- s.wait() }()
	if grace < 0 {
		grace = 0
	}
	select {
	case <-waitDone:
	case <-time.After(grace):
		if err := s.signalGroup(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			return s.failShutdown(result, fmt.Errorf("force terminate process group %d: %w", pid, err))
		}
		<-waitDone
	}
	result.State = classifyExit(s.Cmd)
	if s.Cmd.ProcessState != nil && s.Cmd.ProcessState.Exited() {
		result.ExitCode = s.Cmd.ProcessState.ExitCode()
	}
	return s.finishShutdown(result)
}

// ReconcileState performs the lazy liveness reconciliation mandated by the
// session lifecycle. If the stored state is StateRunning and the process is
// no longer alive, the state is flipped to:
//   - StateExited if the process exited with code 0 (clean exit), or if we
//     cannot determine the exit code (no Cmd, no ProcessState — assume clean).
//   - StateError if the process died from a signal or a non-zero exit code.
//
// If the process is still alive, or the state is already not StateRunning,
// ReconcileState is a no-op. It is safe to call concurrently and to call
// repeatedly. It does not panic on nil Cmd or nil ProcessState.
//
// ReconcileState is exported because sibling sub-packages under the session
// namespace (notably registry) need to trigger reconciliation on Get; it is
// not part of the external API of relay-mcp.
func (s *Session) ReconcileState() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.State != StateRunning {
		return
	}
	if liveness.IsAlive(s.PID) {
		return
	}
	s.State = classifyExit(s.Cmd)
}

// classifyExit decides StateExited vs StateError from the available process
// state. When no information is available (nil Cmd or nil ProcessState) we
// assume a clean exit: the process is dead and we have no evidence of a
// signal/non-zero exit, so defaulting to StateExited is the least surprising
// choice.
func classifyExit(cmd *exec.Cmd) SessionState {
	if cmd == nil || cmd.ProcessState == nil {
		return StateExited
	}
	if cmd.ProcessState.Exited() && cmd.ProcessState.ExitCode() == 0 {
		return StateExited
	}
	return StateError
}

// Write injects raw bytes into the session's PTY master. It is safe for
// concurrent use: writes to the same session are serialized via writeMu so
// their byte streams do not interleave in the PTY (REQ-WT-005).
//
// Write rejects data larger than MaxWriteBytes with ErrWriteTooLarge before
// acquiring any lock (REQ-WT-003). Under writeMu it re-checks the closed flag
// and returns ErrSessionClosed if Close has been called (REQ-WT-006). It then
// performs the PTY write and returns the byte count the kernel accepted.
//
// Partial writes (n < len(data), err == nil) are NOT retried: the caller is
// responsible for resending the remainder (REQ-WT-004).
func (s *Session) Write(data []byte) (int, error) {
	if len(data) > MaxWriteBytes {
		return 0, fmt.Errorf("%w: %d > %d", serror.ErrWriteTooLarge, len(data), MaxWriteBytes)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.closed.Load() {
		return 0, serror.ErrSessionClosed
	}
	// Liveness gate: if the underlying bash process is dead, flip the
	// state to StateError and refuse the write. This is the lazy
	// reconciliation path for write_terminal (REQ-WT-002). The race window
	// between this check and the PTY write is acceptable and documented —
	// the kernel will reject a write to a stale FD and the handler surfaces
	// the I/O error.
	if !liveness.IsAlive(s.PID) {
		s.mu.Lock()
		s.State = StateError
		s.mu.Unlock()
		return 0, fmt.Errorf("%w: session %s is not alive", serror.ErrSessionNotAlive, s.ID)
	}
	if s.ptyWriter == nil {
		return 0, fmt.Errorf("%w: PTY writer not configured", serror.ErrSessionClosed)
	}
	return s.ptyWriter.Write(data)
}

// setPtyWriterForTest replaces the PTY write target with a test stub. It is
// only safe to call before any concurrent Write; it exists to let tests
// inject a controllable writer (partial writes, blocking writes) without
// touching the real PTY FD. Test-only hook.
func (s *Session) setPtyWriterForTest(w ptyWriter) {
	s.ptyWriter = w
}
