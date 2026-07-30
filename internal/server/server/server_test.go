package server

import (
	"testing"

	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/blak0p/relay-mcp/internal/server/description"
	"github.com/blak0p/relay-mcp/internal/session/registry"
)

func TestNewServer_RegistersCreateTerminalTool(t *testing.T) {
	t.Parallel()
	reg := registry.NewRegistry()
	s, err := NewServer(reg)
	if err != nil {
		t.Fatalf("NewServer = %v, want nil", err)
	}
	if s == nil {
		t.Fatal("NewServer returned nil server, want non-nil")
	}

	tools := s.ListTools()
	if len(tools) == 0 {
		t.Fatal("server has no tools registered, want create_terminal")
	}
	tool, ok := tools[description.CreateTerminalName]
	if !ok {
		t.Fatalf("create_terminal not registered; registered tools = %v", toolNames(tools))
	}
	if tool.Tool.Name != description.CreateTerminalName {
		t.Fatalf("registered tool name = %q, want %q", tool.Tool.Name, description.CreateTerminalName)
	}
	if tool.Tool.Description != description.CreateTerminalDescription {
		t.Fatalf("registered tool description = %q, want %q", tool.Tool.Description, description.CreateTerminalDescription)
	}
	if tool.Handler == nil {
		t.Fatal("registered tool handler is nil")
	}
}

func TestNewServer_NilRegistryReturnsError(t *testing.T) {
	t.Parallel()
	_, err := NewServer(nil)
	if err == nil {
		t.Fatal("NewServer(nil) returned nil error, want non-nil")
	}
}

// TestNewServer_RegistersWriteTerminalTool proves the write_terminal tool is
// registered alongside create_terminal with the correct name, description,
// and a non-nil handler. REQ-WT-008.
func TestNewServer_RegistersWriteTerminalTool(t *testing.T) {
	t.Parallel()
	reg := registry.NewRegistry()
	s, err := NewServer(reg)
	if err != nil {
		t.Fatalf("NewServer = %v, want nil", err)
	}

	tools := s.ListTools()

	// create_terminal must still be present.
	if _, ok := tools[description.CreateTerminalName]; !ok {
		t.Fatalf("create_terminal not registered; registered tools = %v", toolNames(tools))
	}

	// write_terminal must be present with the right name, description, and handler.
	wt, ok := tools[description.WriteTerminalName]
	if !ok {
		t.Fatalf("write_terminal not registered; registered tools = %v", toolNames(tools))
	}
	if wt.Tool.Name != description.WriteTerminalName {
		t.Fatalf("write_terminal tool name = %q, want %q", wt.Tool.Name, description.WriteTerminalName)
	}
	if wt.Tool.Description != description.WriteTerminalDescription {
		t.Fatalf("write_terminal description = %q, want %q", wt.Tool.Description, description.WriteTerminalDescription)
	}
	if wt.Handler == nil {
		t.Fatal("write_terminal handler is nil")
	}

	// The input schema must declare exactly two required properties: "data" (string)
	// and "ensure_newline" (boolean), with no default. This is the breaking
	// contract change — callers MUST now provide ensure_newline on every call.
	props := wt.Tool.InputSchema.Properties
	if len(props) != 2 {
		t.Fatalf("write_terminal input schema has %d properties, want 2 (data + ensure_newline)", len(props))
	}

	// Verify "data": string, required
	rawData, ok := props["data"]
	if !ok {
		t.Fatalf("write_terminal input schema has no 'data' property; got %v", props)
	}
	dataPropMap, ok := rawData.(map[string]any)
	if !ok {
		t.Fatalf("write_terminal 'data' property is %T, want map[string]any", rawData)
	}
	dataPropType, _ := dataPropMap["type"].(string)
	if dataPropType != "string" {
		t.Fatalf("write_terminal 'data' property type = %q, want \"string\"", dataPropType)
	}
	if !containsString(wt.Tool.InputSchema.Required, "data") {
		t.Fatalf("write_terminal 'data' is not required; required = %v", wt.Tool.InputSchema.Required)
	}

	// Verify "ensure_newline": boolean, required, no default
	rawEnsure, ok := props["ensure_newline"]
	if !ok {
		t.Fatalf("write_terminal input schema has no 'ensure_newline' property; got %v", props)
	}
	ensurePropMap, ok := rawEnsure.(map[string]any)
	if !ok {
		t.Fatalf("write_terminal 'ensure_newline' property is %T, want map[string]any", rawEnsure)
	}
	ensurePropType, _ := ensurePropMap["type"].(string)
	if ensurePropType != "boolean" {
		t.Fatalf("write_terminal 'ensure_newline' property type = %q, want \"boolean\"", ensurePropType)
	}
	// "ensure_newline" must be required
	if !containsString(wt.Tool.InputSchema.Required, "ensure_newline") {
		t.Fatalf("write_terminal 'ensure_newline' is not required; required = %v", wt.Tool.InputSchema.Required)
	}
	// "ensure_newline" must NOT have a default — this is the breaking change.
	if _, hasDefault := ensurePropMap["default"]; hasDefault {
		t.Fatalf("write_terminal 'ensure_newline' must not have a default; got %v", ensurePropMap["default"])
	}
}

