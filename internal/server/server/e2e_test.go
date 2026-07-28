package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// sessionIDFormat matches the id format produced by idgen.New (term_ + 16 hex).
var sessionIDFormat = regexp.MustCompile(`^term_[0-9a-f]{16}$`)

// jsonrpcMessage is a minimal JSON-RPC envelope used to read server responses.
type jsonrpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonrpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// e2eProbe drives a compiled relay-mcp binary over stdio with raw JSON-RPC
// frames so the test can assert on the exact wire format.
type e2eProbe struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	out    *bufio.Reader
}

func newE2EProbe(t *testing.T) *e2eProbe {
	t.Helper()
	return newE2EProbeWithLDFlags(t, "")
}

func newE2EProbeWithLDFlags(t *testing.T, ldflags string) *e2eProbe {
	t.Helper()
	bin := buildBinaryWithLDFlags(t, ldflags)
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "RELAY_MCP_E2E=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	cmd.Stderr = &strings.Builder{}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start relay-mcp: %v", err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	})
	return &e2eProbe{cmd: cmd, stdin: stdin, stdout: stdout, out: bufio.NewReader(stdout)}
}

func buildBinary(t *testing.T) string {
	t.Helper()
	return buildBinaryWithLDFlags(t, "")
}

func buildBinaryWithLDFlags(t *testing.T, ldflags string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "relay-mcp-test")
	args := []string{"build", "-o", bin}
	if ldflags != "" {
		args = append(args, "-ldflags", ldflags)
	}
	args = append(args, "./cmd/relay-mcp")
	build := exec.Command("go", args...)
	// go build resolves ./cmd/relay-mcp relative to the module root; the test
	// binary lives in internal/server/server, so walk up three parents to find
	// the repo root. We detect it by looking for go.mod.
	root := findModuleRoot(t)
	build.Dir = root
	build.Stderr = &strings.Builder{}
	if err := build.Run(); err != nil {
		t.Fatalf("go build ./cmd/relay-mcp: %v", err)
	}
	return bin
}

func TestE2E_InitializeAdvertisesRelayIdentityAndVersion(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping process identity E2E test in -short mode")
	}

	cases := []struct {
		name    string
		ldflags string
		version string
	}{
		{name: "development build", version: "dev"},
		{
			name:    "release build",
			ldflags: "-X github.com/blak0p/relay-mcp/internal/server/description.ServerVersion=1.2.3",
			version: "1.2.3",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			probe := newE2EProbeWithLDFlags(t, tc.ldflags)
			response := probe.send(t, 1, "initialize", map[string]any{
				"protocolVersion": "2025-11-25",
				"capabilities":    map[string]any{},
				"clientInfo":      map[string]any{"name": "e2e-test", "version": "0.0.1"},
			})
			if response.Error != nil {
				t.Fatalf("initialize returned error: %+v", response.Error)
			}

			var initialized struct {
				ServerInfo struct {
					Name    string `json:"name"`
					Version string `json:"version"`
				} `json:"serverInfo"`
			}
			if err := json.Unmarshal(response.Result, &initialized); err != nil {
				t.Fatalf("unmarshal initialize response: %v", err)
			}
			if initialized.ServerInfo.Name != "relay" {
				t.Fatalf("serverInfo.name = %q, want relay", initialized.ServerInfo.Name)
			}
			if initialized.ServerInfo.Version != tc.version {
				t.Fatalf("serverInfo.version = %q, want %q", initialized.ServerInfo.Version, tc.version)
			}
		})
	}
}

// findModuleRoot walks up from the current working directory until it finds a
// go.mod file. This lets the E2E test build the relay-mcp binary regardless of
// where `go test` was invoked from.
func findModuleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("could not find go.mod walking up from %s", dir)
	return ""
}

