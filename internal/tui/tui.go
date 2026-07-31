// Package tui is the af dashboard: a btop-inspired live view of every
// managed session, with previews, a command bar, and attach round-trips.
package tui

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"agentfactory.sh/af/internal/config"
	"agentfactory.sh/af/internal/core"
)

// Deps is everything the dashboard needs from the CLI layer. NewRoot
// builds a fresh af command tree so the command bar routes through the
// exact same Cobra root as the shell CLI (no subshell).
type Deps struct {
	Config  *config.Config
	Store   *core.Store
	Backend core.SessionBackend
	Manager *core.Manager
	NewRoot func() *cobra.Command
}

// Run starts the dashboard and blocks until quit.
func Run(deps Deps) error {
	m := newModel(deps)
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

type mode int

const (
	modeList mode = iota
	modeDetail
	modeLogs
	modeHelp
	modePicker
	modeConfirm
	modeCommand
)

// Bubble Tea key names shared across the mode handlers and help view.
const (
	keyEnter = "enter"
	keyEsc   = "esc"
)

// Confirm-prompt verbs. close and kill double as the prompt text;
// rm-def is picker-only.
const (
	verbClose   = "close"
	verbKill    = "kill"
	verbRmDef   = "rm-def"
	commandLogs = "logs"
)

// Dashboard tuning knobs.
const (
	flashTTL     = 4 * time.Second        // how long transient footer messages stay up
	redrawWait   = 120 * time.Millisecond // pause after a resize so the harness TUI repaints
	logTailBytes = 256 << 10              // max log bytes loaded into the logs view
)

// Panel chrome around embedded viewports (borders, padding, headers).
const (
	panelChromeCols  = 4 // left/right border + padding columns
	logsChromeRows   = 3 // header + border rows around the logs view
	detailChromeRows = 4 // header + border rows around the detail view
)

type model struct {
	deps   Deps
	mode   mode
	width  int
	height int

	sessions   []*core.AgentSession
	cursor     int
	selectedID string
	preview    string
	refreshSeq uint64

	// open-from-definition picker
	defs         []*core.AgentDefinition
	pickerCursor int

	// command bar + transient footer output
	input       textinput.Model
	flash       string
	flashExpiry time.Time

	// logs view
	logsVP   viewport.Model
	follow   bool
	logsSess *core.AgentSession

	// detail view (scrollable: long commands, big envs, short terminals)
	detailVP viewport.Model

	// confirm prompt
	confirmVerb string // "close" | "kill" | "rm-def"
	confirmSess *core.AgentSession
	confirmDef  *core.AgentDefinition
}

func newModel(deps Deps) *model {
	input := textinput.New()
	input.Prompt = ":"
	input.CharLimit = 512
	return &model{deps: deps, input: input}
}

// --- messages ---

type tickMsg time.Time

type refreshMsg struct {
	seq        uint64
	selectedID string
	sessions   []*core.AgentSession
	preview    string
	previewOK  bool // capture ran, even if the screen is blank
	logs       string
	logsOK     bool
	err        error
}

type defsMsg struct {
	defs []*core.AgentDefinition
	err  error
}

// defDeletedMsg carries the outcome of a picker delete plus the
// remaining definitions, so the picker can refresh in place.
type defDeletedMsg struct {
	name string
	defs []*core.AgentDefinition
	err  error
}

type flashMsg struct {
	text string
	err  error
}

type attachDoneMsg struct{ err error }

func (m *model) Init() tea.Cmd {
	return tea.Batch(m.refreshCmd(), m.tickCmd())
}

func (m *model) tickCmd() tea.Cmd {
	return tea.Tick(m.deps.Config.TUI.Tick.D(), func(t time.Time) tea.Msg { return tickMsg(t) })
}

// refreshCmd does one reconciliation pass and gathers everything the
// current view needs. Runs off the UI goroutine.
func (m *model) refreshCmd() tea.Cmd {
	m.refreshSeq++
	seq := m.refreshSeq
	deps := m.deps
	selectedID := m.selectedID
	previewW, previewH := m.previewArea()
	wantLogs := ""
	if m.mode == modeLogs && m.logsSess != nil {
		wantLogs = m.logsSess.LogPath
	}
	return func() tea.Msg {
		msg := refreshMsg{seq: seq, selectedID: selectedID}
		if err := deps.Manager.Reconcile(); err != nil {
			msg.err = err
			return msg
		}
		sessions, err := deps.Store.ListSessions(false)
		if err != nil {
			msg.err = err
			return msg
		}
		msg.sessions = sessions
		for _, s := range sessions {
			if s.ID != selectedID {
				continue
			}
			// Match the backend view to the preview box so the
			// harness TUI renders for exactly this geometry — the
			// preview then shows what attach would show.
			if resized, err := deps.Backend.SyncSize(s.ID, previewW, previewH); err == nil && resized {
				time.Sleep(redrawWait) // let the TUI redraw
			}
			if screen, err := deps.Backend.CapturePane(s.ID, 0); err == nil {
				msg.preview = screen
				msg.previewOK = true
			}
			break
		}
		if wantLogs != "" {
			if logs, err := core.ReadLogTail(wantLogs, logTailBytes); err == nil {
				msg.logs = logs
				msg.logsOK = true
			}
		}
		return msg
	}
}

func (m *model) selected() *core.AgentSession {
	if m.cursor >= 0 && m.cursor < len(m.sessions) {
		return m.sessions[m.cursor]
	}
	return nil
}

func (m *model) setFlash(text string) {
	m.flash = strings.TrimSpace(text)
	m.flashExpiry = time.Now().Add(flashTTL)
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.applyResize(msg)
		return m, nil
	case tickMsg:
		return m, tea.Batch(m.refreshCmd(), m.tickCmd())
	case refreshMsg:
		m.applyRefresh(msg)
		return m, nil
	case defsMsg:
		m.applyDefs(msg)
		return m, nil
	case defDeletedMsg:
		return m, m.applyDefDeleted(msg)
	case flashMsg:
		if msg.err != nil {
			m.setFlash("error: " + msg.err.Error())
		} else {
			m.setFlash(msg.text)
		}
		return m, m.refreshCmd()
	case attachDoneMsg:
		if msg.err != nil {
			m.setFlash("attach: " + msg.err.Error())
		}
		return m, m.refreshCmd()
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *model) applyResize(msg tea.WindowSizeMsg) {
	m.width, m.height = msg.Width, msg.Height
	// Logs render inside a bordered panel.
	m.logsVP.Width = max(1, msg.Width-panelChromeCols)
	m.logsVP.Height = max(1, msg.Height-logsChromeRows)
	if m.mode == modeDetail {
		m.syncDetail() // re-wrap for the new width
	}
}

func (m *model) applyRefresh(msg refreshMsg) {
	if msg.seq != m.refreshSeq {
		return
	}
	if msg.err != nil {
		m.setFlash("error: " + msg.err.Error())
		return
	}
	m.sessions = msg.sessions
	m.pinSelection()
	if msg.selectedID == m.selectedID && msg.previewOK {
		m.preview = msg.preview
	} else {
		m.preview = ""
	}
	if m.mode == modeLogs && msg.logsOK {
		atBottom := m.logsVP.AtBottom()
		m.logsVP.SetContent(msg.logs)
		if m.follow || atBottom {
			m.logsVP.GotoBottom()
		}
	}
	if m.mode == modeDetail {
		if m.selected() == nil {
			m.mode = modeList // the session went away mid-detail
		} else {
			m.syncDetail() // keep status/log tail live; scroll stays put
		}
	}
}

// pinSelection keeps the cursor on the same session across refreshes,
// clamping it when the selected session disappeared.
func (m *model) pinSelection() {
	for i, s := range m.sessions {
		if s.ID == m.selectedID {
			m.cursor = i
			return
		}
	}
	if m.cursor >= len(m.sessions) {
		m.cursor = max(0, len(m.sessions)-1)
	}
	if s := m.selected(); s != nil {
		m.selectedID = s.ID
	} else {
		m.selectedID = ""
	}
}

func (m *model) applyDefs(msg defsMsg) {
	if msg.err != nil {
		m.setFlash("error: " + msg.err.Error())
		return
	}
	if len(msg.defs) == 0 {
		m.setFlash("no definitions yet — create one with :define <name> --harness ...")
		return
	}
	m.defs = msg.defs
	m.pickerCursor = 0
	m.mode = modePicker
}

func (m *model) applyDefDeleted(msg defDeletedMsg) tea.Cmd {
	if msg.err != nil {
		m.setFlash("error: " + msg.err.Error())
		m.mode = modeList
		return m.refreshCmd()
	}
	m.setFlash("removed definition " + msg.name)
	m.defs = msg.defs
	if len(m.defs) == 0 {
		m.mode = modeList // nothing left to pick from
		return nil
	}
	if m.pickerCursor >= len(m.defs) {
		m.pickerCursor = len(m.defs) - 1
	}
	m.mode = modePicker
	return nil
}

func (m *model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyCtrlC {
		return m, tea.Quit
	}
	switch m.mode {
	case modeCommand:
		return m.handleCommandKey(msg)
	case modeConfirm:
		return m.handleConfirmKey(msg)
	case modePicker:
		return m.handlePickerKey(msg)
	case modeLogs:
		return m.handleLogsKey(msg)
	case modeDetail:
		return m.handleDetailKey(msg)
	case modeHelp:
		switch msg.String() {
		case "q", keyEsc, keyEnter:
			m.mode = modeList
		}
		return m, nil
	default:
		return m.handleListKey(msg)
	}
}

func (m *model) handleListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "j", "down":
		return m, m.moveCursor(1)
	case "k", "up":
		return m, m.moveCursor(-1)
	case keyEnter:
		m.enterDetail()
	case "a":
		return m, m.attachSelected()
	case "o":
		return m, m.loadDefsCmd()
	case "s":
		return m, m.saveSelectedAsDef()
	case "x":
		m.confirmSelected(verbClose)
	case "X":
		m.confirmSelected(verbKill)
	case "l":
		m.enterLogs()
	case ":":
		return m, m.openCommandBar()
	case "?":
		m.mode = modeHelp
	}
	return m, nil
}

