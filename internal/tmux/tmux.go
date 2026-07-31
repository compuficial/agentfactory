// Package tmux implements core.SessionBackend by shelling out to the
// tmux binary on a dedicated socket (tmux -L <socket>). The user's
// personal tmux server and .tmux.conf are never touched.
package tmux

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"agentfactory.sh/af/internal/core"
)

// MinVersion is the oldest supported tmux (needs new-session -e and
// pane_dead_status).
const MinVersion = "3.2"

// setOption is the tmux subcommand used to apply the locked server config.
const setOption = "set-option"

const (
	nullConfig = "/dev/null"
	tmuxBinary = "tmux"
)

// gatePerm: the payload-gate file is same-user IPC; nothing else reads it.
const gatePerm = 0o600

// Backend is the tmux implementation of core.SessionBackend.
type Backend struct {
	Socket    string
	SendDelay time.Duration
}

// New returns a Backend on the given socket. sendDelay is the gap
// between literal text and Enter in SendKeys.
func New(socket string, sendDelay time.Duration) *Backend {
	return &Backend{Socket: socket, SendDelay: sendDelay}
}

// SetSendDelay changes the gap between literal input and Enter.
func (b *Backend) SetSendDelay(delay time.Duration) { b.SendDelay = delay }

func sessionName(id string) string { return "af-" + id }

// target returns an exact-match tmux session target (= prefix disables
// tmux's name-prefix matching).
func target(id string) string { return "=" + sessionName(id) }

// paneTarget returns an exact-match target for pane-taking commands
// (pipe-pane, capture-pane, send-keys, list-panes): the trailing colon
// makes tmux parse it as session:window instead of a bare pane name.
func paneTarget(id string) string { return "=" + sessionName(id) + ":" }

// cmd builds a tmux invocation on the dedicated socket. -f /dev/null
// guarantees the user's .tmux.conf is never sourced if this call
// happens to start the server.
func (b *Backend) cmd(args ...string) *exec.Cmd {
	full := append([]string{"-L", b.Socket, "-f", nullConfig}, args...)
	return exec.CommandContext(context.Background(), tmuxBinary, full...)
}

func (b *Backend) run(args ...string) (string, error) {
	var stderr bytes.Buffer
	c := b.cmd(args...)
	c.Stderr = &stderr
	out, err := c.Output()
	if err != nil {
		return string(out), fmt.Errorf("tmux %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

// CheckTmux verifies the tmux binary exists and is >= MinVersion.
// Returns the version string. Failures are exit-4 environment errors.
func CheckTmux() (string, error) {
	out, err := exec.CommandContext(context.Background(), tmuxBinary, "-V").Output()
	if err != nil {
		return "", core.Errf(core.ExitEnv, "tmux not found on PATH (af requires tmux >= %s): %v", MinVersion, err)
	}
	version := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(out)), "tmux"))
	if !versionAtLeast(version, MinVersion) {
		return version, core.Errf(core.ExitEnv, "tmux %s is too old; af requires >= %s", version, MinVersion)
	}
	return version, nil
}

var versionRe = regexp.MustCompile(`(\d+)\.(\d+)`)

// versionAtLeast compares tmux version strings like "3.6b", "3.2a",
// "next-3.4". Letters break numeric ties upward, so numeric compare
// of major.minor is sufficient.
func versionAtLeast(have, want string) bool {
	hm := versionRe.FindStringSubmatch(have)
	wm := versionRe.FindStringSubmatch(want)
	if hm == nil {
		// "next-x.y" trims to the match anyway; no digits at all =
		// unknown build, let it through rather than block on parsing.
		return true
	}
	hMaj, _ := strconv.Atoi(hm[1])
	hMin, _ := strconv.Atoi(hm[2])
	wMaj, _ := strconv.Atoi(wm[1])
	wMin, _ := strconv.Atoi(wm[2])
	return hMaj > wMaj || (hMaj == wMaj && hMin >= wMin)
}

// EnsureServer starts the af tmux server (if needed) and applies the
// locked configuration from Appendix B. Everything runs as one chained
// tmux invocation: a fresh server with the default exit-empty=on would
// otherwise quit between separate calls (it has zero sessions).
func (b *Backend) EnsureServer() error {
	args := []string{"start-server"}
	for _, opt := range [][]string{
		{setOption, "-s", "exit-empty", "off"},    // server survives zero sessions
		{setOption, "-g", "remain-on-exit", "on"}, // dead panes persist for exit-code harvest
		{setOption, "-g", "status", "off"},
		{setOption, "-g", "mouse", "on"}, // forward wheel/scroll to the agent TUI (it owns its own transcript scrollback)
		{setOption, "-g", "default-terminal", "tmux-256color"},
		{setOption, "-g", "history-limit", "50000"},
		{setOption, "-g", "default-shell", "/bin/sh"}, // payloads run via `sh -c`, not the user's login shell
	} {
		args = append(args, ";")
		args = append(args, opt...)
	}
	if _, err := b.run(args...); err != nil {
		return core.Errf(core.ExitEnv, "start tmux server on socket %q: %v", b.Socket, err)
	}
	return nil
}