// send writes a JSON-RPC request line and reads the next response line.
func (p *e2eProbe) send(t *testing.T, id int, method string, params any) *jsonrpcMessage {
	t.Helper()
	p.sendRequest(t, id, method, params)
	line, err := p.readLine(t)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return unmarshalJSONRPCMessage(t, line)
}

func (p *e2eProbe) sendRequest(t *testing.T, id int, method string, params any) {
	t.Helper()
	body := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
	}
	if params != nil {
		body["params"] = params
	}
	raw, _ := json.Marshal(body)
	if _, err := fmt.Fprintln(p.stdin, string(raw)); err != nil {
		t.Fatalf("write request: %v", err)
	}
}

func unmarshalJSONRPCMessage(t *testing.T, line string) *jsonrpcMessage {
	t.Helper()
	var msg jsonrpcMessage
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		t.Fatalf("unmarshal response %q: %v", line, err)
	}
	return &msg
}

// readLine reads one newline-terminated JSON object from stdout with a timeout.
func (p *e2eProbe) readLine(t *testing.T) (string, error) {
	t.Helper()
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := p.out.ReadString('\n')
		ch <- result{strings.TrimSpace(line), err}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	select {
	case r := <-ch:
		if r.err != nil && r.err != io.EOF {
			return "", r.err
		}
		return r.line, nil
	case <-ctx.Done():
		return "", fmt.Errorf("timeout waiting for server response")
	}
}

func TestE2E_CreateTerminal_RoundTrip(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not in PATH; skipping E2E test")
	}
	probe := newE2EProbe(t)

	// 1. initialize — the server must respond with its serverInfo.
	initResp := probe.send(t, 1, "initialize", map[string]any{
		"protocolVersion": "2025-11-25",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "e2e-test", "version": "0.0.1"},
	})
	if initResp.Error != nil {
		t.Fatalf("initialize returned error: %+v", initResp.Error)
	}
	if initResp.Result == nil {
		t.Fatal("initialize returned no result")
	}

	// 2. tools/call create_terminal — must return a running session with a
	//    term_ + 16 hex id.
	first := callCreateTerminal(t, probe, 2)
	if !sessionIDFormat.MatchString(first.ID) {
		t.Fatalf("first create_terminal id = %q, want match %s", first.ID, sessionIDFormat.String())
	}
	if first.State != "running" {
		t.Fatalf("first create_terminal state = %q, want %q", first.State, "running")
	}
	if first.StartedAt == "" {
		t.Fatal("first create_terminal started_at is empty")
	}

	// 3. tools/call create_terminal again — must fail with -32001 and the
	//    existing session id in data.existing_id.
	second := probe.send(t, 3, "tools/call", map[string]any{
		"name":      "create_terminal",
		"arguments": map[string]any{},
	})
	if second.Error == nil {
		// mcp-go surfaces tool errors as CallToolResult{IsError:true} inside
		// the result, not as a JSON-RPC error. Parse the result and inspect
		// the text content for the error envelope.
		if second.Result == nil {
			t.Fatal("second create_terminal returned neither error nor result")
		}
		errEnv := parseToolErrorFromResult(t, second.Result)
		if errEnv.Code != -32001 {
			t.Fatalf("second create_terminal code = %d, want -32001", errEnv.Code)
		}
		var data struct {
			ExistingID string `json:"existing_id"`
		}
		if err := json.Unmarshal(errEnv.Data, &data); err != nil {
			t.Fatalf("unmarshal error data: %v", err)
		}
		if data.ExistingID != first.ID {
			t.Fatalf("second create_terminal existing_id = %q, want %q", data.ExistingID, first.ID)
		}
	} else {
		// Some versions of mcp-go may propagate tool errors as JSON-RPC errors.
		if second.Error.Code != -32001 {
			t.Fatalf("second create_terminal JSON-RPC code = %d, want -32001", second.Error.Code)
		}
		if !strings.Contains(second.Error.Message, first.ID) {
			t.Fatalf("second create_terminal error message = %q, want it to contain %q", second.Error.Message, first.ID)
		}
	}
}

