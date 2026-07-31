package tui

import (
	"testing"

	"agentfactory.sh/af/internal/config"
	"agentfactory.sh/af/internal/core"
)

func TestApplyRefreshIgnoresOlderRequest(t *testing.T) {
	model := newModel(Deps{Config: config.Defaults()})
	current := &core.AgentSession{ID: "current"}
	model.sessions = []*core.AgentSession{current}
	model.refreshSeq = 2

	model.applyRefresh(refreshMsg{
		seq:        1,
		sessions:   []*core.AgentSession{{ID: "stale"}},
		selectedID: "stale",
	})
	if len(model.sessions) != 1 || model.sessions[0].ID != current.ID {
		t.Fatalf("older refresh replaced current sessions: %+v", model.sessions)
	}
}

func TestApplyRefreshClearsPreviewWhenSelectionDisappears(t *testing.T) {
	model := newModel(Deps{Config: config.Defaults()})
	model.sessions = []*core.AgentSession{{ID: "old"}}
	model.selectedID = "old"
	model.preview = "stale preview"
	model.refreshSeq = 1

	model.applyRefresh(refreshMsg{
		seq:        1,
		selectedID: "old",
		sessions:   []*core.AgentSession{{ID: "new"}},
	})
	if model.selectedID != "new" {
		t.Fatalf("selection = %q, want new", model.selectedID)
	}
	if model.preview != "" {
		t.Fatalf("vanished selection retained preview %q", model.preview)
	}
}
