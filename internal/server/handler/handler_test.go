package handler

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/blak0p/relay-mcp/internal/session/error"
	"github.com/blak0p/relay-mcp/internal/session/liveness"
	"github.com/blak0p/relay-mcp/internal/session/output"
	"github.com/blak0p/relay-mcp/internal/session/registry"
	"github.com/blak0p/relay-mcp/internal/session/session"
	"github.com/creack/pty"
)

// sessionIDFormat matches the id format produced by idgen.New (term_ + 16 hex).
var sessionIDFormat = regexp.MustCompile(`^term_[0-9a-f]{16}$`)

func TestCreateTerminal_StartsOutputReaderBeforeReturning(t *testing.T) {
	if testing.Short() {
		t.Skip("requires a real PTY")
	}

	for _, tt := range []struct {
		name   string
		script string
		want   string
	}{
		{name: "clean exit", script: "printf created", want: "created"},
		{name: "error exit", script: "printf failed; exit 1", want: "failed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			reg := registry.NewRegistry()
			create := New(reg, WithSpawner(func(_ *exec.Cmd) (*os.File, *exec.Cmd, error) {
				cmd := exec.Command("bash", "-c", tt.script)
				ptyFile, err := pty.Start(cmd)
				return ptyFile, cmd, err
			}))

			result, err := create(context.Background(), mcp.CallToolRequest{})
			if err != nil {
				t.Fatalf("create terminal: %v", err)
			}
			if result.IsError {
				t.Fatalf("create terminal returned tool error: %v", result.Content)
			}

			s, err := reg.Get()
			if err != nil {
				t.Fatalf("registry.Get: %v", err)
			}
			t.Cleanup(func() { _ = s.Close() })

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			snapshot, err := s.Output.Snapshot(ctx, 0, output.MaxReadBytes, time.Second)
			if err != nil {
				t.Fatalf("output snapshot: %v", err)
			}
			if !strings.Contains(string(snapshot.Output), tt.want) {
				t.Fatalf("retained output = %q, want %q", snapshot.Output, tt.want)
			}
		})
	}
}

// errorPayload is the JSON shape the handler writes into CallToolResult.Content
// for tool-level errors. It preserves the JSON-RPC-style code/message/data
// triple that the design mandates, even though mcp-go surfaces tool failures
// as CallToolResult{IsError:true} rather than as JSON-RPC error responses.
type errorPayload struct {
	Code    int            `json:"code"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data,omitempty"`
}

// resultPayload is the JSON shape of a successful create_terminal result.
type resultPayload struct {
	ID        string `json:"id"`
	State     string `json:"state"`
	StartedAt string `json:"started_at"`
}

// extractResult parses the JSON text content of a successful CallToolResult.
func extractResult(t *testing.T, res *mcp.CallToolResult) resultPayload {
	t.Helper()
	if res == nil {
		t.Fatal("result is nil")
	}
	if res.IsError {
		t.Fatalf("result.IsError = true, want false; content=%v", res.Content)
	}
	if len(res.Content) == 0 {
		t.Fatal("result.Content is empty")
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] = %T, want mcp.TextContent", res.Content[0])
	}
	out, err := parseJSON[resultPayload](tc.Text)
	if err != nil {
		t.Fatalf("parse result: %v (raw=%q)", err, tc.Text)
	}
	return out
}

// extractError parses the JSON text content of an errored CallToolResult and
// returns the code, message, and data map.
func extractError(t *testing.T, res *mcp.CallToolResult) errorPayload {
	t.Helper()
	if res == nil {
		t.Fatal("result is nil")
	}
	if !res.IsError {
		t.Fatalf("result.IsError = false, want true; content=%v", res.Content)
	}
	if len(res.Content) == 0 {
		t.Fatal("result.Content is empty")
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] = %T, want mcp.TextContent", res.Content[0])
	}
	out, err := parseJSON[errorPayload](tc.Text)
	if err != nil {
		t.Fatalf("parse error: %v (raw=%q)", err, tc.Text)
	}
	return out
}