func TestE2E_CloseTerminal_ReleasesSlotForNextCreate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping process lifecycle E2E test in -short mode")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not in PATH; skipping E2E test")
	}
	probe := newE2EProbe(t)
	initResp := probe.send(t, 1, "initialize", map[string]any{
		"protocolVersion": "2025-11-25",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "e2e-test", "version": "0.0.1"},
	})
	if initResp.Error != nil {
		t.Fatalf("initialize returned error: %+v", initResp.Error)
	}

	first := callCreateTerminal(t, probe, 2)
	closed := callCloseTerminal(t, probe, 3, first.ID)
	if !closed.Closed || closed.Status != "error" || closed.ExitCode != -1 {
		t.Fatalf("close_terminal = %#v, want closed error session with exit_code -1", closed)
	}
	second := callCreateTerminal(t, probe, 4)
	if second.ID == first.ID || second.State != "running" {
		t.Fatalf("second create = %#v, want a new running session after close", second)
	}
}

// createTerminalResult is the success payload of create_terminal.
type createTerminalResult struct {
	ID        string `json:"id"`
	State     string `json:"state"`
	StartedAt string `json:"started_at"`
}

func callCreateTerminal(t *testing.T, probe *e2eProbe, id int) createTerminalResult {
	t.Helper()
	resp := probe.send(t, id, "tools/call", map[string]any{
		"name":      "create_terminal",
		"arguments": map[string]any{},
	})
	if resp.Error != nil {
		t.Fatalf("create_terminal (id=%d) returned JSON-RPC error: %+v", id, resp.Error)
	}
	if resp.Result == nil {
		t.Fatalf("create_terminal (id=%d) returned no result", id)
	}
	return parseToolResultFromResult(t, resp.Result)
}

type closeTerminalResult struct {
	Closed   bool   `json:"closed"`
	Status   string `json:"status"`
	ExitCode int    `json:"exit_code"`
}

func callCloseTerminal(t *testing.T, probe *e2eProbe, id int, sessionID string) closeTerminalResult {
	t.Helper()
	resp := probe.send(t, id, "tools/call", map[string]any{
		"name":      "close_terminal",
		"arguments": map[string]any{"session_id": sessionID},
	})
	if resp.Error != nil {
		t.Fatalf("close_terminal (id=%d) returned JSON-RPC error: %+v", id, resp.Error)
	}
	if resp.Result == nil {
		t.Fatalf("close_terminal (id=%d) returned no result", id)
	}
	var wrapper struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(resp.Result, &wrapper); err != nil {
		t.Fatalf("unmarshal close_terminal CallToolResult: %v", err)
	}
	if wrapper.IsError || len(wrapper.Content) != 1 || wrapper.Content[0].Type != "text" {
		t.Fatalf("unexpected close_terminal result: %s", resp.Result)
	}
	var out closeTerminalResult
	if err := json.Unmarshal([]byte(wrapper.Content[0].Text), &out); err != nil {
		t.Fatalf("unmarshal close_terminal payload %q: %v", wrapper.Content[0].Text, err)
	}
	return out
}

// parseToolResultFromResult extracts the JSON success payload from a
// CallToolResult's text content field.
func parseToolResultFromResult(t *testing.T, raw json.RawMessage) createTerminalResult {
	t.Helper()
	var wrapper struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		t.Fatalf("unmarshal CallToolResult: %v (raw=%s)", err, raw)
	}
	if wrapper.IsError {
		t.Fatalf("CallToolResult.IsError = true; content=%v", wrapper.Content)
	}
	if len(wrapper.Content) == 0 || wrapper.Content[0].Type != "text" {
		t.Fatalf("unexpected content shape: %+v", wrapper.Content)
	}
	var out createTerminalResult
	if err := json.Unmarshal([]byte(wrapper.Content[0].Text), &out); err != nil {
		t.Fatalf("unmarshal create_terminal payload %q: %v", wrapper.Content[0].Text, err)
	}
	return out
}

