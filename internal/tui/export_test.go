package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"agentfactory.sh/af/internal/core"
)

// Test hooks. The dashboard tests live in the external package tui_test
// so they can use the real cli root for the command bar (package tui
// tests importing cli would be an import cycle: cli -> tui).

type TestModel struct{ m *model }

func NewTestModel(deps Deps) TestModel { return TestModel{newModel(deps)} }

// Send feeds a message into Update and pumps any resulting commands
// back through, the way the Bubble Tea runtime would (tick cycles are
// dropped to avoid looping forever).
func (t TestModel) Send(msg tea.Msg) {
	_, cmd := t.m.Update(msg)
	for cmd != nil {
		out := cmd()
		if out == nil {
			return
		}
		if _, isTick := out.(tickMsg); isTick {
			return
		}
		_, cmd = t.m.Update(out)
	}
}

// Refresh runs one reconcile + data-gather pass synchronously.
func (t TestModel) Refresh() {
	if msg := t.m.refreshCmd()(); msg != nil {
		t.m.Update(msg)
	}
}

func (t TestModel) View() string                   { return t.m.View() }
func (t TestModel) Sessions() []*core.AgentSession { return t.m.sessions }
func (t TestModel) Select(id string)               { t.m.selectedID = id }
func (t TestModel) InCommandMode() bool            { return t.m.mode == modeCommand }
func (t TestModel) InPickerMode() bool             { return t.m.mode == modePicker }
