package tui

import (
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"agentfactory.sh/af/internal/config"
	"agentfactory.sh/af/internal/core"
)

type confirmBackend struct {
	alive map[string]bool
}

func (b *confirmBackend) Create(sess *core.AgentSession) error { return nil }
func (b *confirmBackend) Attach(id string) error               { return nil }
func (b *confirmBackend) SetSendDelay(delay time.Duration)     {}
func (b *confirmBackend) PrepareAttach(id string) (core.AttachSpec, error) {
	return core.AttachSpec{}, nil
}

func (b *confirmBackend) SyncSize(id string, width, height int) (bool, error) {
	return false, nil
}
func (b *confirmBackend) CapturePane(id string, lines int) (string, error) { return "", nil }
func (b *confirmBackend) SendKeys(id, input string, enter bool) error      { return nil }
func (b *confirmBackend) IsAlive(id string) (bool, error)                  { return b.alive[id], nil }
func (b *confirmBackend) DeadStatus(id string) (bool, int, error)          { return false, 0, nil }
func (b *confirmBackend) Kill(id string) error {
	b.alive[id] = false
	return nil
}

func TestConfirmReResolvesSessionBeforeClose(t *testing.T) {
	store, openErr := core.OpenStore(t.TempDir())
	if openErr != nil {
		t.Fatal(openErr)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	sess := &core.AgentSession{
		ID: "confirm001", Name: "confirm", Harness: core.HarnessCustom,
		Command: "sleep 600", WorkDir: "/", Status: core.StatusWorking,
		LogPath:   filepath.Join(t.TempDir(), "session.log"),
		StartedAt: now, LastActive: now,
	}
	if insertErr := store.InsertSession(sess); insertErr != nil {
		t.Fatal(insertErr)
	}
	backend := &confirmBackend{alive: map[string]bool{sess.ID: true}}
	manager := &core.Manager{
		Store: store, Backend: backend, Harnesses: core.NewHarnessSet(nil),
		CloseTimeout: time.Millisecond,
	}
	model := newModel(Deps{Config: config.Defaults(), Store: store, Backend: backend, Manager: manager})
	model.sessions = []*core.AgentSession{sess}
	model.selectedID = sess.ID
	model.confirmSelected(verbClose)

	terminal, loadErr := store.GetSession(sess.ID)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	endedAt := time.Now().UTC()
	terminal.Status = core.StatusExited
	terminal.EndedAt = &endedAt
	if updateErr := store.UpdateSession(terminal); updateErr != nil {
		t.Fatal(updateErr)
	}
	backend.alive[sess.ID] = false

	_, command := model.handleConfirmKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if command == nil {
		t.Fatal("confirming close must return a command")
	}
	command()
	got, err := store.GetSession(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != core.StatusExited {
		t.Fatalf("stale close confirmation overwrote terminal status as %s", got.Status)
	}
}
