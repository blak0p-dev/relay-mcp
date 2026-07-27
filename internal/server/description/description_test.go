package description

import (
	"strings"
	"testing"
)

func TestServerIdentity_DefaultsToRelayDevelopmentVersion(t *testing.T) {
	t.Parallel()

	if ServerName != "relay" {
		t.Fatalf("ServerName = %q, want %q", ServerName, "relay")
	}
	if ServerVersion != "dev" {
		t.Fatalf("ServerVersion = %q, want %q", ServerVersion, "dev")
	}
}

func TestCreateTerminalConstants_NonEmpty(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		value string
	}{
		{"CreateTerminalName", CreateTerminalName},
		{"CreateTerminalSummary", CreateTerminalSummary},
		{"CreateTerminalDescription", CreateTerminalDescription},
	}
	for _, c := range cases {
		if c.value == "" {
			t.Fatalf("%s is empty, want a non-empty string", c.name)
		}
	}
}

func TestCreateTerminalName_IsCreateTerminal(t *testing.T) {
	t.Parallel()
	if CreateTerminalName != "create_terminal" {
		t.Fatalf("CreateTerminalName = %q, want %q", CreateTerminalName, "create_terminal")
	}
}

func TestWriteTerminalConstants_NonEmpty(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		value string
	}{
		{"WriteTerminalName", WriteTerminalName},
		{"WriteTerminalSummary", WriteTerminalSummary},
		{"WriteTerminalDescription", WriteTerminalDescription},
	}
	for _, c := range cases {
		if c.value == "" {
			t.Fatalf("%s is empty, want a non-empty string", c.name)
		}
	}
}

func TestWriteTerminalName_IsWriteTerminal(t *testing.T) {
	t.Parallel()
	if WriteTerminalName != "write_terminal" {
		t.Fatalf("WriteTerminalName = %q, want %q", WriteTerminalName, "write_terminal")
	}
}

// TestWriteTerminalConstants_DistinctFromCreate asserts the write_terminal
// constants do not duplicate the create_terminal constants (REQ-WT-007 —
// single source of truth per tool).
func TestWriteTerminalConstants_DistinctFromCreate(t *testing.T) {
	t.Parallel()
	if WriteTerminalName == CreateTerminalName {
		t.Fatalf("WriteTerminalName equals CreateTerminalName, want distinct")
	}
	if WriteTerminalSummary == CreateTerminalSummary {
		t.Fatalf("WriteTerminalSummary equals CreateTerminalSummary, want distinct")
	}
	if WriteTerminalDescription == CreateTerminalDescription {
		t.Fatalf("WriteTerminalDescription equals CreateTerminalDescription, want distinct")
	}
}

// TestWriteTerminalDescription_StatesContract asserts the description tells
// the agent the required policy, its true/false behavior, and the 1 MiB cap.
func TestWriteTerminalDescription_StatesContract(t *testing.T) {
	t.Parallel()
	for _, phrase := range []string{
		"ensure_newline is required",
		"true appends one trailing LF",
		"false preserves bytes exactly",
		"1 MiB",
	} {
		if !strings.Contains(WriteTerminalDescription, phrase) {
			t.Fatalf("WriteTerminalDescription missing %q; got %q", phrase, WriteTerminalDescription)
		}
	}
}

func TestSendControlConstants_DescribeFiniteAllowlist(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		value string
	}{
		{"SendControlName", SendControlName},
		{"SendControlSummary", SendControlSummary},
		{"SendControlDescription", SendControlDescription},
	}
	for _, c := range cases {
		if c.value == "" {
			t.Fatalf("%s is empty, want a non-empty string", c.name)
		}
	}
	if SendControlName != "send_control" {
		t.Fatalf("SendControlName = %q, want send_control", SendControlName)
	}
	if !strings.Contains(SendControlDescription, "allowlist") || !strings.Contains(SendControlDescription, "active") {
		t.Fatalf("SendControlDescription = %q, want finite allowlist and active-session contract", SendControlDescription)
	}
}
