package tui_test

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"agentfactory.sh/af/internal/cli"
	"agentfactory.sh/af/internal/config"
	"agentfactory.sh/af/internal/core"
	"agentfactory.sh/af/internal/tmux"
	"agentfactory.sh/af/internal/tui"
)

func testDeps(t *testing.T) tui.Deps {
	t.Helper()
	buf := make([]byte, 4)
	rand.Read(buf)
	socket := "af-test-" + hex.EncodeToString(buf)
	dataDir := t.TempDir()
	t.Cleanup(func() {
		exec.Command("tmux", "-L", socket, "kill-server").Run()
		os.Remove(filepath.Join(fmt.Sprintf("/tmp/tmux-%d", os.Getuid()), socket))
	})
	// The command bar builds fresh cli roots, which read env config.
	t.Setenv("AF_SOCKET", socket)
	t.Setenv("AF_DATA_DIR", dataDir)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	cfg := config.Defaults()
	cfg.Socket = socket
	cfg.DataDir = dataDir
	cfg.IdleThreshold = config.Duration(time.Second)

	store, err := core.OpenStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	backend := tmux.New(socket, cfg.SendDelay.D())
	manager := &core.Manager{
		Store: store, Backend: backend, Harnesses: core.NewHarnessSet(nil),
		DataDir: dataDir, IdleThreshold: cfg.IdleThreshold.D(), CloseTimeout: cfg.CloseTimeout.D(),
	}
	return tui.Deps{Config: cfg, Store: store, Backend: backend, Manager: manager, NewRoot: cli.NewRoot}
}