// moveCursor shifts the selection by delta and refreshes the preview;
// no-op (and no refresh) at either end of the list.
func (m *model) moveCursor(delta int) tea.Cmd {
	next := m.cursor + delta
	if next < 0 || next >= len(m.sessions) {
		return nil
	}
	m.cursor = next
	m.selectedID = m.sessions[next].ID
	m.preview = ""
	return m.refreshCmd()
}

func (m *model) enterDetail() {
	if m.selected() == nil {
		return
	}
	m.mode = modeDetail
	m.detailVP = viewport.New(max(1, m.width-panelChromeCols), max(1, m.height-detailChromeRows))
	m.syncDetail()
}

func (m *model) attachSelected() tea.Cmd {
	if s := m.selected(); s != nil && !s.Status.Terminal() {
		return m.attachCmd(s.ID)
	}
	return nil
}

func (m *model) loadDefsCmd() tea.Cmd {
	deps := m.deps
	return func() tea.Msg {
		defs, err := deps.Store.ListDefinitions()
		return defsMsg{defs: defs, err: err}
	}
}

// saveSelectedAsDef prefills the command bar with the equivalent
// `define ... --from` so it runs through the same path as the CLI and
// the name stays editable before you commit.
func (m *model) saveSelectedAsDef() tea.Cmd {
	s := m.selected()
	if s == nil {
		return nil
	}
	m.input.SetValue(fmt.Sprintf("define %s --from %s", s.Name, s.ID))
	m.input.CursorEnd()
	m.input.Focus()
	m.mode = modeCommand
	return textinput.Blink
}

