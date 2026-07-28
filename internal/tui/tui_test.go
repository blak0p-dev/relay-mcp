package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/blak0p/relay-mcp/internal/clientconfig"
)

func TestModelGuidesMaintenanceAndClientConfiguration(t *testing.T) {
	controller := Controller{
		Clients:   []clientconfig.Client{{Name: "claude-code", Selected: true}, {Name: "codex"}},
		Configure: func([]clientconfig.Client) Result { return Result{Summary: "Configured clients"} },
	}
	model := NewModel(controller)
	model = update(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = update(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(model.View(), "claude-code [x]") || !strings.Contains(model.View(), "codex [ ]") {
		t.Fatalf("selection view = %q", model.View())
	}
	model = update(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = update(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if !strings.Contains(model.View(), "Configured clients") {
		t.Fatalf("outcome view = %q", model.View())
	}
}

func TestSmokeConfiguresClientAndExits(t *testing.T) {
	model := NewModel(Controller{
		Clients:   []clientconfig.Client{{Name: "claude-code", Selected: true}},
		Configure: func([]clientconfig.Client) Result { return Result{Summary: "Configured clients"} },
	})
	testModel := teatest.NewTestModel(t, model)
	testModel.Send(tea.KeyMsg{Type: tea.KeyDown})
	testModel.Send(tea.KeyMsg{Type: tea.KeyEnter})
	testModel.Send(tea.KeyMsg{Type: tea.KeyEnter})
	testModel.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	teatest.WaitFor(t, testModel.Output(), func(output []byte) bool { return strings.Contains(string(output), "Configured clients") }, teatest.WithDuration(time.Second))
	testModel.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	testModel.WaitFinished(t, teatest.WithFinalTimeout(time.Second))
}

func TestModelHandlesCancellationNoClientsAndRemediation(t *testing.T) {
	mutated := false
	model := NewModel(Controller{
		UpdateRelay: func() Result { mutated = true; return Result{} },
		Configure: func([]clientconfig.Client) Result {
			mutated = true
			return Result{Summary: "partial", Remediation: "Fix codex permissions"}
		},
	})
	model = update(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = update(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(model.View(), "No supported clients found") {
		t.Fatalf("no-client view = %q", model.View())
	}
	if mutated {
		t.Fatal("no-client guidance mutated configuration")
	}
	model = NewModel(Controller{UpdateRelay: func() Result { mutated = true; return Result{Summary: "changed"} }})
	model = update(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = update(t, model, tea.KeyMsg{Type: tea.KeyEsc})
	if mutated || !strings.Contains(model.View(), "Cancelled") {
		t.Fatalf("cancelled view = %q, mutated = %v", model.View(), mutated)
	}

	model = NewModel(Controller{Clients: []clientconfig.Client{{Name: "codex", Selected: true}}, Configure: func([]clientconfig.Client) Result {
		return Result{Summary: "partial", Err: errors.New("codex failed"), Remediation: "Fix codex permissions"}
	}})
	model = update(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = update(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = update(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = update(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if !strings.Contains(model.View(), "codex failed") || !strings.Contains(model.View(), "Fix codex permissions") {
		t.Fatalf("remediation view = %q", model.View())
	}
}

func TestModelUpdatesRelayTruthfullyAndSupportsVimNavigation(t *testing.T) {
	for _, tt := range []struct {
		name   string
		result Result
		want   string
	}{
		{"completed", Result{Summary: "Relay updated to 1.2.3"}, "Relay updated to 1.2.3"},
		{"current", Result{Summary: "Relay is already current"}, "Relay is already current"},
		{"deferred", Result{Summary: "Relay 1.2.3 staged; replacement did not complete", Remediation: "Exit Relay, then replace from /tmp/relay"}, "did not complete"},
		{"error", Result{Err: errors.New("verification failed"), Remediation: "Retry when online"}, "verification failed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			model := NewModel(Controller{UpdateRelay: func() Result { calls++; return tt.result }})
			if !strings.Contains(model.View(), "Update Relay") {
				t.Fatalf("menu = %q", model.View())
			}
			model = update(t, model, tea.KeyMsg{Type: tea.KeyEnter})
			model = update(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
			if calls != 1 || !strings.Contains(model.View(), tt.want) {
				t.Fatalf("outcome = %q, calls = %d", model.View(), calls)
			}
		})
	}
	controller := Controller{Clients: []clientconfig.Client{{Name: "claude"}, {Name: "codex"}}}
	for _, tt := range []struct {
		name  string
		model Model
		key   tea.KeyMsg
		want  int
	}{
		{"menu down arrow", NewModel(controller), tea.KeyMsg{Type: tea.KeyDown}, 1},
		{"menu j", NewModel(controller), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")}, 1},
		{"menu up arrow", Model{controller: controller, screen: menu, cursor: 1}, tea.KeyMsg{Type: tea.KeyUp}, 0},
		{"menu k", Model{controller: controller, screen: menu, cursor: 1}, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")}, 0},
		{"clients down arrow", Model{controller: controller, screen: clients}, tea.KeyMsg{Type: tea.KeyDown}, 1},
		{"clients j", Model{controller: controller, screen: clients}, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")}, 1},
		{"clients up arrow", Model{controller: controller, screen: clients, cursor: 1}, tea.KeyMsg{Type: tea.KeyUp}, 0},
		{"clients k", Model{controller: controller, screen: clients, cursor: 1}, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")}, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			model := update(t, tt.model, tt.key)
			if model.cursor != tt.want {
				t.Fatalf("cursor = %d, want %d", model.cursor, tt.want)
			}
		})
	}
}

func update(t *testing.T, model Model, message tea.Msg) Model {
	t.Helper()
	next, command := model.Update(message)
	if command != nil {
		next, _ = next.Update(command())
	}
	return next.(Model)
}