// parseToolErrorFromResult extracts the {code, message, data} error envelope
// from an errored CallToolResult's text content.
func parseToolErrorFromResult(t *testing.T, raw json.RawMessage) jsonrpcError {
	t.Helper()
	var wrapper struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		t.Fatalf("unmarshal CallToolResult: %v (raw=%s)", err, raw)
	}
	if !wrapper.IsError {
		t.Fatalf("CallToolResult.IsError = false, want true; content=%v", wrapper.Content)
	}
	if len(wrapper.Content) == 0 || wrapper.Content[0].Type != "text" {
		t.Fatalf("unexpected content shape: %+v", wrapper.Content)
	}
	var env jsonrpcError
	if err := json.Unmarshal([]byte(wrapper.Content[0].Text), &env); err != nil {
		t.Fatalf("unmarshal error envelope %q: %v", wrapper.Content[0].Text, err)
	}
	return env
}

// writeTerminalResult is the success payload of write_terminal.
type writeTerminalResult struct {
	BytesWritten int    `json:"bytes_written"`
	State        string `json:"state"`
}

// callWriteTerminal invokes the write_terminal tool with the given data and
// returns the parsed success payload.
func callWriteTerminal(t *testing.T, probe *e2eProbe, id int, data string) writeTerminalResult {
	t.Helper()
	resp := probe.send(t, id, "tools/call", map[string]any{
		"name":      "write_terminal",
		"arguments": map[string]any{"data": data, "ensure_newline": false},
	})
	if resp.Error != nil {
		t.Fatalf("write_terminal (id=%d) returned JSON-RPC error: %+v", id, resp.Error)
	}
	if resp.Result == nil {
		t.Fatalf("write_terminal (id=%d) returned no result", id)
	}
	return parseWriteToolResultFromResult(t, resp.Result)
}

// parseWriteToolResultFromResult extracts the {bytes_written, state} payload
// from a successful write_terminal CallToolResult.
func parseWriteToolResultFromResult(t *testing.T, raw json.RawMessage) writeTerminalResult {
	t.Helper()
	var wrapper struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		t.Fatalf("unmarshal CallToolResult: %v (raw=%s)", err, raw)
	}
	if wrapper.IsError {
		t.Fatalf("write_terminal CallToolResult.IsError = true; content=%v", wrapper.Content)
	}
	if len(wrapper.Content) == 0 || wrapper.Content[0].Type != "text" {
		t.Fatalf("unexpected content shape: %+v", wrapper.Content)
	}
	var out writeTerminalResult
	if err := json.Unmarshal([]byte(wrapper.Content[0].Text), &out); err != nil {
		t.Fatalf("unmarshal write_terminal payload %q: %v", wrapper.Content[0].Text, err)
	}
	return out
}