func (m *model) confirmSelected(verb string) {
	if s := m.selected(); s != nil && !s.Status.Terminal() {
		m.confirmVerb, m.confirmSess = verb, s
		m.mode = modeConfirm
	}
}

func (m *model) enterLogs() {
	s := m.selected()
	if s == nil {
		return
	}
	m.logsSess = s
	m.follow = true
	m.logsVP = viewport.New(max(1, m.width-panelChromeCols), max(1, m.height-logsChromeRows))
	logs, _ := core.ReadLogTail(s.LogPath, logTailBytes)
	m.logsVP.SetContent(logs)
	m.logsVP.GotoBottom()
	m.mode = modeLogs
}

func (m *model) openCommandBar() tea.Cmd {
	m.input.SetValue("")
	m.input.Focus()
	m.mode = modeCommand
	return textinput.Blink
}

func (m *model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y": // X is shifted, so accept a still-held shift on the y
		if m.confirmVerb == verbRmDef {
			def := m.confirmDef
			m.confirmDef = nil
			store := m.deps.Store
			return m, func() tea.Msg {
				if err := store.DeleteDefinition(def.Name); err != nil {
					return defDeletedMsg{name: def.Name, err: err}
				}
				defs, err := store.ListDefinitions()
				return defDeletedMsg{name: def.Name, defs: defs, err: err}
			}
		}
		verb, sess := m.confirmVerb, m.confirmSess
		m.confirmSess = nil
		m.mode = modeList
		if sess == nil {
			return m, nil
		}
		sessionID := sess.ID
		manager := m.deps.Manager
		return m, func() tea.Msg {
			current, err := manager.Store.GetSession(sessionID)
			if err != nil {
				return flashMsg{err: err}
			}
			if current.Status.Terminal() {
				return flashMsg{text: fmt.Sprintf("session %s already %s", current.ID, current.Status)}
			}
			var operationErr error
			done := "closed"
			if verb == verbKill {
				operationErr = manager.Kill(current)
				done = "killed"
			} else {
				operationErr = manager.Close(current, 0)
			}
			return flashMsg{text: fmt.Sprintf("%s %s  %s", done, current.ID, current.Name), err: operationErr}
		}
	case "n", "N", keyEsc, "q":
		if m.confirmVerb == verbRmDef { // cancel returns to the picker, not the list
			m.confirmDef = nil
			m.mode = modePicker
			m.setFlash("delete canceled")
			return m, nil
		}
		m.mode = modeList
		m.setFlash(m.confirmVerb + " canceled")
	}
	return m, nil // other keys keep the prompt up
}

