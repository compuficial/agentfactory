package tui

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"agentfactory.sh/af/internal/core"
)

// The palette leans on the 256-color cube (lipgloss degrades it on
// simpler terminals): cyan chrome, gray borders, one loud magenta
// reserved for awaiting-input.
var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("81"))
	accentStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("81"))
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("242"))
	headerStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("242")).Bold(true)
	borderStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).Background(lipgloss.Color("24"))
	flashStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	confirmStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("220"))
	awaitingText  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))

	statusStyles = map[core.Status]lipgloss.Style{
		core.StatusStarting: lipgloss.NewStyle().Foreground(lipgloss.Color("220")),
		core.StatusWorking:  lipgloss.NewStyle().Foreground(lipgloss.Color("42")),
		core.StatusIdle:     lipgloss.NewStyle().Foreground(lipgloss.Color("250")),
		// awaiting-input must be visually loud (§11, M4): the whole
		// row lights up, selected or not. done is green-quiet — task
		// complete is good news, not an interrupt.
		core.StatusAwaitingInput: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).Background(lipgloss.Color("125")),
		core.StatusDone:          lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42")),
		core.StatusExited:        lipgloss.NewStyle().Foreground(lipgloss.Color("242")),
		core.StatusFailed:        lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("196")),
	}

	statusOrder = []core.Status{
		core.StatusAwaitingInput, core.StatusDone, core.StatusWorking,
		core.StatusStarting, core.StatusIdle, core.StatusFailed, core.StatusExited,
	}
)

// rowStyle picks the style for a whole session row: awaiting-input
// always wins, then the selection highlight, then the status tint.
func rowStyle(s *core.AgentSession, selected bool) lipgloss.Style {
	if s.Status == core.StatusAwaitingInput {
		return statusStyles[s.Status]
	}
	if selected {
		return selectedStyle
	}
	if style, ok := statusStyles[s.Status]; ok {
		return style
	}
	return lipgloss.NewStyle()
}

// pad truncates or right-pads s to exactly w display cells. ANSI-aware:
// styled strings and wide runes measure by cell, not rune.
func pad(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if ansi.StringWidth(s) > w {
		return ansi.Truncate(s, w, "…")
	}
	return s + strings.Repeat(" ", w-ansi.StringWidth(s))
}

// panel draws a btop-style box with the title embedded in the top
// border. The box is exactly width × height cells (borders included);
// height <= 0 sizes the box to its content.
func panel(title string, lines []string, width, height int) string {
	cw := max(1, width-4)
	ch := len(lines)
	if height > 0 {
		ch = max(0, height-2)
	}
	t := " " + title + " "
	tw := ansi.StringWidth(t)
	if tw > max(0, width-3) {
		t = ansi.Truncate(t, max(0, width-3), "…")
		tw = ansi.StringWidth(t)
	}
	var b strings.Builder
	b.WriteString(borderStyle.Render("╭─") + t + borderStyle.Render(strings.Repeat("─", max(0, width-3-tw))+"╮") + "\n")
	for i := range ch {
		line := ""
		if i < len(lines) {
			line = lines[i]
		}
		b.WriteString(borderStyle.Render("│") + " " + pad(line, cw) + " " + borderStyle.Render("│") + "\n")
	}
	b.WriteString(borderStyle.Render("╰" + strings.Repeat("─", max(0, width-2)) + "╯"))
	return b.String()
}