// TestE2E_WriteTerminal_RoundTrip proves the full MCP path: initialize →
// create_terminal → write_terminal. We assert the write_terminal response
// carries bytes_written == len(data) and state == "running". Actual byte
// delivery to the PTY is proven at the unit level (T-WT-04); at the e2e level
// the response shape is the contract the client branches on. REQ-WT-001,
// REQ-WT-008, REQ-WT-009.
func TestE2E_WriteTerminal_RoundTrip(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not in PATH; skipping E2E test")
	}
	probe := newE2EProbe(t)

	// 1. initialize.
	initResp := probe.send(t, 1, "initialize", map[string]any{
		"protocolVersion": "2025-11-25",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "e2e-test", "version": "0.0.1"},
	})
	if initResp.Error != nil {
		t.Fatalf("initialize returned error: %+v", initResp.Error)
	}

	// 2. create_terminal → running session.
	created := callCreateTerminal(t, probe, 2)
	if created.State != "running" {
		t.Fatalf("create_terminal state = %q, want running", created.State)
	}

	// 3. write_terminal with "echo HELLO_FROM_WRITE\n" — must return
	//    bytes_written == len(payload) and state == "running".
	payload := "echo HELLO_FROM_WRITE\n"
	written := callWriteTerminal(t, probe, 3, payload)
	if written.State != "running" {
		t.Fatalf("write_terminal state = %q, want running", written.State)
	}
	if written.BytesWritten != len(payload) {
		t.Fatalf("write_terminal bytes_written = %d, want %d (len(payload))", written.BytesWritten, len(payload))
	}
	if written.BytesWritten <= 0 {
		t.Fatalf("write_terminal bytes_written = %d, want > 0", written.BytesWritten)
	}

	// 4. Second write_terminal with a smaller payload proves the session stays
	//    usable across calls (no one-shot side effect).
	again := callWriteTerminal(t, probe, 4, "ls\n")
	if again.State != "running" {
		t.Fatalf("second write_terminal state = %q, want running", again.State)
	}
	if again.BytesWritten != len("ls\n") {
		t.Fatalf("second write_terminal bytes_written = %d, want %d", again.BytesWritten, len("ls\n"))
	}
}

func TestE2E_WriteTerminal_RequiresExplicitNewlinePolicy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping write_terminal E2E test in -short mode")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not in PATH; skipping E2E test")
	}
	probe := newE2EProbe(t)
	initializeE2EProbe(t, probe)
	callCreateTerminal(t, probe, 2)

	invalidMarker := "WRITE_TERMINAL_INVALID_MARKER"
	for _, tc := range []struct {
		name      string
		arguments map[string]any
	}{
		{name: "missing policy", arguments: map[string]any{"data": "printf '" + invalidMarker + "\\n'"}},
		{name: "non boolean policy", arguments: map[string]any{"data": "printf '" + invalidMarker + "\\n'", "ensure_newline": "true"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := probe.send(t, 3, "tools/call", map[string]any{"name": "write_terminal", "arguments": tc.arguments})
			if response.Error != nil || response.Result == nil {
				t.Fatalf("write_terminal response = %+v, want tool error result", response)
			}
			if got := parseToolErrorFromResult(t, response.Result).Code; got != -32602 {
				t.Fatalf("write_terminal error code = %d, want -32602", got)
			}
		})
	}

	truePayload := "printf 'WRITE_TERMINAL_TRUE_MARKER\\n'"
	if got := callWriteTerminalWithPolicy(t, probe, 4, truePayload, true); got.BytesWritten != len(truePayload)+1 {
		t.Fatalf("true bytes_written = %d, want %d", got.BytesWritten, len(truePayload)+1)
	}
	falsePayload := "printf 'WRITE_TERMINAL_FALSE_MARKER\\n'\n"
	if got := callWriteTerminalWithPolicy(t, probe, 5, falsePayload, false); got.BytesWritten != len(falsePayload) {
		t.Fatalf("false bytes_written = %d, want %d", got.BytesWritten, len(falsePayload))
	}

	first := readTerminalSnapshotAtCursor(t, probe, 6, 0)
	second := readTerminalSnapshotAtCursor(t, probe, 7, first.NextCursor)
	output := first.Output + second.Output
	if strings.Contains(output, invalidMarker) {
		t.Fatalf("terminal output contains marker from rejected request: %q", output)
	}
	for _, marker := range []string{"WRITE_TERMINAL_TRUE_MARKER", "WRITE_TERMINAL_FALSE_MARKER"} {
		if !strings.Contains(output, marker) {
			t.Fatalf("terminal output = %q, want marker %q", output, marker)
		}
	}
}

func initializeE2EProbe(t *testing.T, probe *e2eProbe) {
	t.Helper()
	response := probe.send(t, 1, "initialize", map[string]any{
		"protocolVersion": "2025-11-25",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "e2e-test", "version": "0.0.1"},
	})
	if response.Error != nil {
		t.Fatalf("initialize returned error: %+v", response.Error)
	}
}

