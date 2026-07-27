package handler

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/blak0p/relay-mcp/internal/session/registry"
	"github.com/blak0p/relay-mcp/internal/session/session"
)

func TestWriteTerminalHandler_EnsureNewlineValidation(t *testing.T) {
	t.Parallel()
	h := NewWriteTerminalHandler(registry.NewRegistry())
	for _, args := range []map[string]any{
		{"data": "hello"},
		{"data": "hello", "ensure_newline": "true"},
		{"data": "hello", "ensure_newline": 1},
	} {
		res, err := h(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: args}})
		if err != nil {
			t.Fatal(err)
		}
		if got := extractError(t, res).Code; got != codeInvalidArgument {
			t.Fatalf("error code = %d, want %d", got, codeInvalidArgument)
		}
	}
}

func TestWriteTerminalHandler_EnsureNewlinePayloadSize(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		data   string
		ensure bool
		want   int
	}{
		{"adds LF", "hello", true, 6},
		{"preserves LF", "hello\n", true, 6},
		{"turns empty into LF", "", true, 1},
		{"turns CR into CRLF", "hello\r", true, 7},
		{"preserves raw multiline", "one\ntwo", false, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg := registry.NewRegistry()
			seedLiveSession(t, reg)
			res, err := NewWriteTerminalHandler(reg)(context.Background(), mcp.CallToolRequest{
				Params: mcp.CallToolParams{Arguments: map[string]any{"data": tc.data, "ensure_newline": tc.ensure}},
			})
			if err != nil {
				t.Fatal(err)
			}
			text := res.Content[0].(mcp.TextContent).Text
			out, err := parseJSON[writeTerminalResultPayload](text)
			if err != nil {
				t.Fatal(err)
			}
			if res.IsError || out.BytesWritten != tc.want {
				t.Fatalf("bytes_written = %d, want %d; result = %s", out.BytesWritten, tc.want, text)
			}
		})
	}
}

func TestWriteTerminalHandler_EnsureNewlineFinalSize(t *testing.T) {
	t.Parallel()
	reg := registry.NewRegistry()
	seedLiveSession(t, reg)
	res, err := NewWriteTerminalHandler(reg)(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]any{
			"data": strings.Repeat("x", session.MaxWriteBytes), "ensure_newline": true,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := extractError(t, res).Code; got != codeWriteTooLarge {
		t.Fatalf("error code = %d, want %d", got, codeWriteTooLarge)
	}
}