// hints renders a footer line of key/description pairs.
func hints(kv ...string) string {
	parts := make([]string, 0, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		parts = append(parts, accentStyle.Render(kv[i])+" "+dimStyle.Render(kv[i+1]))
	}
	return strings.Join(parts, dimStyle.Render(" · "))
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func (m *model) View() string {
	if m.width == 0 {
		return "loading..."
	}
	switch m.mode {
	case modeDetail:
		return m.viewDetail()
	case modeLogs:
		return m.viewLogs()
	case modeHelp:
		return m.viewHelp()
	case modePicker:
		return m.viewPicker()
	default:
		return m.viewList()
	}
}

// layout splits the vertical space between the two panels (heights
// include their borders): the session list gets what its rows need
// (capped at 40% of the body), the preview panel gets the rest.
func (m *model) layout(footerH int) (listH, previewH int) {
	body := m.height - 1 - footerH // minus header line
	rows := max(1, len(m.sessions))
	listH = min(rows+3, max(6, body*2/5)) // rows + column header + borders
	listH = max(4, min(listH, body-4))
	previewH = max(0, body-listH)
	return listH, previewH
}

// previewArea is the preview panel's content geometry; the selected
// session's tmux window is synced to exactly this size. Assumes the
// usual one-line footer — a taller flash skews it for a tick or two,
// which is not worth rendering the footer twice to avoid.
func (m *model) previewArea() (w, h int) {
	_, previewH := m.layout(1)
	return max(1, m.width-4), max(1, previewH-2)
}

// viewHeader is the top line: brand, socket, live status counts.
func (m *model) viewHeader() string {
	h := titleStyle.Render(" AgentFactory ") + dimStyle.Render("socket "+m.deps.Config.Socket)
	if s := m.statusSummary(); s != "" {
		h += dimStyle.Render(" · ") + s
	}
	return h
}

// statusSummary renders per-status session counts ("2 working · 1 idle").
func (m *model) statusSummary() string {
	counts := map[core.Status]int{}
	for _, s := range m.sessions {
		counts[s.Status]++
	}
	var parts []string
	for _, st := range statusOrder {
		n := counts[st]
		if n == 0 {
			continue
		}
		style, ok := statusStyles[st]
		if st == core.StatusAwaitingInput {
			style = awaitingText // foreground-only variant for the header
		} else if !ok {
			style = dimStyle
		}
		parts = append(parts, style.Render(fmt.Sprintf("%d %s", n, st)))
	}
	return strings.Join(parts, dimStyle.Render(" · "))
}

func (m *model) viewList() string {
	footer := m.viewFooter()
	footerH := strings.Count(footer, "\n") + 1
	listH, previewH := m.layout(footerH)
	cw := max(1, m.width-4)

	var b strings.Builder
	b.WriteString(m.viewHeader() + "\n")

	listTitle := titleStyle.Render("sessions") + dimStyle.Render(fmt.Sprintf(" (%d)", len(m.sessions)))
	b.WriteString(panel(listTitle, m.sessionRows(cw, max(1, listH-3)), m.width, listH) + "\n")

	previewTitle := titleStyle.Render("preview")
	var previewLines []string
	if s := m.selected(); s == nil {
		previewLines = []string{dimStyle.Render("no session selected")}
	} else {
		previewTitle += dimStyle.Render(fmt.Sprintf(" · %s (%s) · %s %s",
			s.Name, s.ID, core.StatusLabel(s), core.Ago(s.LastActive)))
		previewLines = lastLines(m.preview, max(1, previewH-2))
	}
	b.WriteString(panel(previewTitle, previewLines, m.width, previewH) + "\n")

	b.WriteString(footer)
	return b.String()
}

// sessionRows renders the column header plus up to visible session
// rows, windowed around the cursor.
func (m *model) sessionRows(cw, visible int) []string {
	nameW, harnW, modelW, statusW, lastW, upW := 16, 12, 10, 15, 9, 7
	fixed := 2 + nameW + harnW + modelW + statusW + lastW + upW + 6
	workW := max(8, cw-fixed)
	lines := []string{headerStyle.Render("  " + pad("NAME", nameW) + " " + pad("HARNESS", harnW) + " " +
		pad("MODEL", modelW) + " " + pad("STATUS", statusW) + " " + pad("LAST", lastW) + " " +
		pad("UP", upW) + " " + pad("WORKDIR", workW))}
	if len(m.sessions) == 0 {
		return append(lines, dimStyle.Render("no sessions — press o to open from a definition, or : for a command"))
	}
	start := 0
	if m.cursor >= visible {
		start = m.cursor - visible + 1
	}
	for i := start; i < len(m.sessions) && i < start+visible; i++ {
		s := m.sessions[i]
		gutter := "  "
		if i == m.cursor {
			gutter = "▶ "
		}
		row := gutter + pad(s.Name, nameW) + " " + pad(s.Harness, harnW) + " " + pad(orDash(s.Model), modelW) + " " +
			pad(core.StatusLabel(s), statusW) + " " + pad(core.Ago(s.LastActive), lastW) + " " +
			pad(core.Uptime(s), upW) + " " + pad(core.TildePath(s.WorkDir), workW)
		lines = append(lines, rowStyle(s, i == m.cursor).Render(pad(row, cw)))
	}
	return lines
}

// lastLines returns the trailing n lines of s, ready for a panel body.
func lastLines(s string, n int) []string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

func (m *model) viewFooter() string {
	switch m.mode {
	case modeCommand:
		return m.input.View()
	case modeConfirm:
		if m.confirmSess != nil {
			return confirmStyle.Render(fmt.Sprintf("%s %s  %s? (y/n)", m.confirmVerb, m.confirmSess.ID, m.confirmSess.Name))
		}
	}
	if m.flash != "" && time.Now().Before(m.flashExpiry) {
		return flashStyle.Render(firstLines(m.flash, 5, m.width))
	}
	return hints("j/k", "move", "a", "attach", "o", "open", "x", "close", "X", "kill",
		"l", "logs", "enter", "detail", ":", "cmd", "?", "help", "q", "quit")
}

func firstLines(s string, n, width int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
		lines[n-1] += " …"
	}
	for i, line := range lines {
		if width > 0 && ansi.StringWidth(line) > width {
			lines[i] = ansi.Truncate(line, width-1, "…")
		}
	}
	return strings.Join(lines, "\n")
}