func callWriteTerminalWithPolicy(t *testing.T, probe *e2eProbe, id int, data string, ensureNewline bool) writeTerminalResult {
	t.Helper()
	response := probe.send(t, id, "tools/call", map[string]any{
		"name":      "write_terminal",
		"arguments": map[string]any{"data": data, "ensure_newline": ensureNewline},
	})
	if response.Error != nil || response.Result == nil {
		t.Fatalf("write_terminal response = %+v, want successful tool result", response)
	}
	return parseWriteToolResultFromResult(t, response.Result)
}

func readTerminalSnapshot(t *testing.T, probe *e2eProbe, id int) string {
	return readTerminalSnapshotAtCursor(t, probe, id, 0).Output
}

type terminalSnapshot struct {
	Output     string `json:"output"`
	NextCursor int64  `json:"next_cursor"`
}

func readTerminalSnapshotAtCursor(t *testing.T, probe *e2eProbe, id int, cursor int64) terminalSnapshot {
	t.Helper()
	response := probe.send(t, id, "tools/call", map[string]any{
		"name":      "read_terminal",
		"arguments": map[string]any{"mode": "snapshot", "cursor": cursor, "wait_ms": 1000},
	})
	if response.Error != nil || response.Result == nil {
		t.Fatalf("read_terminal response = %+v, want successful tool result", response)
	}
	var wrapper struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(response.Result, &wrapper); err != nil || wrapper.IsError || len(wrapper.Content) != 1 {
		t.Fatalf("read_terminal result = %s, err = %v", response.Result, err)
	}
	var result terminalSnapshot
	if err := json.Unmarshal([]byte(wrapper.Content[0].Text), &result); err != nil {
		t.Fatalf("unmarshal read_terminal payload: %v", err)
	}
	return result
}

type sendControlResult struct {
	Key       string `json:"key"`
	BytesSent int    `json:"bytes_sent"`
}

func callSendControl(t *testing.T, probe *e2eProbe, id int, key string) sendControlResult {
	t.Helper()
	resp := probe.send(t, id, "tools/call", map[string]any{
		"name":      "send_control",
		"arguments": map[string]any{"key": key},
	})
	if resp.Error != nil {
		t.Fatalf("send_control (id=%d) returned JSON-RPC error: %+v", id, resp.Error)
	}
	if resp.Result == nil {
		t.Fatalf("send_control (id=%d) returned no result", id)
	}
	var wrapper struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(resp.Result, &wrapper); err != nil {
		t.Fatalf("unmarshal send_control CallToolResult: %v", err)
	}
	if wrapper.IsError || len(wrapper.Content) != 1 || wrapper.Content[0].Type != "text" {
		t.Fatalf("unexpected send_control result: %s", resp.Result)
	}
	var out sendControlResult
	if err := json.Unmarshal([]byte(wrapper.Content[0].Text), &out); err != nil {
		t.Fatalf("unmarshal send_control payload %q: %v", wrapper.Content[0].Text, err)
	}
	return out
}

func TestE2E_SendControl_RoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping process lifecycle E2E test in -short mode")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not in PATH; skipping E2E test")
	}
	probe := newE2EProbe(t)
	initResp := probe.send(t, 1, "initialize", map[string]any{
		"protocolVersion": "2025-11-25",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "e2e-test", "version": "0.0.1"},
	})
	if initResp.Error != nil {
		t.Fatalf("initialize returned error: %+v", initResp.Error)
	}

	created := callCreateTerminal(t, probe, 2)
	got := callSendControl(t, probe, 3, "UP")
	if got.Key != "up" || got.BytesSent != 3 {
		t.Fatalf("send_control result = %#v, want {key:up bytes_sent:3}", got)
	}
	closed := callCloseTerminal(t, probe, 4, created.ID)
	if !closed.Closed {
		t.Fatalf("close_terminal = %#v, want closed true", closed)
	}
}

