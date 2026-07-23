// Package tui is the af dashboard: a btop-inspired live view of every
// managed session, with previews, a command bar, and attach round-trips.
package tui

import (
	"bytes"
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
	"agentfactory.sh/af/internal/tmux"
)

// Deps is everything the dashboard needs from the CLI layer. NewRoot
// builds a fresh af command tree so the command bar routes through the
// exact same Cobra root as the shell CLI (no subshell).
type Deps struct {
	Config  *config.Config
	Store   *core.Store
	Backend *tmux.Backend
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

type model struct {
	deps   Deps
	mode   mode
	width  int
	height int

	sessions   []*core.AgentSession
	cursor     int
	selectedID string
	preview    string

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
	confirmVerb string // "close" | "kill"
	confirmSess *core.AgentSession
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
	sessions  []*core.AgentSession
	preview   string
	previewOK bool // capture ran, even if the screen is blank
	logs      string
	logsOK    bool
	err       error
}

type defsMsg struct {
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
	deps := m.deps
	selectedID := m.selectedID
	previewW, previewH := m.previewArea()
	wantLogs := ""
	if m.mode == modeLogs && m.logsSess != nil {
		wantLogs = m.logsSess.LogPath
	}
	return func() tea.Msg {
		var msg refreshMsg
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
			// Match the tmux window to the preview box so the
			// harness TUI renders for exactly this geometry — the
			// preview then shows what attach would show.
			if resized, err := deps.Backend.SyncSize(s.ID, previewW, previewH); err == nil && resized {
				time.Sleep(120 * time.Millisecond) // let the TUI redraw
			}
			if screen, err := deps.Backend.CapturePane(s.ID, 0); err == nil {
				msg.preview = screen
				msg.previewOK = true
			}
			break
		}
		if wantLogs != "" {
			if logs, err := core.ReadLogTail(wantLogs, 256<<10); err == nil {
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
	m.flashExpiry = time.Now().Add(4 * time.Second)
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// Logs render inside a bordered panel: 4 cols / 3 rows of chrome.
		m.logsVP.Width = max(1, msg.Width-4)
		m.logsVP.Height = max(1, msg.Height-3)
		if m.mode == modeDetail {
			m.syncDetail() // re-wrap for the new width
		}
		return m, nil

	case tickMsg:
		return m, tea.Batch(m.refreshCmd(), m.tickCmd())

	case refreshMsg:
		if msg.err != nil {
			m.setFlash("error: " + msg.err.Error())
			return m, nil
		}
		m.sessions = msg.sessions
		if msg.previewOK {
			m.preview = msg.preview
		}
		// Keep the selection pinned to the same session across refreshes.
		found := false
		for i, s := range m.sessions {
			if s.ID == m.selectedID {
				m.cursor, found = i, true
				break
			}
		}
		if !found {
			if m.cursor >= len(m.sessions) {
				m.cursor = max(0, len(m.sessions)-1)
			}
			if s := m.selected(); s != nil {
				m.selectedID = s.ID
			}
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
		return m, nil

	case defsMsg:
		if msg.err != nil {
			m.setFlash("error: " + msg.err.Error())
			return m, nil
		}
		if len(msg.defs) == 0 {
			m.setFlash("no definitions yet — create one with :define <name> --harness ...")
			return m, nil
		}
		m.defs = msg.defs
		m.pickerCursor = 0
		m.mode = modePicker
		return m, nil

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
		case "q", "esc", "enter":
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
		if m.cursor < len(m.sessions)-1 {
			m.cursor++
			m.selectedID = m.sessions[m.cursor].ID
			m.preview = ""
			return m, m.refreshCmd()
		}
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
			m.selectedID = m.sessions[m.cursor].ID
			m.preview = ""
			return m, m.refreshCmd()
		}
	case "enter":
		if m.selected() != nil {
			m.mode = modeDetail
			m.detailVP = viewport.New(max(1, m.width-4), max(1, m.height-4))
			m.syncDetail()
		}
	case "a":
		if s := m.selected(); s != nil && !s.Status.Terminal() {
			return m, m.attachCmd(s.ID)
		}
	case "o":
		deps := m.deps
		return m, func() tea.Msg {
			defs, err := deps.Store.ListDefinitions()
			return defsMsg{defs: defs, err: err}
		}
	case "x":
		if s := m.selected(); s != nil && !s.Status.Terminal() {
			m.confirmVerb, m.confirmSess = "close", s
			m.mode = modeConfirm
		}
	case "X":
		if s := m.selected(); s != nil && !s.Status.Terminal() {
			m.confirmVerb, m.confirmSess = "kill", s
			m.mode = modeConfirm
		}
	case "l":
		if s := m.selected(); s != nil {
			m.logsSess = s
			m.follow = true
			m.logsVP = viewport.New(max(1, m.width-4), max(1, m.height-3))
			logs, _ := core.ReadLogTail(s.LogPath, 256<<10)
			m.logsVP.SetContent(logs)
			m.logsVP.GotoBottom()
			m.mode = modeLogs
		}
	case ":":
		m.input.SetValue("")
		m.input.Focus()
		m.mode = modeCommand
		return m, textinput.Blink
	case "?":
		m.mode = modeHelp
	}
	return m, nil
}

func (m *model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y": // X is shifted, so accept a still-held shift on the y
		verb, sess := m.confirmVerb, m.confirmSess
		m.mode = modeList
		if sess == nil {
			return m, nil
		}
		manager := m.deps.Manager
		return m, func() tea.Msg {
			var err error
			done := "closed"
			if verb == "kill" {
				err = manager.Kill(sess)
				done = "killed"
			} else {
				err = manager.Close(sess, 0)
			}
			return flashMsg{text: fmt.Sprintf("%s %s  %s", done, sess.ID, sess.Name), err: err}
		}
	case "n", "N", "esc", "q":
		m.mode = modeList
		m.setFlash(m.confirmVerb + " cancelled")
	}
	return m, nil // other keys keep the prompt up
}

func (m *model) handlePickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		m.mode = modeList
	case "j", "down":
		if m.pickerCursor < len(m.defs)-1 {
			m.pickerCursor++
		}
	case "k", "up":
		if m.pickerCursor > 0 {
			m.pickerCursor--
		}
	case "enter":
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
	case "q", "esc", "enter":
		m.mode = modeList
		return m, nil
	}
	var cmd tea.Cmd
	m.detailVP, cmd = m.detailVP.Update(msg)
	return m, cmd
}

func (m *model) handleLogsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
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
	switch args[0] {
	case "attach":
		if len(args) != 2 {
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

// attachCmd releases the terminal, runs tmux attach, and re-initializes
// the dashboard when the user detaches (§11 attach round-trip).
func (m *model) attachCmd(id string) tea.Cmd {
	argv := m.deps.Backend.AttachArgs(id)
	c := exec.Command(argv[0], argv[1:]...)
	// Strip $TMUX so attaching works when the dashboard itself runs
	// inside the user's tmux (tmux refuses nested attach otherwise;
	// detach from the nest with C-b C-b d).
	c.Env = tmux.AttachEnv()
	return tea.ExecProcess(c, func(err error) tea.Msg { return attachDoneMsg{err} })
}