func (m *model) viewDetail() string {
	s := m.selected()
	if s == nil {
		return m.viewList() // Update leaves detail mode on the next refresh
	}
	title := titleStyle.Render(s.Name) + dimStyle.Render(" · "+s.ID)
	body := panel(title, strings.Split(m.detailVP.View(), "\n"), m.width, max(3, m.height-2))
	return m.viewHeader() + "\n" + body + "\n" + hints("j/k", "scroll", "q/esc", "back")
}

// syncDetail (re)builds the detail viewport for the selected session,
// keeping the scroll position across refreshes.
func (m *model) syncDetail() {
	s := m.selected()
	if s == nil {
		return
	}
	m.detailVP.Width = max(1, m.width-4)
	m.detailVP.Height = max(1, m.height-4)
	m.detailVP.SetContent(strings.Join(m.detailLines(s), "\n"))
}

// detailLines renders the detail page body. Long values (rendered
// commands, paths, env) soft-wrap into continuation lines under the
// value column instead of truncating; the viewport handles overflow.
func (m *model) detailLines(s *core.AgentSession) []string {
	const keyW = 13 // "%-12s" plus the separating space
	valW := max(20, m.width-4-keyW)
	var lines []string
	pair := func(k, v string) {
		parts := wrapPlain(v, valW)
		lines = append(lines, dimStyle.Render(fmt.Sprintf("%-12s", k))+" "+parts[0])
		for _, p := range parts[1:] {
			lines = append(lines, strings.Repeat(" ", keyW)+p)
		}
	}
	// Status is styled, so it skips the (plain-text) wrapper; it never
	// gets near valW anyway.
	lines = append(lines, dimStyle.Render(fmt.Sprintf("%-12s", "Status"))+" "+rowStyle(s, false).Render(core.StatusLabel(s)))
	if s.Definition != "" {
		pair("Definition", s.Definition)
	}
	pair("Harness", s.Harness)
	if s.Model != "" {
		pair("Model", s.Model)
	}
	pair("Command", s.Command)
	pair("WorkDir", core.TildePath(s.WorkDir))
	pair("PID", fmt.Sprintf("%d (pgid %d)", s.PID, s.PGID))
	pair("Service", fmt.Sprintf("%v", s.Service))
	pair("Log", core.TildePath(s.LogPath))
	pair("Started", s.StartedAt.Local().Format(time.RFC3339)+" · up "+core.Uptime(s))
	pair("LastActive", core.Ago(s.LastActive))
	if s.EndedAt != nil {
		pair("Ended", s.EndedAt.Local().Format(time.RFC3339))
	}
	for _, k := range sortedKeys(s.Metadata) {
		pair("Meta."+k, s.Metadata[k])
	}
	env := s.Env
	if len(env) == 0 {
		// Rows written by another af build may lack env; §7.4 still
		// guarantees the injected AF_* pair.
		env = map[string]string{"AF_SESSION_ID": s.ID, "AF_SESSION_NAME": s.Name}
	}
	lines = append(lines, "", headerStyle.Render("ENVIRONMENT"))
	for _, k := range sortedKeys(env) {
		pair(k, maskEnvValue(k, env[k]))
	}
	lines = append(lines, "", headerStyle.Render("last 20 log lines"))
	logs, _ := core.ReadLogTail(s.LogPath, 64<<10)
	tail := strings.TrimRight(string(core.TailLines([]byte(logs), 20)), "\n")
	for line := range strings.SplitSeq(tail, "\n") {
		lines = append(lines, wrapPlain(line, max(20, m.width-4))...)
	}
	return lines
}

