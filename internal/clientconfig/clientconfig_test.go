package clientconfig

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestDetectPreselectsExistingClientConfigs(t *testing.T) {
	home := t.TempDir()
	writeFixture(t, filepath.Join(home, ".claude.json"), `{}`)
	writeFixture(t, filepath.Join(home, ".codex", "config.toml"), "model = \"o3\"\n")
	writeFixture(t, filepath.Join(home, ".config", "opencode", "opencode.json"), `{}`)

	clients := Detect(home)
	got := selectedNames(clients)
	want := []string{"claude-code", "codex", "opencode"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("selected clients = %v, want %v", got, want)
	}
}

func TestAdaptersMergeRelayWithoutDuplicatingOrDiscardingEntries(t *testing.T) {
	cases := []struct {
		name     string
		adapter  Adapter
		input    string
		contains []string
	}{
		{"claude", ClaudeCode(), `{"theme":"dark","mcpServers":{"other":{"command":"other"}}}`, []string{`"theme": "dark"`, `"other"`, `"relay"`}},
		{"codex", Codex(), "model = \"o3\"\n[mcp_servers.other]\ncommand = \"other\"\n", []string{"model = \"o3\"", "[mcp_servers.other]", "[mcp_servers.relay]", "command = \"relay\""}},
		{"opencode", OpenCode(), "// user comment\n{\"$schema\":\"schema\",\"mcp\":{\"other\":{\"command\":[\"other\"]}}}", []string{`"$schema": "schema"`, `"other"`, `"relay"`}},
		{"pi", Pi(), `{"theme":"dark","mcpServers":{"other":{"command":"other"}}}`, []string{`"theme": "dark"`, `"other"`, `"relay"`}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			first, changed, err := tt.adapter.Merge([]byte(tt.input))
			if err != nil || !changed {
				t.Fatalf("first Merge() = %q, %v, %v", first, changed, err)
			}
			for _, part := range tt.contains {
				if !contains(string(first), part) {
					t.Fatalf("merged config %q does not contain %q", first, part)
				}
			}
			second, changed, err := tt.adapter.Merge(first)
			if err != nil || changed || string(second) != string(first) {
				t.Fatalf("second Merge() = %q, %v, %v; want idempotent", second, changed, err)
			}
		})
	}
}

func TestPiCommandUsesFixedArgvWithoutShell(t *testing.T) {
	command := Pi().Command("/tmp/relay; echo unsafe")
	want := []string{"pi", "mcp", "add", "relay", "--", "/tmp/relay; echo unsafe"}
	if !reflect.DeepEqual(command, want) {
		t.Fatalf("Pi command = %#v, want %#v", command, want)
	}
}

func TestFileStoreAppliesValidatedBackupAndAtomicReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	original := `{"theme":"dark","mcpServers":{"other":{"command":"other"}}}`
	writeFixture(t, path, original)
	store := FileStore{Now: func() time.Time { return time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC) }}

	changed, err := store.Apply(ClaudeCode(), path)
	if err != nil || !changed {
		t.Fatalf("Apply() = %v, %v", changed, err)
	}
	backup, err := os.ReadFile(path + ".relay-backup-20260728T120000Z")
	if err != nil || string(backup) != original {
		t.Fatalf("backup = %q, %v", backup, err)
	}
	updated, err := os.ReadFile(path)
	if err != nil || !contains(string(updated), `"relay"`) || !contains(string(updated), `"other"`) {
		t.Fatalf("updated config = %q, %v", updated, err)
	}
	changed, err = store.Apply(ClaudeCode(), path)
	if err != nil || changed {
		t.Fatalf("rerun Apply() = %v, %v; want idempotent", changed, err)
	}
}

func TestFileStoreRejectsInvalidInputAndHashRaceWithoutReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	writeFixture(t, path, "not json")
	if _, err := (FileStore{}).Apply(ClaudeCode(), path); err == nil {
		t.Fatal("Apply() succeeded for invalid input")
	}
	if got, _ := os.ReadFile(path); string(got) != "not json" {
		t.Fatalf("invalid config changed to %q", got)
	}
	if _, changed, err := Codex().Merge([]byte("not toml")); err == nil || changed {
		t.Fatalf("Codex Merge() = %v, %v; want invalid input rejection", changed, err)
	}

	writeFixture(t, path, `{}`)
	store := FileStore{BeforeReplace: func() error { return os.WriteFile(path, []byte(`{"external":true}`), 0o600) }}
	if _, err := store.Apply(ClaudeCode(), path); !errors.Is(err, ErrSourceChanged) {
		t.Fatalf("Apply() error = %v, want ErrSourceChanged", err)
	}
	if got, _ := os.ReadFile(path); string(got) != `{"external":true}` {
		t.Fatalf("race replacement overwrote %q", got)
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".config.json.relay-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("temporary files = %v, %v", matches, err)
	}
}

func TestFileStorePermissionFailurePreservesConfigAndBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	original := `{"theme":"dark"}`
	writeFixture(t, path, original)
	store := FileStore{Now: func() time.Time { return time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC) }}
	backupPath := path + ".relay-backup-20260728T120000Z"
	if err := os.Mkdir(backupPath, 0o700); err != nil {
		t.Fatal(err)
	}

	changed, err := store.Apply(ClaudeCode(), path)
	if err == nil || changed || !contains(err.Error(), "write config backup") {
		t.Fatalf("Apply() = %v, %v; want backup write failure", changed, err)
	}
	if got, readErr := os.ReadFile(path); readErr != nil || string(got) != original {
		t.Fatalf("config = %q, %v; want original", got, readErr)
	}
	if info, statErr := os.Stat(backupPath); statErr != nil || !info.IsDir() {
		t.Fatalf("backup destination = %v, %v; want preserved directory", info, statErr)
	}
}

func writeFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func selectedNames(clients []Client) []string {
	var names []string
	for _, client := range clients {
		if client.Selected {
			names = append(names, client.Name)
		}
	}
	return names
}

func contains(s, part string) bool {
	return len(part) == 0 || (len(s) >= len(part) && index(s, part) >= 0)
}

func index(s, part string) int {
	for i := 0; i+len(part) <= len(s); i++ {
		if s[i:i+len(part)] == part {
			return i
		}
	}
	return -1
}
