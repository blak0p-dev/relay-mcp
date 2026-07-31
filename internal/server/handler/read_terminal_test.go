package handler

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/blak0p/relay-mcp/internal/session/output"
	"github.com/blak0p/relay-mcp/internal/session/registry"
	"github.com/blak0p/relay-mcp/internal/session/session"
)

func TestReadTerminalSnapshotAndDrain(t *testing.T) {
	for _, tt := range []struct {
		name string
		mode string
		args map[string]any
		want ReadTerminalResult
	}{
		{"snapshot is repeatable", "snapshot", map[string]any{"max_bytes": 3, "wait_ms": 0}, ReadTerminalResult{Output: "abc", Cursor: 0, NextCursor: 3, Status: "running"}},
		{"drain advances caller cursor", "drain", map[string]any{"cursor": 3, "max_bytes": 2, "wait_ms": 0}, ReadTerminalResult{Output: "de", Cursor: 3, NextCursor: 5, Status: "running"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			reg := seededReadRegistry(t, "abcdef")
			tt.args["mode"] = tt.mode
			res, err := NewReadTerminalHandler(reg)(context.Background(), readRequest(tt.args, nil))
			if err != nil {
				t.Fatalf("handler returned Go error: %v", err)
			}
			got := extractReadResult(t, res)
			if got != tt.want {
				t.Fatalf("result = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestReadTerminalDrainRawArgumentsCapsOutput(t *testing.T) {
	res, err := NewReadTerminalHandler(seededReadRegistry(t, strings.Repeat("x", 4097)))(
		context.Background(),
		mcp.CallToolRequest{Params: mcp.CallToolParams{
			Name:         "read_terminal",
			RawArguments: json.RawMessage(`{"mode":"drain","max_bytes":4096,"wait_ms":1000}`),
		}},
	)
	if err != nil {
		t.Fatalf("handler returned Go error: %v", err)
	}
	got := extractReadResult(t, res)
	if got.Status != "running" {
		t.Fatalf("status = %q, want running", got.Status)
	}
	if len(got.Output) != 4096 {
		t.Fatalf("output length = %d, want 4096", len(got.Output))
	}
	if got.NextCursor != 4096 {
		t.Fatalf("next cursor = %d, want 4096", got.NextCursor)
	}
}

func TestReadTerminalSnapshotWithoutWaitUsesDefaultTimeout(t *testing.T) {
	reg := seededReadRegistry(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	started := time.Now()
	res, err := NewReadTerminalHandler(reg)(ctx, readRequest(map[string]any{"mode": "snapshot"}, nil))
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("handler returned Go error: %v", err)
	}
	if got, want := extractReadResult(t, res), (ReadTerminalResult{Status: "running"}); got != want {
		t.Fatalf("result = %+v, want %+v", got, want)
	}
	if elapsed < output.DefaultWait {
		t.Fatalf("elapsed = %s, want at least default wait %s", elapsed, output.DefaultWait)
	}
}

func TestReadTerminalSnapshotAcceptsMaximumWait(t *testing.T) {
	res, err := NewReadTerminalHandler(seededReadRegistry(t, "ready"))(context.Background(), readRequest(map[string]any{
		"mode":    "snapshot",
		"wait_ms": 5000,
	}, nil))
	if err != nil {
		t.Fatalf("handler returned Go error: %v", err)
	}
	if got, want := extractReadResult(t, res).Output, "ready"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestReadTerminalStableErrors(t *testing.T) {
	for _, tt := range []struct {
		name   string
		reg    func(t *testing.T) *registry.Registry
		args   map[string]any
		meta   *mcp.Meta
		reason string
	}{
		{"missing session", func(t *testing.T) *registry.Registry { return registry.NewRegistry() }, map[string]any{"mode": "snapshot", "wait_ms": 0}, nil, "session_not_found"},
		{"stream needs token", func(t *testing.T) *registry.Registry { return seededReadRegistry(t, "") }, map[string]any{}, nil, "stream_requires_progress_token"},
		{"invalid read request", func(t *testing.T) *registry.Registry { return seededReadRegistry(t, "") }, map[string]any{"mode": "snapshot", "max_bytes": 0}, nil, "invalid_read_request"},
		{"maximum bytes is bounded", func(t *testing.T) *registry.Registry { return seededReadRegistry(t, "") }, map[string]any{"mode": "snapshot", "max_bytes": output.MaxReadBytes + 1}, nil, "invalid_read_request"},
		{"wait is bounded", func(t *testing.T) *registry.Registry { return seededReadRegistry(t, "") }, map[string]any{"mode": "snapshot", "wait_ms": 5001}, nil, "invalid_read_request"},
		{"closed session", closedReadRegistry, map[string]any{"mode": "snapshot"}, nil, "session_closed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			res, err := NewReadTerminalHandler(tt.reg(t))(context.Background(), readRequest(tt.args, tt.meta))
			if err != nil {
				t.Fatalf("handler returned Go error: %v", err)
			}
			if got := extractError(t, res).Data["reason"]; got != tt.reason {
				t.Fatalf("error reason = %q, want %q", got, tt.reason)
			}
		})
	}
}

func TestReadTerminalStreamOrderedCursors(t *testing.T) {
	reg := seededReadRegistry(t, "first")
	notifications := make(chan map[string]any, 2)
	h := NewReadTerminalHandler(reg, WithProgressSender(func(_ context.Context, _ string, params map[string]any) error {
		notifications <- params
		return nil
	}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan *mcp.CallToolResult, 1)
	go func() { res, _ := h(ctx, readRequest(nil, &mcp.Meta{ProgressToken: "progress-1"})); done <- res }()

	first := <-notifications
	if first["output"] != "first" || first["cursor"] != int64(0) || first["next_cursor"] != int64(5) || first["progressToken"] != "progress-1" {
		t.Fatalf("first notification = %#v", first)
	}
	cancel()
	if res := <-done; res.IsError {
		t.Fatalf("cancel result = %v, want success", res.Content)
	}
}

func TestReadTerminalStreamCancelStopsNotifications(t *testing.T) {
	reg := seededReadRegistry(t, "")
	notifications := make(chan map[string]any, 1)
	h := NewReadTerminalHandler(reg, WithProgressSender(func(_ context.Context, _ string, params map[string]any) error {
		notifications <- params
		return nil
	}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _, _ = h(ctx, readRequest(nil, &mcp.Meta{ProgressToken: 1})); close(done) }()
	cancel()
	<-done
	regSession(t, reg).Output.Append([]byte("late"))
	select {
	case got := <-notifications:
		t.Fatalf("notification after cancellation = %#v", got)
	case <-time.After(30 * time.Millisecond):
	}
}

func TestReadTerminalStreamBlockedNotificationDoesNotStallCapture(t *testing.T) {
	reg := seededReadRegistry(t, "ready")
	h := NewReadTerminalHandler(reg, WithProgressSender(func(context.Context, string, map[string]any) error {
		return server.ErrNotificationChannelBlocked
	}))
	res, err := h(context.Background(), readRequest(nil, &mcp.Meta{ProgressToken: "blocked"}))
	if err != nil {
		t.Fatalf("handler returned Go error: %v", err)
	}
	if got := extractError(t, res).Data["reason"]; got != "stream_delivery_failed" {
		t.Fatalf("error reason = %q, want stream_delivery_failed", got)
	}
	s := regSession(t, reg)
	s.Output.Append([]byte(" retained"))
	snapshot, err := s.Output.Snapshot(context.Background(), 0, output.MaxReadBytes, 0)
	if err != nil || string(snapshot.Output) != "ready retained" {
		t.Fatalf("captured output = %q, %v; want %q, nil", snapshot.Output, err, "ready retained")
	}
}

func readRequest(args map[string]any, meta *mcp.Meta) mcp.CallToolRequest {
	return mcp.CallToolRequest{Params: mcp.CallToolParams{Name: "read_terminal", Arguments: args, Meta: meta}}
}

func seededReadRegistry(t *testing.T, retained string) *registry.Registry {
	t.Helper()
	reg := registry.NewRegistry()
	s := session.New(nil, nil)
	s.Output.Append([]byte(retained))
	if err := reg.Put(s); err != nil {
		t.Fatal(err)
	}
	return reg
}

func closedReadRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg := seededReadRegistry(t, "")
	if err := regSession(t, reg).Close(); err != nil {
		t.Fatal(err)
	}
	return reg
}

func regSession(t *testing.T, reg *registry.Registry) *session.Session {
	t.Helper()
	s, err := reg.Get()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func extractReadResult(t *testing.T, res *mcp.CallToolResult) ReadTerminalResult {
	t.Helper()
	if res == nil || res.IsError || len(res.Content) != 1 {
		t.Fatalf("unexpected result: %#v", res)
	}
	text, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("content = %T, want text", res.Content[0])
	}
	got, err := parseJSON[ReadTerminalResult](text.Text)
	if err != nil {
		t.Fatal(err)
	}
	return got
}