// wrapPlain splits unstyled text into chunks of at most w runes; the
// caller indents continuation lines. Always returns at least one entry.
func wrapPlain(s string, w int) []string {
	runes := []rune(s)
	if w < 8 || len(runes) <= w {
		return []string{s}
	}
	var parts []string
	for start := 0; start < len(runes); start += w {
		parts = append(parts, string(runes[start:min(start+w, len(runes))]))
	}
	return parts
}

// maskEnvValue hides secrets in the detail view (§11 requires
// KEY/TOKEN/SECRET; the rest are the same idea).
func maskEnvValue(key, value string) string {
	upper := strings.ToUpper(key)
	for _, needle := range []string{"KEY", "TOKEN", "SECRET", "PASSWORD", "PASSWD", "CREDENTIAL"} {
		if strings.Contains(upper, needle) {
			return "••••••"
		}
	}
	return value
}

func sortedKeys(m map[string]string) []string {
	return slices.Sorted(maps.Keys(m))
}

func (m *model) viewLogs() string {
	name, id := "", ""
	if m.logsSess != nil {
		name, id = m.logsSess.Name, m.logsSess.ID
	}
	followLabel := "follow off"
	if m.follow {
		followLabel = "follow on"
	}
	title := titleStyle.Render("logs") + dimStyle.Render(fmt.Sprintf(" · %s (%s) · ", name, id)) + accentStyle.Render(followLabel)
	body := panel(title, strings.Split(m.logsVP.View(), "\n"), m.width, max(3, m.height-1))
	return body + "\n" + hints("j/k", "scroll", "f", "toggle follow", "q/esc", "back")
}

func (m *model) viewPicker() string {
	nameW, harnW, modelW := 20, 12, 12
	cw := max(1, m.width-4)
	lines := []string{headerStyle.Render("  " + pad("NAME", nameW) + " " + pad("HARNESS", harnW) + " " +
		pad("MODEL", modelW) + " WORKDIR")}
	for i, d := range m.defs {
		gutter := "  "
		if i == m.pickerCursor {
			gutter = "▶ "
		}
		row := gutter + pad(d.Name, nameW) + " " + pad(d.Harness, harnW) + " " +
			pad(orDash(d.Model), modelW) + " " + core.TildePath(d.WorkDir)
		if i == m.pickerCursor {
			row = selectedStyle.Render(pad(row, cw))
		}
		lines = append(lines, row)
	}
	return m.viewHeader() + "\n" + panel(titleStyle.Render("open from definition"), lines, m.width, 0) + "\n" +
		hints("enter", "open", "j/k", "move", "esc", "cancel")
}

func (m *model) viewHelp() string {
	keys := [][2]string{
		{"j / k, arrows", "move selection"},
		{"enter", "session detail (metadata, env, recent log)"},
		{"a", "attach to the selected session (detach: C-b d; inside tmux: C-b C-b d)"},
		{"o", "open a session from a definition"},
		{"x", "close the selected session (graceful, y/n)"},
		{"X", "kill the selected session (immediate, y/n)"},
		{"l", "logs view (f toggles follow)"},
		{":", "command bar — any af command, e.g. :status --all"},
		{"?", "this help"},
		{"q", "quit"},
	}
	lines := []string{""}
	for _, k := range keys {
		lines = append(lines, "  "+accentStyle.Render(pad(k[0], 15))+" "+k[1])
	}
	lines = append(lines, "",
		dimStyle.Render("  the preview pane mirrors the selected session's live screen"),
		dimStyle.Render("  the dashboard never types into sessions; attach or use :send"), "")
	return m.viewHeader() + "\n" + panel(titleStyle.Render("help"), lines, m.width, 0) + "\n" + hints("q/esc", "back")
}