func TestE2E_SendControl_NoActiveSessionReturnsEstablishedEnvelope(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stdio E2E test in -short mode")
	}
	probe := newE2EProbe(t)
	initResp := probe.send(t, 1, "initialize", map[string]any{
		"protocolVersion": "2025-11-25",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "e2e-test", "version": "0.0.1"},
	})
	if initResp.Error != nil {
		t.Fatalf("initialize returned error: %+v", initResp.Error)
	}

	resp := probe.send(t, 2, "tools/call", map[string]any{
		"name":      "send_control",
		"arguments": map[string]any{"key": "enter"},
	})
	if resp.Error != nil {
		t.Fatalf("send_control returned JSON-RPC error: %+v", resp.Error)
	}
	if resp.Result == nil {
		t.Fatal("send_control returned no result")
	}

	errEnv := parseToolErrorFromResult(t, resp.Result)
	if errEnv.Code != -32004 {
		t.Fatalf("send_control no-active-session code = %d, want -32004", errEnv.Code)
	}
	if errEnv.Message != "no active session; call create_terminal first" {
		t.Fatalf("send_control no-active-session message = %q, want established envelope", errEnv.Message)
	}
	if len(errEnv.Data) != 0 {
		t.Fatalf("send_control no-active-session data = %s, want omitted", errEnv.Data)
	}
}