// shellQuote single-quotes s for use inside a shell command line.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Create starts a detached session running the payload in its workdir,
// attaches pipe-pane logging, and fills in sess.PID / sess.PGID. The
// payload is gated on a wait-for channel until the log pipe is
// attached: pipe-pane can only be added after the pane exists, so an
// ungated payload races it — output emitted first (a fixture's "ready"
// line, a fast-failing command's error) would be lost forever, and an
// already-exited pane makes pipe-pane fail outright. exec keeps the
// pane PID (and its exit status) belonging to the payload shell.
func (b *Backend) Create(sess *core.AgentSession) error {
	if err := b.EnsureServer(); err != nil {
		return err
	}
	args := []string{"new-session", "-d", "-s", sessionName(sess.ID), "-c", sess.WorkDir}
	for _, k := range slices.Sorted(maps.Keys(sess.Env)) {
		args = append(args, "-e", k+"="+sess.Env[k])
	}
	// A single trailing arg: tmux runs it via default-shell, which the
	// locked server config pins to /bin/sh — so rendered commands get
	// `sh -c` semantics (pipes, quoting, K=V prefixes; Appendix B)
	// even when the user's login shell is fish/csh.
	// A file gate rather than tmux wait-for: a wait-for signal with no
	// waiter yet registered is silently lost (the payload would then
	// block forever), while a file existing is a level, not an edge —
	// whichever side arrives first, the gate opens exactly once.
	gate := sess.LogPath + ".gate"
	args = append(args, fmt.Sprintf("until [ -e %s ]; do sleep 0.01; done; rm -f %s; exec /bin/sh -c %s",
		shellQuote(gate), shellQuote(gate), shellQuote(sess.Command)))
	if out, err := b.cmd(args...).CombinedOutput(); err != nil {
		return core.Errf(core.ExitRuntime, "tmux new-session: %v: %s", err, strings.TrimSpace(string(out)))
	}
	setupComplete := false
	defer func() {
		if setupComplete {
			return
		}
		_ = b.Kill(sess.ID)
		_ = os.Remove(gate)
	}()
	if _, err := b.run("pipe-pane", "-t", paneTarget(sess.ID), "-o", "cat >> "+shellQuote(sess.LogPath)); err != nil {
		return core.Errf(core.ExitRuntime, "attach log pipe: %v", err)
	}
	if err := os.WriteFile(gate, nil, gatePerm); err != nil {
		return core.Errf(core.ExitRuntime, "release payload gate: %v", err)
	}
	out, err := b.run("list-panes", "-t", paneTarget(sess.ID), "-F", "#{pane_pid}")
	if err != nil {
		return core.Errf(core.ExitRuntime, "read pane pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return core.Errf(core.ExitRuntime, "parse pane pid %q: %v", strings.TrimSpace(out), err)
	}
	sess.PID = pid
	if pgid, err := syscall.Getpgid(pid); err == nil {
		sess.PGID = pgid
	} else {
		sess.PGID = pid // payload may have exited already; group == pid for session leaders
	}
	setupComplete = true
	return nil
}

// attachArgv restores automatic window sizing (dashboard previews set
// it to manual via resize-window) before attaching, so the client's
// real terminal size always wins on attach.
func (b *Backend) attachArgv(id string) []string {
	return []string{
		tmuxBinary, "-L", b.Socket, "-f", nullConfig,
		"set-option", "-w", "-t", paneTarget(id), "window-size", "latest", ";",
		"attach-session", "-t", target(id),
	}
}

// AttachEnv is the environment for attach children: the current
// process env minus tmux's own markers (TMUX, TMUX_PANE). With TMUX
// set, tmux refuses to attach ("sessions should be nested with care");
// stripping it lets the nested attach work. The inner session then owns
// the default prefix, so detaching from inside another tmux takes
// C-b twice then d — the outer tmux forwards the doubled prefix inward.
func AttachEnv() []string {
	env := os.Environ()
	out := env[:0:0]
	for _, kv := range env {
		if strings.HasPrefix(kv, "TMUX=") || strings.HasPrefix(kv, "TMUX_PANE=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// Attach replaces the current process with tmux attach.
func (b *Backend) Attach(id string) error {
	spec, err := b.PrepareAttach(id)
	if err != nil {
		return err
	}
	return syscall.Exec(spec.Path, spec.Argv, spec.Env)
}

// PrepareAttach returns the process specification for a fork/exec attach.
func (b *Backend) PrepareAttach(id string) (core.AttachSpec, error) {
	path, err := exec.LookPath(tmuxBinary)
	if err != nil {
		return core.AttachSpec{}, core.Errf(core.ExitEnv, "tmux not found: %v", err)
	}
	return core.AttachSpec{Path: path, Argv: b.attachArgv(id), Env: AttachEnv()}, nil
}

// SyncSize resizes a session's window to w x h so its full-screen TUI
// renders exactly what a preview of that size will show. No-op when a
// client is attached (their terminal owns the size) or when the size
// already matches. Returns true if a resize happened.
func (b *Backend) SyncSize(id string, w, h int) (bool, error) {
	if w <= 0 || h <= 0 {
		return false, nil
	}
	out, err := b.run("display-message", "-p", "-t", paneTarget(id),
		"#{window_width} #{window_height} #{session_attached}")
	if err != nil {
		return false, core.Errf(core.ExitRuntime, "read window size: %v", err)
	}
	var curW, curH, attached int
	if _, err := fmt.Sscanf(strings.TrimSpace(out), "%d %d %d", &curW, &curH, &attached); err != nil {
		return false, core.Errf(core.ExitRuntime, "parse window size %q: %v", out, err)
	}
	if attached > 0 || (curW == w && curH == h) {
		return false, nil
	}
	if _, err := b.run("resize-window", "-t", paneTarget(id),
		"-x", strconv.Itoa(w), "-y", strconv.Itoa(h)); err != nil {
		return false, core.Errf(core.ExitRuntime, "resize window: %v", err)
	}
	return true, nil
}

// CapturePane returns the rendered visible screen; lines>0 keeps only
// the trailing N lines.
func (b *Backend) CapturePane(id string, lines int) (string, error) {
	out, err := b.run("capture-pane", "-p", "-t", paneTarget(id))
	if err != nil {
		return "", core.Errf(core.ExitRuntime, "capture pane: %v", err)
	}
	if lines > 0 {
		all := strings.Split(strings.TrimRight(out, "\n"), "\n")
		if len(all) > lines {
			all = all[len(all)-lines:]
		}
		return strings.Join(all, "\n") + "\n", nil
	}
	return out, nil
}

// SendKeys sends input literally, then (after SendDelay) Enter. The
// delay exists because some harnesses drop input arriving mid-render.
func (b *Backend) SendKeys(id string, input string, enter bool) error {
	if input != "" {
		if _, err := b.run("send-keys", "-t", paneTarget(id), "-l", "--", input); err != nil {
			return core.Errf(core.ExitRuntime, "send keys: %v", err)
		}
	}
	if enter {
		time.Sleep(b.SendDelay)
		if _, err := b.run("send-keys", "-t", paneTarget(id), "Enter"); err != nil {
			return core.Errf(core.ExitRuntime, "send enter: %v", err)
		}
	}
	return nil
}

// IsAlive reports whether the tmux session exists (dead-pane sessions
// count as existing until harvested).
func (b *Backend) IsAlive(id string) (bool, error) {
	out, err := b.cmd("has-session", "-t", target(id)).CombinedOutput()
	if err == nil {
		return true, nil
	}
	exitError := &exec.ExitError{}
	if errors.As(err, &exitError) && exitError.ExitCode() == 1 {
		return false, nil // missing session or no server
	}
	return false, core.Errf(core.ExitEnv, "tmux has-session: %v: %s", err, strings.TrimSpace(string(out)))
}

// DeadStatus reads pane_dead / pane_dead_status. exitCode is -1 when
// the payload died without a recorded exit status (killed by signal).
func (b *Backend) DeadStatus(id string) (bool, int, error) {
	out, err := b.run("list-panes", "-t", paneTarget(id), "-F", "#{pane_dead}\t#{pane_dead_status}")
	if err != nil {
		return false, 0, core.Errf(core.ExitRuntime, "read pane status: %v", err)
	}
	const deadPaneFields = 2 // pane_dead \t pane_dead_status
	fields := strings.SplitN(strings.TrimSpace(out), "\t", deadPaneFields)
	if fields[0] != "1" {
		return false, 0, nil
	}
	code := -1
	if len(fields) == deadPaneFields {
		if n, err := strconv.Atoi(strings.TrimSpace(fields[1])); err == nil {
			code = n
		}
	}
	return true, code, nil
}

// Kill destroys the tmux session (not the process group; core handles
// signaling before calling this).
func (b *Backend) Kill(id string) error {
	if out, err := b.run("kill-session", "-t", target(id)); err != nil {
		if alive, _ := b.IsAlive(id); !alive {
			return nil // already gone
		}
		return core.Errf(core.ExitRuntime, "kill session: %v: %s", err, strings.TrimSpace(out))
	}
	return nil
}

var _ core.SessionBackend = (*Backend)(nil)