func TestCreateTerminalHandler_HappyPath(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not in PATH; skipping spawn test")
	}
	reg := registry.NewRegistry()
	h := New(reg)

	res, err := h(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("handler returned Go error: %v; want nil (tool errors go in IsError)", err)
	}
	out := extractResult(t, res)
	if !sessionIDFormat.MatchString(out.ID) {
		t.Fatalf("result.ID = %q, want match %s", out.ID, sessionIDFormat.String())
	}
	if out.State != string(session.StateRunning) {
		t.Fatalf("result.State = %q, want %q", out.State, session.StateRunning)
	}
	if out.StartedAt == "" {
		t.Fatal("result.StartedAt is empty")
	}

	// The session must be registered and retrievable.
	got, gerr := reg.Get()
	if gerr != nil {
		t.Fatalf("reg.Get after handler = %v, want nil", gerr)
	}
	if got.ID != out.ID {
		t.Fatalf("registered session ID = %q, want %q", got.ID, out.ID)
	}
	// Best-effort cleanup so later subtests don't hold the PTY open.
	_ = got.Close()
}

func TestCreateTerminalHandler_SessionAlreadyExists(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not in PATH; skipping spawn test")
	}
	reg := registry.NewRegistry()
	h := New(reg)

	// First call seeds the registry.
	first, err := h(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	firstOut := extractResult(t, first)
	t.Cleanup(func() {
		if s, gerr := reg.Get(); gerr == nil {
			_ = s.Close()
		}
	})

	// Second call must fail with -32001 and the existing id.
	second, err := h(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("second call returned Go error: %v; want nil (tool errors go in IsError)", err)
	}
	e := extractError(t, second)
	if e.Code != -32001 {
		t.Fatalf("error code = %d, want -32001", e.Code)
	}
	if !strings.Contains(e.Message, "session") || !strings.Contains(e.Message, "active") {
		t.Fatalf("error message = %q, want it to mention an active session", e.Message)
	}
	if e.Data == nil {
		t.Fatal("error.Data is nil, want existing session id")
	}
	existingID, _ := e.Data["existing_id"].(string)
	if existingID != firstOut.ID {
		t.Fatalf("error.Data.existing_id = %q, want %q", existingID, firstOut.ID)
	}

	// Sanity: the registry must still hold the original session.
	got, gerr := reg.Get()
	if gerr != nil {
		t.Fatalf("reg.Get after duplicate = %v, want nil", gerr)
	}
	if got.ID != firstOut.ID {
		t.Fatalf("registry was mutated by rejected call; got %q, want %q", got.ID, firstOut.ID)
	}
}

func TestCreateTerminalHandler_BashNotFound(t *testing.T) {
	// Not parallel: t.Setenv mutates the process environment.
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not in PATH; skipping bash_not_found test")
	}
	// Force PATH to a directory with no bash. /nonexistent is guaranteed absent.
	t.Setenv("PATH", "/nonexistent")

	reg := registry.NewRegistry()
	h := New(reg)

	res, err := h(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("handler returned Go error: %v; want nil (tool errors go in IsError)", err)
	}
	e := extractError(t, res)
	if e.Code != -32002 {
		t.Fatalf("error code = %d, want -32002 (bash_not_found)", e.Code)
	}
	if !strings.Contains(e.Message, "bash") {
		t.Fatalf("error message = %q, want it to mention bash", e.Message)
	}

	// No session must have been registered.
	if _, gerr := reg.Get(); !errors.Is(gerr, serror.ErrSessionNotFound) {
		t.Fatalf("reg.Get after bash_not_found = %v, want ErrSessionNotFound", gerr)
	}
}

// --- T-WT-08: writeTerminalHandler success path ---

// writeTerminalResultPayload is the JSON shape of a successful write_terminal
// result: {bytes_written, state}.
type writeTerminalResultPayload struct {
	BytesWritten int    `json:"bytes_written"`
	State        string `json:"state"`
}

// extractWriteResult parses the JSON text content of a successful
// write_terminal CallToolResult.
func extractWriteResult(t *testing.T, res *mcp.CallToolResult) writeTerminalResultPayload {
	t.Helper()
	if res == nil {
		t.Fatal("result is nil")
	}
	if res.IsError {
		t.Fatalf("result.IsError = true, want false; content=%v", res.Content)
	}
	if len(res.Content) == 0 {
		t.Fatal("result.Content is empty")
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] = %T, want mcp.TextContent", res.Content[0])
	}
	out, err := parseJSON[writeTerminalResultPayload](tc.Text)
	if err != nil {
		t.Fatalf("parse write_terminal result: %v (raw=%q)", err, tc.Text)
	}
	return out
}

// newWriteRequest builds a CallToolRequest with the given data argument.
func newWriteRequest(data string) mcp.CallToolRequest {
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "write_terminal",
			Arguments: map[string]any{"data": data, "ensure_newline": false},
		},
	}
}