func TestE2E_ReadTerminal_StreamsProgressBeforeFinalResponse(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not in PATH; skipping E2E test")
	}
	probe := newE2EProbe(t)

	initResp := probe.send(t, 1, "initialize", map[string]any{
		"protocolVersion": "2025-11-25",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "e2e-test", "version": "0.0.1"},
	})
	if initResp.Error != nil {
		t.Fatalf("initialize returned error: %+v", initResp.Error)
	}
	created := callCreateTerminal(t, probe, 2)
	if created.State != "running" {
		t.Fatalf("create_terminal state = %q, want running", created.State)
	}

	ready := probe.send(t, 3, "tools/call", map[string]any{
		"name":      "read_terminal",
		"arguments": map[string]any{"mode": "snapshot", "wait_ms": 1000},
	})
	if ready.Error != nil {
		t.Fatalf("initial read_terminal snapshot returned JSON-RPC error: %+v", ready.Error)
	}
	if ready.Result == nil {
		t.Fatal("initial read_terminal snapshot returned no result")
	}

	marker := "READ_TERMINAL_E2E"
	releasedMarker := "READ_TERMINAL_RELEASED"
	callWriteTerminal(t, probe, 4, "printf '"+marker+"\\n'; read -r _; printf '"+releasedMarker+"\\n'\n")
	probe.sendRequest(t, 5, "tools/call", map[string]any{
		"name":      "read_terminal",
		"arguments": map[string]any{},
		"_meta":     map[string]any{"progressToken": "read-terminal-e2e"},
	})

	sawProgress := false
	sawMarker := false
	for !sawMarker {
		line, err := probe.readLine(t)
		if err != nil {
			t.Fatalf("read read_terminal response: %v", err)
		}
		message := unmarshalJSONRPCMessage(t, line)
		if message.Method == "notifications/progress" {
			var progress struct {
				ProgressToken string `json:"progressToken"`
				Output        string `json:"output"`
				Cursor        int64  `json:"cursor"`
				NextCursor    int64  `json:"next_cursor"`
			}
			if err := json.Unmarshal(message.Params, &progress); err != nil {
				t.Fatalf("unmarshal progress params: %v", err)
			}
			if progress.ProgressToken != "read-terminal-e2e" {
				t.Fatalf("progress token = %q, want read-terminal-e2e", progress.ProgressToken)
			}
			if progress.NextCursor <= progress.Cursor {
				t.Fatalf("progress cursor range = [%d,%d), want advancing", progress.Cursor, progress.NextCursor)
			}
			sawProgress = true
			sawMarker = sawMarker || strings.Contains(progress.Output, marker)
			continue
		}
		if string(message.ID) == "5" {
			t.Fatal("read_terminal final response arrived before a progress notification containing the marker")
		}
		t.Fatalf("unexpected message before read_terminal progress: %+v", message)
	}
	if !sawProgress {
		t.Fatal("read_terminal progress was not observed before releasing the terminal")
	}

	// Release read first, keeping bash alive so its write response cannot race
	// with stream completion. The subsequent exit ends the stream.
	released := probe.send(t, 6, "tools/call", map[string]any{
		"name":      "write_terminal",
		"arguments": map[string]any{"data": "\n", "ensure_newline": false},
	})
	if released.Error != nil {
		t.Fatalf("release write_terminal returned JSON-RPC error: %+v", released.Error)
	}
	if released.Result == nil {
		t.Fatal("release write_terminal returned no result")
	}
	releaseResult := parseWriteToolResultFromResult(t, released.Result)
	if releaseResult.BytesWritten != len("\n") {
		t.Fatalf("release write_terminal bytes_written = %d, want %d", releaseResult.BytesWritten, len("\n"))
	}

	// Wait until bash has consumed the read input and emitted the follow-up
	// marker before sending exit. Otherwise the PTY's buffered input may let
	// read -r consume the exit line as well.
	sawReleasedMarker := false
	for !sawReleasedMarker {
		line, err := probe.readLine(t)
		if err != nil {
			t.Fatalf("read release progress: %v", err)
		}
		message := unmarshalJSONRPCMessage(t, line)
		if message.Method == "notifications/progress" {
			var progress struct {
				Output string `json:"output"`
			}
			if err := json.Unmarshal(message.Params, &progress); err != nil {
				t.Fatalf("unmarshal release progress params: %v", err)
			}
			sawReleasedMarker = strings.Contains(progress.Output, releasedMarker)
			continue
		}
		t.Fatalf("unexpected message before release progress: %+v", message)
	}

	probe.sendRequest(t, 7, "tools/call", map[string]any{
		"name":      "write_terminal",
		"arguments": map[string]any{"data": "exit\n", "ensure_newline": false},
	})

	sawExitResponse := false
	sawFinalResponse := false
	for !sawExitResponse || !sawFinalResponse {
		line, err := probe.readLine(t)
		if err != nil {
			t.Fatalf("read terminal completion response: %v", err)
		}
		message := unmarshalJSONRPCMessage(t, line)
		if message.Method == "notifications/progress" {
			continue
		}
		switch string(message.ID) {
		case "7":
			if message.Error != nil {
				t.Fatalf("exit write_terminal returned JSON-RPC error: %+v", message.Error)
			}
			if message.Result == nil {
				t.Fatal("exit write_terminal returned no result")
			}
			exited := parseWriteToolResultFromResult(t, message.Result)
			if exited.BytesWritten != len("exit\n") {
				t.Fatalf("exit write_terminal bytes_written = %d, want %d", exited.BytesWritten, len("exit\n"))
			}
			sawExitResponse = true
		case "5":
			if message.Error != nil {
				t.Fatalf("read_terminal returned JSON-RPC error: %+v", message.Error)
			}
			if message.Result == nil {
				t.Fatal("read_terminal returned no final result")
			}
			var final struct {
				IsError bool `json:"isError"`
			}
			if err := json.Unmarshal(message.Result, &final); err != nil {
				t.Fatalf("unmarshal read_terminal final result: %v", err)
			}
			if final.IsError {
				t.Fatalf("read_terminal final result is an error: %s", message.Result)
			}
			sawFinalResponse = true
		default:
			t.Fatalf("unexpected message before read_terminal response: %+v", message)
		}
	}
}
