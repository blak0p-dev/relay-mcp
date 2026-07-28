// Package clientconfig safely discovers and prepares supported MCP client configurations.
package clientconfig

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const relayCommand = "relay"

// ErrSourceChanged means another writer modified the configuration during the
// transaction; no replacement was performed.
var ErrSourceChanged = errors.New("configuration changed during transaction")

// Client is a discovered configuration target. Selected mirrors discovery so
// callers can preselect installed clients while still offering absent clients.
type Client struct {
	Name     string
	Adapter  Adapter
	Path     string
	Selected bool
}

// Adapter owns the format-specific, structural Relay merge.
type Adapter interface {
	Name() string
	Path(home string) string
	Merge(input []byte) (output []byte, changed bool, err error)
}

// Detect returns all supported clients in stable presentation order.
func Detect(home string) []Client {
	adapters := []Adapter{ClaudeCode(), Codex(), OpenCode(), Pi()}
	clients := make([]Client, 0, len(adapters))
	for _, adapter := range adapters {
		path := adapter.Path(home)
		_, err := os.Stat(path)
		clients = append(clients, Client{Name: adapter.Name(), Adapter: adapter, Path: path, Selected: err == nil})
	}
	return clients
}

type jsonAdapter struct{ name, relative, key string }

func (a jsonAdapter) Name() string            { return a.name }
func (a jsonAdapter) Path(home string) string { return filepath.Join(home, a.relative) }
func (a jsonAdapter) Merge(input []byte) ([]byte, bool, error) {
	var document map[string]any
	if len(bytes.TrimSpace(input)) == 0 {
		document = map[string]any{}
	} else if err := json.Unmarshal(jsonWithoutComments(input), &document); err != nil {
		return nil, false, fmt.Errorf("parse %s config: %w", a.name, err)
	}
	servers, ok := document[a.key].(map[string]any)
	if !ok {
		if document[a.key] != nil {
			return nil, false, fmt.Errorf("%s is not an object", a.key)
		}
		servers = map[string]any{}
		document[a.key] = servers
	}
	want := map[string]any{"command": relayCommand}
	if a.name == "opencode" {
		want = map[string]any{"type": "local", "command": []any{relayCommand}}
	}
	if current, exists := servers["relay"]; exists && reflectJSON(current, want) {
		return input, false, nil
	}
	servers["relay"] = want
	output, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, false, fmt.Errorf("encode %s config: %w", a.name, err)
	}
	return append(output, '\n'), true, nil
}

func jsonWithoutComments(input []byte) []byte {
	var output []byte
	inString, escaped := false, false
	for i := 0; i < len(input); i++ {
		if !inString && input[i] == '/' && i+1 < len(input) {
			if input[i+1] == '/' {
				for i < len(input) && input[i] != '\n' {
					i++
				}
				if i < len(input) {
					output = append(output, '\n')
				}
				continue
			}
			if input[i+1] == '*' {
				i += 2
				for i+1 < len(input) && (input[i] != '*' || input[i+1] != '/') {
					i++
				}
				i++
				continue
			}
		}
		output = append(output, input[i])
		if input[i] == '"' && !escaped {
			inString = !inString
		}
		escaped = input[i] == '\\' && !escaped
		if input[i] != '\\' {
			escaped = false
		}
	}
	return output
}

type codexAdapter struct{}

func (codexAdapter) Name() string            { return "codex" }
func (codexAdapter) Path(home string) string { return filepath.Join(home, ".codex", "config.toml") }
func (codexAdapter) Merge(input []byte) ([]byte, bool, error) {
	if err := validateTOML(input); err != nil {
		return nil, false, fmt.Errorf("parse codex config: %w", err)
	}
	if strings.Contains(string(input), "[mcp_servers.relay]") {
		return input, false, nil
	}
	output := append(append([]byte{}, input...), []byte("\n[mcp_servers.relay]\ncommand = \"relay\"\n")...)
	return output, true, nil
}

func validateTOML(input []byte) error {
	for _, line := range strings.Split(string(input), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			return errors.New("invalid TOML line")
		}
	}
	return nil
}

// ClaudeCode merges ~/.claude.json.
func ClaudeCode() Adapter { return jsonAdapter{"claude-code", ".claude.json", "mcpServers"} }

// Codex merges ~/.codex/config.toml.
func Codex() Adapter { return codexAdapter{} }

// OpenCode merges ~/.config/opencode/opencode.json.
func OpenCode() Adapter {
	return jsonAdapter{"opencode", filepath.Join(".config", "opencode", "opencode.json"), "mcp"}
}

// Pi merges ~/.pi/agent/settings.json.
func Pi() piAdapter {
	return piAdapter{jsonAdapter{"pi", filepath.Join(".pi", "agent", "settings.json"), "mcpServers"}}
}

type piAdapter struct{ jsonAdapter }

// Command returns fixed argv for Pi's CLI; callers must pass it to exec.Command,
// never a shell. The executable path remains one literal argument.
func (piAdapter) Command(relayPath string) []string {
	return []string{"pi", "mcp", "add", "relay", "--", relayPath}
}

func reflectJSON(a, b any) bool {
	left, leftErr := json.Marshal(a)
	right, rightErr := json.Marshal(b)
	return leftErr == nil && rightErr == nil && string(left) == string(right)
}

// FileStore commits one adapter merge with validation, backup, and an atomic
// same-directory rename. BeforeReplace is a test seam for concurrent-writer
// simulation; production callers leave it nil.
type FileStore struct {
	Now           func() time.Time
	BeforeReplace func() error
}

// Apply merges adapter output into path. It never replaces a file whose bytes
// differ from those originally validated.
func (s FileStore) Apply(adapter Adapter, path string) (bool, error) {
	original, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("read config: %w", err)
	}
	if errors.Is(err, os.ErrNotExist) {
		original = nil
	}
	updated, changed, err := adapter.Merge(original)
	if err != nil || !changed {
		return changed, err
	}
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".relay-*")
	if err != nil {
		return false, fmt.Errorf("create temp config: %w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if _, err := temp.Write(updated); err != nil {
		temp.Close()
		return false, fmt.Errorf("write temp config: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return false, fmt.Errorf("sync temp config: %w", err)
	}
	if err := temp.Close(); err != nil {
		return false, fmt.Errorf("close temp config: %w", err)
	}
	if s.BeforeReplace != nil {
		if err := s.BeforeReplace(); err != nil {
			return false, err
		}
	}
	current, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("re-read config: %w", err)
	}
	if sha256.Sum256(current) != sha256.Sum256(original) {
		return false, ErrSourceChanged
	}
	if len(original) > 0 {
		now := time.Now()
		if s.Now != nil {
			now = s.Now()
		}
		backup := path + ".relay-backup-" + now.UTC().Format("20060102T150405Z")
		if err := os.WriteFile(backup, original, 0o600); err != nil {
			return false, fmt.Errorf("write config backup: %w", err)
		}
	}
	if err := os.Rename(tempName, path); err != nil {
		return false, fmt.Errorf("replace config atomically: %w", err)
	}
	return true, nil
}