func fixturePath(t *testing.T, name string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "testdata", "fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEscape}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

// waitTUI refreshes the model until cond holds (status changes need a
// reconciliation pass or two).
func waitTUI(t *testing.T, m tui.TestModel, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		m.Refresh()
		if cond() {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s\nview:\n%s", what, m.View())
}

func TestDashboardListPreviewAndConfirm(t *testing.T) {
	deps := testDeps(t)
	sess, err := deps.Manager.Open(core.OpenRequest{
		Cmd:  "sh " + fixturePath(t, "ticker.sh"),
		Name: "tickers",
	})
	if err != nil {
		t.Fatal(err)
	}

	m := tui.NewTestModel(deps)
	m.Send(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.Select(sess.ID)

	// List reflects the fixture's status; preview shows its screen.
	waitTUI(t, m, func() bool {
		view := m.View()
		return strings.Contains(view, "tickers") && strings.Contains(view, "working") &&
			strings.Contains(view, "tick")
	}, "list shows the session and its live preview")

	// Detail view on enter: metadata + env summary.
	m.Send(key("enter"))
	if view := m.View(); !strings.Contains(view, "AF_SESSION_ID") || !strings.Contains(view, sess.Command) {
		t.Fatalf("detail view missing metadata/env:\n%s", view)
	}
	m.Send(key("esc"))

	// x -> confirm prompt -> y -> session closes.
	m.Send(key("x"))
	if view := m.View(); !strings.Contains(view, "close") || !strings.Contains(view, "(y/n)") {
		t.Fatalf("confirm prompt missing:\n%s", view)
	}
	m.Send(key("y"))
	waitTUI(t, m, func() bool { return len(m.Sessions()) == 0 },
		"closed session leaves the list")
}

// TestDashboardKillConfirmCapitalY pins the shift-still-held path: X
// opens the kill confirm, and a capital Y must confirm, not cancel.
func TestDashboardKillConfirmCapitalY(t *testing.T) {
	deps := testDeps(t)
	sess, err := deps.Manager.Open(core.OpenRequest{Cmd: "sleep 60", Name: "shifty"})
	if err != nil {
		t.Fatal(err)
	}

	m := tui.NewTestModel(deps)
	m.Send(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.Select(sess.ID)
	waitTUI(t, m, func() bool { return len(m.Sessions()) == 1 }, "session appears in the list")

	m.Send(key("X"))
	if view := m.View(); !strings.Contains(view, "kill") || !strings.Contains(view, "(y/n)") {
		t.Fatalf("kill confirm prompt missing:\n%s", view)
	}
	// An unrelated key must not cancel the prompt.
	m.Send(key("j"))
	if view := m.View(); !strings.Contains(view, "(y/n)") {
		t.Fatalf("stray key dismissed the confirm prompt:\n%s", view)
	}
	m.Send(key("Y"))
	waitTUI(t, m, func() bool { return len(m.Sessions()) == 0 },
		"capital Y confirms the kill and the session leaves the list")
}

// TestDashboardFullScreenHarness drives the dashboard against a mock
// full-screen TUI (alt screen, cursor addressing, bottom input bar) —
// the shape of grok/opencode/claude-code. Preview must mirror the
// harness at the preview pane's size, and detail/logs must render
// readable text, never raw escape bytes.
func TestDashboardFullScreenHarness(t *testing.T) {
	deps := testDeps(t)
	sess, err := deps.Manager.Open(core.OpenRequest{
		Cmd:  "exec sh " + fixturePath(t, "tuiapp.sh"),
		Name: "mocktui",
	})
	if err != nil {
		t.Fatal(err)
	}

	m := tui.NewTestModel(deps)
	m.Send(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.Select(sess.ID)

	// The refresh resizes the detached window to the preview box, and
	// each input line makes the fixture redraw at whatever size it has
	// at that moment. On slow machines a redraw can land before the
	// resize does, so keep nudging until one lands *after* it — the
	// fixture reports its geometry (rows=N), which doubles as the
	// assertion that it saw the preview's size rather than 80x24.
	waitTUI(t, m, func() bool {
		m.Refresh()
		deps.Manager.Send(sess, "ping", true)
		view := m.View()
		return strings.Contains(view, "echo: ping") &&
			strings.Contains(view, "> input-bar") &&
			!strings.Contains(view, "rows=24")
	}, "preview mirrors the harness at the preview pane's size")

	// Detail view: every field renders, no escape bytes leak through.
	m.Send(key("enter"))
	view := m.View()
	for _, field := range []string{"Status", "Command", "WorkDir", "PID", "AF_SESSION_ID", "log lines"} {
		if !strings.Contains(view, field) {
			t.Errorf("detail view missing %q:\n%s", field, view)
		}
	}
	if strings.ContainsRune(view, 0x1b) {
		t.Error("detail view leaks raw escape bytes")
	}
	m.Send(key("esc"))

	// Logs view: readable text, no escape bytes.
	m.Send(key("l"))
	view = m.View()
	if !strings.Contains(view, "echo: ping") {
		t.Errorf("logs view should show harness output:\n%s", view)
	}
	if strings.ContainsRune(view, 0x1b) {
		t.Error("logs view leaks raw escape bytes")
	}
}

// TestDashboardDetailScrollAndWrap pins the scrollable detail view: on
// a short terminal the page is clipped (not overflowed), j scrolls the
// rest into view, and long values soft-wrap instead of truncating.
func TestDashboardDetailScrollAndWrap(t *testing.T) {
	deps := testDeps(t)
	longCmd := "sleep 60 # " + strings.Repeat("wrap", 60)
	sess, err := deps.Manager.Open(core.OpenRequest{Cmd: longCmd, Name: "longcmd"})
	if err != nil {
		t.Fatal(err)
	}

	m := tui.NewTestModel(deps)
	m.Send(tea.WindowSizeMsg{Width: 60, Height: 20})
	m.Select(sess.ID)
	waitTUI(t, m, func() bool { return len(m.Sessions()) == 1 }, "session appears in the list")

	m.Send(key("enter"))
	view := m.View()
	if strings.Contains(view, "…") {
		t.Errorf("detail view truncates instead of wrapping:\n%s", view)
	}
	if strings.Contains(view, "log lines") {
		t.Fatalf("short terminal should clip the detail page, got the log section on screen:\n%s", view)
	}
	for range 40 {
		m.Send(key("j"))
	}
	if view := m.View(); !strings.Contains(view, "log lines") {
		t.Fatalf("scrolling should reach the log section:\n%s", view)
	}
	m.Send(key("esc"))
}

func TestDashboardCommandBar(t *testing.T) {
	deps := testDeps(t)
	if _, err := deps.Manager.Open(core.OpenRequest{Cmd: "sleep 60", Name: "cmdbar"}); err != nil {
		t.Fatal(err)
	}
	m := tui.NewTestModel(deps)
	m.Send(tea.WindowSizeMsg{Width: 100, Height: 30})

	m.Send(key(":"))
	if !m.InCommandMode() {
		t.Fatal("':' should enter command mode")
	}
	for _, r := range "status" {
		m.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m.Send(key("enter"))
	if view := m.View(); !strings.Contains(view, "cmdbar") {
		t.Fatalf(":status output not shown in footer:\n%s", view)
	}
}

func TestDashboardPicker(t *testing.T) {
	deps := testDeps(t)
	def := &core.AgentDefinition{
		Name: "pickme", Harness: "custom",
		Config: map[string]string{"cmd": "sleep 60"},
	}
	if err := deps.Store.PutDefinition(def); err != nil {
		t.Fatal(err)
	}
	m := tui.NewTestModel(deps)
	m.Send(tea.WindowSizeMsg{Width: 100, Height: 30})

	m.Send(key("o"))
	if !m.InPickerMode() || !strings.Contains(m.View(), "pickme") {
		t.Fatalf("picker should list definitions:\n%s", m.View())
	}
	m.Send(key("enter"))
	waitTUI(t, m, func() bool {
		sessions := m.Sessions()
		return len(sessions) == 1 && sessions[0].Definition == "pickme"
	}, "picker opens the chosen definition")
}

func TestDashboardSaveDefinition(t *testing.T) {
	deps := testDeps(t)
	if _, err := deps.Manager.Open(core.OpenRequest{Cmd: "sleep 60", Name: "savory"}); err != nil {
		t.Fatal(err)
	}
	m := tui.NewTestModel(deps)
	m.Send(tea.WindowSizeMsg{Width: 100, Height: 30})
	waitTUI(t, m, func() bool { return len(m.Sessions()) == 1 }, "session loads into the list")

	// 's' prefills the command bar with an editable define --from command.
	m.Send(key("s"))
	if !m.InCommandMode() {
		t.Fatalf("'s' should enter the command bar:\n%s", m.View())
	}
	if view := m.View(); !strings.Contains(view, "define savory --from") {
		t.Fatalf("'s' should prefill 'define <name> --from <id>':\n%s", view)
	}

	// Running it saves the session's config as a definition.
	m.Send(key("enter"))
	waitTUI(t, m, func() bool {
		defs, _ := deps.Store.ListDefinitions()
		return len(defs) == 1 && defs[0].Name == "savory" &&
			defs[0].Harness == "custom" && defs[0].Config["cmd"] == "sleep 60"
	}, "the prefilled command saves the definition")
}

func TestDashboardDeleteDefinition(t *testing.T) {
	deps := testDeps(t)
	for _, n := range []string{"alpha", "beta"} { // ListDefinitions orders by name
		if err := deps.Store.PutDefinition(&core.AgentDefinition{
			Name: n, Harness: "custom", Config: map[string]string{"cmd": "sleep 60"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	m := tui.NewTestModel(deps)
	m.Send(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.Send(key("o"))
	if !m.InPickerMode() {
		t.Fatalf("expected picker mode:\n%s", m.View())
	}

	// Cancel path: d then n deletes nothing and stays in the picker.
	m.Send(key("d"))
	m.Send(key("n"))
	if defs, _ := deps.Store.ListDefinitions(); len(defs) != 2 {
		t.Fatalf("cancel must not delete; have %d defs", len(defs))
	}
	if !m.InPickerMode() {
		t.Fatalf("cancel returns to the picker:\n%s", m.View())
	}

	// Delete the selected definition (alpha, cursor 0): d then y.
	m.Send(key("d"))
	m.Send(key("y"))
	defs, err := deps.Store.ListDefinitions()
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 1 || defs[0].Name != "beta" {
		t.Fatalf("delete should remove alpha, leaving beta; got %v", defs)
	}
	if !m.InPickerMode() || !strings.Contains(m.View(), "beta") {
		t.Fatalf("picker should refresh in place to show beta:\n%s", m.View())
	}

	// Deleting the last definition drops back to the session list.
	m.Send(key("d"))
	m.Send(key("y"))
	if defs, _ := deps.Store.ListDefinitions(); len(defs) != 0 {
		t.Fatalf("second delete should empty the definitions; got %v", defs)
	}
	if m.InPickerMode() {
		t.Fatal("emptying definitions should leave the picker")
	}
}