// seedLiveSession spawns a real bash PTY, registers it in reg, and returns the
// session plus a cleanup func. Used by write_terminal handler tests that need
// a genuinely alive session exercising the real Session.Write path.
func seedLiveSession(t *testing.T, reg *registry.Registry) *session.Session {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not in PATH; skipping write_terminal handler test")
	}
	cmd := exec.Command("bash", "--norc", "-i")
	ptyFile, _, err := defaultSpawner(cmd)
	if err != nil {
		t.Fatalf("defaultSpawner: %v", err)
	}
	s := session.New(cmd, ptyFile)
	if cmd.Process != nil {
		s.PID = cmd.Process.Pid
	}
	if err := reg.Put(s); err != nil {
		_ = ptyFile.Close()
		t.Fatalf("reg.Put: %v", err)
	}
	t.Cleanup(func() {
		_ = s.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	})
	return s
}

// TestWriteTerminalHandler_Success proves the happy path: with a live session
// registered, calling write_terminal with data "hello" returns
// {bytes_written: 5, state: "running"} and no error. REQ-WT-001, REQ-WT-009.
func TestWriteTerminalHandler_Success(t *testing.T) {
	t.Parallel()
	reg := registry.NewRegistry()
	s := seedLiveSession(t, reg)
	h := NewWriteTerminalHandler(reg)

	res, err := h(context.Background(), newWriteRequest("hello"))
	if err != nil {
		t.Fatalf("handler returned Go error: %v; want nil (tool errors go in IsError)", err)
	}
	out := extractWriteResult(t, res)
	if out.BytesWritten != 5 {
		t.Fatalf("bytes_written = %d, want 5 (len(\"hello\"))", out.BytesWritten)
	}
	if out.State != string(session.StateRunning) {
		t.Fatalf("state = %q, want %q", out.State, session.StateRunning)
	}
	// Best-effort: confirm the session is still registered and the same one.
	got, gerr := reg.Get()
	if gerr != nil {
		t.Fatalf("reg.Get after write: %v", gerr)
	}
	if got.ID != s.ID {
		t.Fatalf("registry session id = %q, want %q", got.ID, s.ID)
	}
}

// TestWriteTerminalHandler_MissingSession proves the empty-registry path: with
// no session registered, the handler returns an error envelope with
// codeSessionNotFound (-32004) and the message is non-empty. REQ-WT-002.
func TestWriteTerminalHandler_MissingSession(t *testing.T) {
	t.Parallel()
	reg := registry.NewRegistry()
	h := NewWriteTerminalHandler(reg)

	res, err := h(context.Background(), newWriteRequest("hi"))
	if err != nil {
		t.Fatalf("handler returned Go error: %v; want nil (tool errors go in IsError)", err)
	}
	e := extractError(t, res)
	if e.Code != codeSessionNotFound {
		t.Fatalf("error code = %d, want %d (codeSessionNotFound)", e.Code, codeSessionNotFound)
	}
	if e.Message == "" {
		t.Fatal("error message is empty, want a non-empty message")
	}
}

// --- T-WT-09: error mapping to stable codes ---

// TestWriteTerminalHandler_MissingData proves the invalid-argument path: with
// no "data" argument, the handler returns codeInvalidArgument (-32602).
// REQ-WT-008 (parameter schema) + the design's error mapping table.
func TestWriteTerminalHandler_MissingData(t *testing.T) {
	t.Parallel()
	reg := registry.NewRegistry()
	seedLiveSession(t, reg) // a live session is registered so we isolate the arg failure
	h := NewWriteTerminalHandler(reg)

	// No "data" key in arguments.
	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "write_terminal",
			Arguments: map[string]any{},
		},
	}
	res, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("handler returned Go error: %v; want nil (tool errors go in IsError)", err)
	}
	e := extractError(t, res)
	if e.Code != codeInvalidArgument {
		t.Fatalf("error code = %d, want %d (codeInvalidArgument)", e.Code, codeInvalidArgument)
	}
	if !strings.Contains(e.Message, "data") {
		t.Fatalf("error message = %q, want it to mention 'data'", e.Message)
	}
}