func TestNewServer_RegistersReadTerminalTool(t *testing.T) {
	t.Parallel()
	reg := registry.NewRegistry()
	s, err := NewServer(reg)
	if err != nil {
		t.Fatalf("NewServer = %v, want nil", err)
	}

	read, ok := s.ListTools()[description.ReadTerminalName]
	if !ok {
		t.Fatalf("read_terminal not registered; registered tools = %v", toolNames(s.ListTools()))
	}
	if read.Tool.Name != description.ReadTerminalName {
		t.Fatalf("read_terminal tool name = %q, want %q", read.Tool.Name, description.ReadTerminalName)
	}
	if read.Tool.Description != description.ReadTerminalDescription {
		t.Fatalf("read_terminal description = %q, want %q", read.Tool.Description, description.ReadTerminalDescription)
	}
	if read.Handler == nil {
		t.Fatal("read_terminal handler is nil")
	}

	props := read.Tool.InputSchema.Properties
	if len(props) != 4 {
		t.Fatalf("read_terminal input schema has %d properties, want 4", len(props))
	}
	assertReadStringProperty(t, props, "mode", []string{"stream", "snapshot", "drain"})
	assertReadIntegerProperty(t, props, "cursor", 0, nil)
	assertReadIntegerProperty(t, props, "max_bytes", 1, 65536)
	assertReadIntegerProperty(t, props, "wait_ms", 0, 5000)
	if len(read.Tool.InputSchema.Required) != 0 {
		t.Fatalf("read_terminal required fields = %v, want none", read.Tool.InputSchema.Required)
	}
}

func TestNewServer_RegistersCloseTerminalTool(t *testing.T) {
	t.Parallel()
	s, err := NewServer(registry.NewRegistry())
	if err != nil {
		t.Fatalf("NewServer = %v, want nil", err)
	}

	close, ok := s.ListTools()[description.CloseTerminalName]
	if !ok {
		t.Fatalf("close_terminal not registered; registered tools = %v", toolNames(s.ListTools()))
	}
	if close.Tool.Name != description.CloseTerminalName {
		t.Fatalf("close_terminal name = %q, want %q", close.Tool.Name, description.CloseTerminalName)
	}
	if close.Tool.Description != description.CloseTerminalDescription {
		t.Fatalf("close_terminal description = %q, want %q", close.Tool.Description, description.CloseTerminalDescription)
	}
	if close.Handler == nil {
		t.Fatal("close_terminal handler is nil")
	}
	if len(close.Tool.InputSchema.Properties) != 1 {
		t.Fatalf("close_terminal properties = %v, want only session_id", close.Tool.InputSchema.Properties)
	}
	property, ok := close.Tool.InputSchema.Properties["session_id"].(map[string]any)
	if !ok || property["type"] != "string" {
		t.Fatalf("close_terminal session_id schema = %#v, want required string", close.Tool.InputSchema.Properties["session_id"])
	}
	if !containsString(close.Tool.InputSchema.Required, "session_id") {
		t.Fatalf("close_terminal required = %v, want session_id", close.Tool.InputSchema.Required)
	}
}