func (m *model) handlePickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", keyEsc:
		m.mode = modeList
	case "j", "down":
		if m.pickerCursor < len(m.defs)-1 {
			m.pickerCursor++
		}
	case "k", "up":
		if m.pickerCursor > 0 {
			m.pickerCursor--
		}
	case "d", "x":
		if len(m.defs) > 0 {
			m.confirmVerb = verbRmDef
			m.confirmDef = m.defs[m.pickerCursor]
			m.confirmSess = nil
			m.mode = modeConfirm
		}
	case keyEnter:
		def := m.defs[m.pickerCursor]
		m.mode = modeList
		manager := m.deps.Manager
		return m, func() tea.Msg {
			sess, err := manager.Open(core.OpenRequest{Definition: def.Name})
			if err != nil {
				return flashMsg{err: err}
			}
			return flashMsg{text: fmt.Sprintf("opened %s  %s", sess.ID, sess.Name)}
		}
	}
	return m, nil
}

func (m *model) handleDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", keyEsc, keyEnter:
		m.mode = modeList
		return m, nil
	}
	var cmd tea.Cmd
	m.detailVP, cmd = m.detailVP.Update(msg)
	return m, cmd
}

func (m *model) handleLogsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", keyEsc:
		m.mode = modeList
		return m, nil
	case "f":
		m.follow = !m.follow
		if m.follow {
			m.logsVP.GotoBottom()
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.logsVP, cmd = m.logsVP.Update(msg)
	return m, cmd
}

func (m *model) handleCommandKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		m.mode = modeList
		m.input.Blur()
		return m, nil
	case tea.KeyEnter:
		line := strings.TrimSpace(m.input.Value())
		m.mode = modeList
		m.input.Blur()
		if line == "" {
			return m, nil
		}
		return m, m.execLineCmd(line)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// execLineCmd runs a command-bar line through the af Cobra root
// in-process. attach is special-cased into the TUI round-trip (an
// in-process attach would exec over the dashboard).
func (m *model) execLineCmd(line string) tea.Cmd {
	args := splitWords(line)
	if len(args) > 0 && args[0] == "af" {
		args = args[1:]
	}
	if len(args) == 0 {
		return nil
	}
	// attach takes exactly "attach <session>".
	const attachArgc = 2
	switch args[0] {
	case "attach":
		if len(args) != attachArgc {
			m.setFlash("usage: attach <session>")
			return nil
		}
		sess, err := m.deps.Manager.ResolveOne(args[1])
		if err != nil {
			m.setFlash("error: " + err.Error())
			return nil
		}
		if sess.Status.Terminal() {
			m.setFlash(fmt.Sprintf("session %s is %s", sess.ID, sess.Status))
			return nil
		}
		return m.attachCmd(sess.ID)
	case "dashboard":
		m.setFlash("already in the dashboard")
		return nil
	}
	if err := validateCommandBarArgs(args); err != nil {
		m.setFlash("error: " + err.Error())
		return nil
	}
	newRoot := m.deps.NewRoot
	return func() tea.Msg {
		root := newRoot()
		var buf bytes.Buffer
		root.SetOut(&buf)
		root.SetErr(&buf)
		root.SetArgs(args)
		err := root.Execute()
		out := strings.TrimSpace(buf.String())
		if err != nil {
			if out != "" {
				out += " · "
			}
			out += "error: " + err.Error()
		}
		if out == "" {
			out = "ok: " + line
		}
		return flashMsg{text: out}
	}
}

func validateCommandBarArgs(args []string) error {
	if len(args) == 0 {
		return core.Errf(core.ExitUsage, "empty command")
	}
	switch args[0] {
	case "status", "open", "send", "peek", "close", "kill", "rm", "prune",
		"define", "defs", "rm-def", "signal", "doctor", "version":
		return nil
	case commandLogs:
		for _, arg := range args[1:] {
			if arg == "-f" || arg == "--follow" || strings.HasPrefix(arg, "-f=") || strings.HasPrefix(arg, "--follow=") {
				return core.Errf(core.ExitUsage, "logs --follow cannot run in the dashboard command bar")
			}
		}
		return nil
	default:
		return core.Errf(core.ExitUsage,
			"%s cannot run in the dashboard command bar; run it from a terminal", args[0])
	}
}

// attachCmd releases the terminal, connects to the session, and
// re-initializes the dashboard when the user detaches.
func (m *model) attachCmd(id string) tea.Cmd {
	spec, err := m.deps.Backend.PrepareAttach(id)
	if err != nil {
		return func() tea.Msg { return attachDoneMsg{err} }
	}
	c := exec.CommandContext(context.Background(), spec.Path, spec.Argv[1:]...)
	c.Env = spec.Env
	return tea.ExecProcess(c, func(err error) tea.Msg { return attachDoneMsg{err} })
}
