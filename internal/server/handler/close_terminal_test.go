package handler

import (
	"context"
	"errors"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	serror "github.com/blak0p/relay-mcp/internal/session/error"
	"github.com/blak0p/relay-mcp/internal/session/registry"
	"github.com/blak0p/relay-mcp/internal/session/session"
)

type closeTerminalResultPayload struct {
	Closed   bool   `json:"closed"`
	Status   string `json:"status"`
	ExitCode int    `json:"exit_code"`
}

func newCloseRequest(sessionID any) mcp.CallToolRequest {
	args := map[string]any{}
	if sessionID != nil {
		args["session_id"] = sessionID
	}
	return mcp.CallToolRequest{Params: mcp.CallToolParams{Name: "close_terminal", Arguments: args}}
}

func extractCloseResult(t *testing.T, res *mcp.CallToolResult) closeTerminalResultPayload {
	t.Helper()
	if res == nil || res.IsError || len(res.Content) != 1 {
		t.Fatalf("close result = %#v, want one successful text result", res)
	}
	text, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("close content = %T, want mcp.TextContent", res.Content[0])
	}
	result, err := parseJSON[closeTerminalResultPayload](text.Text)
	if err != nil {
		t.Fatalf("parse close result %q: %v", text.Text, err)
	}
	return result
}

func TestCloseTerminalHandler_RejectsInvalidSessionID(t *testing.T) {
	for _, tt := range []struct {
		name string
		id   any
	}{
		{name: "missing", id: nil},
		{name: "wrong type", id: 42},
	} {
		t.Run(tt.name, func(t *testing.T) {
			reg := registry.NewRegistry()
			s := seedLiveSession(t, reg)
			h := NewCloseTerminalHandler(reg)

			res, err := h(context.Background(), newCloseRequest(tt.id))
			if err != nil {
				t.Fatalf("handler returned Go error: %v", err)
			}
			if got := extractError(t, res).Code; got != codeInvalidArgument {
				t.Fatalf("error code = %d, want %d", got, codeInvalidArgument)
			}
			got, err := reg.Get()
			if err != nil || got.ID != s.ID {
				t.Fatalf("invalid request mutated registry: session=%v err=%v, want %s", got, err, s.ID)
			}
		})
	}
}

func TestCloseTerminalHandler_MatchingAndIdempotent(t *testing.T) {
	reg := registry.NewRegistry()
	s := seedLiveSession(t, reg)
	h := NewCloseTerminalHandler(reg)

	res, err := h(context.Background(), newCloseRequest(s.ID))
	if err != nil {
		t.Fatalf("matching close returned Go error: %v", err)
	}
	got := extractCloseResult(t, res)
	if !got.Closed || got.Status != string(session.StateExited) || got.ExitCode != 0 {
		t.Fatalf("matching close = %#v, want cleanly closed session with exit code 0", got)
	}

	retry, err := h(context.Background(), newCloseRequest(s.ID))
	if err != nil {
		t.Fatalf("retry close returned Go error: %v", err)
	}
	if text := retry.Content[0].(mcp.TextContent).Text; text != `{"closed":false}` {
		t.Fatalf("retry response = %s, want exactly {\"closed\":false}", text)
	}
}

func TestCloseTerminalHandler_MismatchAndCleanupFailure(t *testing.T) {
	t.Run("mismatch preserves active session", func(t *testing.T) {
		reg := registry.NewRegistry()
		s := seedLiveSession(t, reg)
		h := NewCloseTerminalHandler(reg)

		res, err := h(context.Background(), newCloseRequest("term_other"))
		if err != nil {
			t.Fatalf("mismatched close returned Go error: %v", err)
		}
		if text := res.Content[0].(mcp.TextContent).Text; text != `{"closed":false}` {
			t.Fatalf("mismatch response = %s, want exactly {\"closed\":false}", text)
		}
		got, err := reg.Get()
		if err != nil || got.ID != s.ID {
			t.Fatalf("mismatched close changed registry: session=%v err=%v, want %s", got, err, s.ID)
		}
	})

	t.Run("cleanup failure releases session", func(t *testing.T) {
		reg := registry.NewRegistry()
		s := session.New(nil, nil)
		s.ID = "term_cleanup_failure"
		if err := reg.Put(s); err != nil {
			t.Fatalf("registry.Put: %v", err)
		}
		h := NewCloseTerminalHandler(reg)

		res, err := h(context.Background(), newCloseRequest(s.ID))
		if err != nil {
			t.Fatalf("cleanup failure returned Go error: %v", err)
		}
		got := extractError(t, res)
		if got.Code != codeSessionCleanup || got.Data["session_id"] != s.ID || got.Data["reason"] != "cleanup_failed" {
			t.Fatalf("cleanup error = %#v, want stable cleanup failure for %s", got, s.ID)
		}
		if _, err := reg.Get(); !errors.Is(err, serror.ErrSessionNotFound) {
			t.Fatalf("registry.Get after cleanup failure = %v, want ErrSessionNotFound", err)
		}
	})
}