func TestNewServer_RegistersSendControlTool(t *testing.T) {
	t.Parallel()
	s, err := NewServer(registry.NewRegistry())
	if err != nil {
		t.Fatalf("NewServer = %v, want nil", err)
	}

	control, ok := s.ListTools()[description.SendControlName]
	if !ok {
		t.Fatalf("send_control not registered; registered tools = %v", toolNames(s.ListTools()))
	}
	if control.Tool.Name != description.SendControlName {
		t.Fatalf("send_control name = %q, want %q", control.Tool.Name, description.SendControlName)
	}
	if control.Tool.Description != description.SendControlDescription {
		t.Fatalf("send_control description = %q, want %q", control.Tool.Description, description.SendControlDescription)
	}
	if control.Handler == nil {
		t.Fatal("send_control handler is nil")
	}
	if len(control.Tool.InputSchema.Properties) != 1 {
		t.Fatalf("send_control properties = %v, want only key", control.Tool.InputSchema.Properties)
	}
	property, ok := control.Tool.InputSchema.Properties["key"].(map[string]any)
	if !ok || property["type"] != "string" {
		t.Fatalf("send_control key schema = %#v, want required string", control.Tool.InputSchema.Properties["key"])
	}
	if !containsString(control.Tool.InputSchema.Required, "key") {
		t.Fatalf("send_control required = %v, want key", control.Tool.InputSchema.Required)
	}
}

func assertReadStringProperty(t *testing.T, props map[string]any, name string, wantEnum []string) {
	t.Helper()
	property, ok := props[name].(map[string]any)
	if !ok {
		t.Fatalf("read_terminal %q property = %T, want map[string]any", name, props[name])
	}
	if got, _ := property["type"].(string); got != "string" {
		t.Fatalf("read_terminal %q type = %q, want string", name, got)
	}
	if got, ok := property["enum"].([]string); !ok || !equalStringSlices(got, wantEnum) {
		t.Fatalf("read_terminal %q enum = %#v, want %#v", name, property["enum"], wantEnum)
	}
}

func assertReadIntegerProperty(t *testing.T, props map[string]any, name string, wantMin int, wantMax any) {
	t.Helper()
	property, ok := props[name].(map[string]any)
	if !ok {
		t.Fatalf("read_terminal %q property = %T, want map[string]any", name, props[name])
	}
	if got, _ := property["type"].(string); got != "integer" {
		t.Fatalf("read_terminal %q type = %q, want integer", name, got)
	}
	if got := property["minimum"]; got != wantMin {
		t.Fatalf("read_terminal %q minimum = %#v, want %d", name, got, wantMin)
	}
	if wantMax == nil {
		if _, ok := property["maximum"]; ok {
			t.Fatalf("read_terminal %q maximum = %#v, want omitted", name, property["maximum"])
		}
		return
	}
	if got := property["maximum"]; got != wantMax {
		t.Fatalf("read_terminal %q maximum = %#v, want %#v", name, got, wantMax)
	}
}

func equalStringSlices(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// containsString reports whether s is in the slice. Kept local to avoid pulling
// in slices for one-off test use.
func containsString(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func toolNames(m map[string]*mcpserver.ServerTool) []string {
	out := make([]string, 0, len(m))
	for n := range m {
		out = append(out, n)
	}
	return out
}
