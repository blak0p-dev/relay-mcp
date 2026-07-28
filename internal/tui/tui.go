package tui

import (
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/blak0p/relay-mcp/internal/clientconfig"
)

type Result struct {
	Summary, Remediation string
	Err                  error
}

type Controller struct {
	Clients   []clientconfig.Client
	Maintain  func() Result
	Configure func([]clientconfig.Client) Result
}

func NewController(home string) Controller {
	return Controller{
		Clients: clientconfig.Detect(home),
		Maintain: func() Result {
			return Result{Err: errors.New("no verified release is selected"), Remediation: "Select a verified Relay release before maintenance."}
		},
		Configure: func(clients []clientconfig.Client) Result {
			var completed, failed []string
			for _, client := range clients {
				if !client.Selected {
					continue
				}
				if _, err := (clientconfig.FileStore{}).Apply(client.Adapter, client.Path); err != nil {
					failed = append(failed, client.Name+": "+err.Error())
				} else {
					completed = append(completed, client.Name)
				}
			}
			result := Result{Summary: "Configured: " + strings.Join(completed, ", ")}
			if len(failed) > 0 {
				result.Err = errors.New(strings.Join(failed, "; "))
				result.Remediation = "Review the reported client configuration and rerun the action."
			}
			return result
		},
	}
}

type screen uint8

const (
	menu screen = iota
	clients
	confirm
	outcome
)

type Model struct {
	controller Controller
	screen     screen
	cursor     int
	action     string
	result     Result
}
type resultMsg Result

func NewModel(controller Controller) Model { return Model{controller: controller} }
func (m Model) Init() tea.Cmd              { return nil }

func (m Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if result, ok := message.(resultMsg); ok {
		m.result = Result(result)
		return m, nil
	}
	key, ok := message.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	if key.String() == "q" || key.Type == tea.KeyCtrlC {
		return m, tea.Quit
	}
	switch m.screen {
	case menu:
		if key.Type == tea.KeyDown && m.cursor < 2 {
			m.cursor++
		}
		if key.Type == tea.KeyUp && m.cursor > 0 {
			m.cursor--
		}
		if key.Type == tea.KeyEnter {
			switch m.cursor {
			case 0:
				m.action, m.screen = "Maintain Relay", confirm
			case 1:
				m.action, m.screen = "Configure clients", clients
			default:
				return m, tea.Quit
			}
		}
	case clients:
		if len(m.controller.Clients) == 0 {
			return m, nil
		}
		if key.Type == tea.KeyDown && m.cursor < len(m.controller.Clients)-1 {
			m.cursor++
		}
		if key.Type == tea.KeyUp && m.cursor > 0 {
			m.cursor--
		}
		if key.String() == " " {
			m.controller.Clients[m.cursor].Selected = !m.controller.Clients[m.cursor].Selected
		}
		if key.Type == tea.KeyEnter {
			m.screen = confirm
		}
	case confirm:
		if key.Type == tea.KeyEsc || key.String() == "n" {
			m.result, m.screen = Result{Summary: "Cancelled"}, outcome
			return m, nil
		}
		if key.String() == "y" {
			m.screen, m.result = outcome, Result{Summary: "Working..."}
			return m, func() tea.Msg {
				if m.action == "Maintain Relay" {
					return resultMsg(m.controller.Maintain())
				}
				return resultMsg(m.controller.Configure(m.controller.Clients))
			}
		}
	case outcome:
		if key.Type == tea.KeyEnter || key.Type == tea.KeyEsc {
			m.screen, m.cursor = menu, 0
		}
	}
	return m, nil
}
func (m Model) View() string {
	switch m.screen {
	case menu:
		items := []string{"Maintain Relay", "Configure clients", "Quit"}
		return "Relay installer\n\n" + menuView(items, m.cursor)
	case clients:
		if len(m.controller.Clients) == 0 {
			return "No supported clients found. Add Relay manually, then rerun this installer.\n"
		}
		var lines []string
		for i, client := range m.controller.Clients {
			mark := " "
			if client.Selected {
				mark = "x"
			}
			prefix := " "
			if i == m.cursor {
				prefix = ">"
			}
			lines = append(lines, fmt.Sprintf("%s %s [%s]", prefix, client.Name, mark))
		}
		return "Select clients (space toggles, enter continues)\n\n" + strings.Join(lines, "\n") + "\n"
	case confirm:
		return "Confirm " + m.action + "? [y/n]\n"
	default:
		view := m.result.Summary
		if m.result.Err != nil {
			view += "\nFailed: " + m.result.Err.Error()
		}
		if m.result.Remediation != "" {
			view += "\nRemediation: " + m.result.Remediation
		}
		return view + "\n\nPress enter to return.\n"
	}
}
func menuView(items []string, cursor int) string {
	for i, item := range items {
		if i == cursor {
			items[i] = "> " + item
		} else {
			items[i] = "  " + item
		}
	}
	return strings.Join(items, "\n") + "\n"
}

func Run(home string) error {
	_, err := tea.NewProgram(NewModel(NewController(home))).Run()
	return err
}
