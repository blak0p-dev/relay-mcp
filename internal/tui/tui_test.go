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
		Maintain: func() Result { mutated = true; return Result{} },
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
	model = NewModel(Controller{Maintain: func() Result { mutated = true; return Result{Summary: "changed"} }})
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

func update(t *testing.T, model Model, message tea.Msg) Model {
	t.Helper()
	next, command := model.Update(message)
	if command != nil {
		next, _ = next.Update(command())
	}
	return next.(Model)
}