// TestWriteTerminalHandler_Oversize proves the size-cap path: data larger than
// 1 MiB yields codeWriteTooLarge (-32006) and the message contains the limit
// value (1048576). REQ-WT-003.
func TestWriteTerminalHandler_Oversize(t *testing.T) {
	t.Parallel()
	reg := registry.NewRegistry()
	s := seedLiveSession(t, reg)
	h := NewWriteTerminalHandler(reg)

	oversize := strings.Repeat("x", session.MaxWriteBytes+1)
	res, err := h(context.Background(), newWriteRequest(oversize))
	if err != nil {
		t.Fatalf("handler returned Go error: %v; want nil (tool errors go in IsError)", err)
	}
	e := extractError(t, res)
	if e.Code != codeWriteTooLarge {
		t.Fatalf("error code = %d, want %d (codeWriteTooLarge)", e.Code, codeWriteTooLarge)
	}
	if !strings.Contains(e.Message, "1048576") {
		t.Fatalf("error message = %q, want it to contain the limit 1048576", e.Message)
	}
	// The session id must be referenced in the data payload for traceability.
	if e.Data == nil {
		t.Fatal("error.Data is nil, want session_id")
	}
	if got, _ := e.Data["session_id"].(string); got != s.ID {
		t.Fatalf("error.Data.session_id = %q, want %q", got, s.ID)
	}
}

// TestWriteTerminalHandler_DeadSession proves the liveness-gate path: when the
// bash process is dead, the handler returns codeSessionNotAlive (-32005) and
// the message contains the session id. REQ-WT-002.
func TestWriteTerminalHandler_DeadSession(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not in PATH; skipping dead-session handler test")
	}
	reg := registry.NewRegistry()
	s := seedLiveSession(t, reg)
	h := NewWriteTerminalHandler(reg)

	// Kill the bash process and reap it so IsAlive flips to false.
	if err := s.Cmd.Process.Kill(); err != nil {
		t.Fatalf("kill bash: %v", err)
	}
	_, _ = s.Cmd.Process.Wait()

	// Wait until the PID is genuinely dead (liveness.IsAlive returns false).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && liveness.IsAlive(s.PID) {
		time.Sleep(10 * time.Millisecond)
	}
	if liveness.IsAlive(s.PID) {
		t.Fatalf("pid %d still alive after 2s", s.PID)
	}

	res, err := h(context.Background(), newWriteRequest("post-mortem"))
	if err != nil {
		t.Fatalf("handler returned Go error: %v; want nil (tool errors go in IsError)", err)
	}
	e := extractError(t, res)
	if e.Code != codeSessionNotAlive {
		t.Fatalf("error code = %d, want %d (codeSessionNotAlive)", e.Code, codeSessionNotAlive)
	}
	if !strings.Contains(e.Message, s.ID) {
		t.Fatalf("error message = %q, want it to contain session id %q", e.Message, s.ID)
	}
}

// TestWriteTerminalHandler_ClosedSession proves the closed-session path: after
// Close() is called, the handler returns codeSessionClosed (-32007).
// REQ-WT-006.
func TestWriteTerminalHandler_ClosedSession(t *testing.T) {
	t.Parallel()
	reg := registry.NewRegistry()
	s := seedLiveSession(t, reg)
	h := NewWriteTerminalHandler(reg)

	// Close the session's PTY. The registry still holds the (closed) session,
	// so reg.Get returns it and Write observes closed=true.
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	res, err := h(context.Background(), newWriteRequest("after-close"))
	if err != nil {
		t.Fatalf("handler returned Go error: %v; want nil (tool errors go in IsError)", err)
	}
	e := extractError(t, res)
	if e.Code != codeSessionClosed {
		t.Fatalf("error code = %d, want %d (codeSessionClosed)", e.Code, codeSessionClosed)
	}
	if !strings.Contains(e.Message, s.ID) {
		t.Fatalf("error message = %q, want it to contain session id %q", e.Message, s.ID)
	}
}

func TestCreateTerminalHandler_SpawnFailed(t *testing.T) {
	t.Parallel()
	// Use a real, discoverable shell but force the spawn to fail by pointing
	// SHELL at a non-executable file. The handler uses exec.LookPath("bash");
	// when bash is present we instead override the spawn via the WithSpawner
	// option to force an error from pty.StartWithSize. This isolates the
	// spawn_failed path from the bash_not_found path.
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not in PATH; skipping spawn_failed test")
	}
	reg := registry.NewRegistry()
	h := New(reg, WithSpawner(funcaltySpawner))

	res, err := h(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("handler returned Go error: %v; want nil (tool errors go in IsError)", err)
	}
	e := extractError(t, res)
	if e.Code != -32003 {
		t.Fatalf("error code = %d, want -32003 (spawn_failed)", e.Code)
	}
	if !strings.Contains(e.Message, "spawn") {
		t.Fatalf("error message = %q, want it to mention spawn", e.Message)
	}

	// No session must have been registered.
	if _, gerr := reg.Get(); !errors.Is(gerr, serror.ErrSessionNotFound) {
		t.Fatalf("reg.Get after spawn_failed = %v, want ErrSessionNotFound", gerr)
	}
}
