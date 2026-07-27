package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Manager wires the store, backend, and resolved settings into the
// session operations behind every CLI/TUI command.
type Manager struct {
	Store         *Store
	Backend       SessionBackend
	Harnesses     HarnessSet
	DataDir       string
	IdleThreshold time.Duration
	CloseTimeout  time.Duration
	// Detect holds compiled T1.5 rules per harness (nil = detection off).
	Detect map[string]*CompiledDetect
	// Signals holds the T1.75 terminal-signal config (nil = tier off).
	Signals *CompiledSignals
}

func now() time.Time { return time.Now().UTC() }

// Reconcile runs one reconciliation pass (§10.1).
func (m *Manager) Reconcile() error {
	return Reconcile(m.Store, m.Backend, m.IdleThreshold, m.Detect, m.Signals, now())
}

// OpenRequest carries af open's positional arg and flags.
type OpenRequest struct {
	Definition string
	Name       string
	Harness    string
	Model      string
	WorkDir    string
	Env        map[string]string
	Cmd        string
	Service    bool
	// ExtraArgs are appended verbatim to the rendered command — the
	// frictionless launcher's passthrough (`af claude --model opus`).
	ExtraArgs []string
}

// ResolveWorkDir expands ~, makes the path absolute, and verifies it's
// an existing directory. Empty means the current working directory.
func ResolveWorkDir(dir string) (string, error) {
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", Errf(ExitRuntime, "resolve cwd: %v", err)
		}
		dir = cwd
	}
	abs, err := filepath.Abs(ExpandHome(dir))
	if err != nil {
		return "", Errf(ExitRuntime, "resolve workdir: %v", err)
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return "", Errf(ExitRuntime, "workdir %s does not exist or is not a directory", abs)
	}
	return abs, nil
}

// LiveMatch returns the most-recently-active live (non-terminal,
// non-service) session with the given harness and workdir, or nil —
// resume-or-start's lookup for the frictionless launcher.
func (m *Manager) LiveMatch(harness, workDir string) (*AgentSession, error) {
	sessions, err := m.Store.ListSessions(false)
	if err != nil {
		return nil, err
	}
	var best *AgentSession
	for _, s := range sessions {
		if s.Service || s.Harness != harness || s.WorkDir != workDir {
			continue
		}
		if best == nil || s.LastActive.After(best.LastActive) {
			best = s
		}
	}
	return best, nil
}

// Open resolves definition + overrides, validates, and starts a session
// (§8.2 af open, Appendix A lifecycle).
func (m *Manager) Open(req OpenRequest) (*AgentSession, error) {
	def := AgentDefinition{Env: map[string]string{}, Config: map[string]string{}}
	if req.Definition != "" {
		loaded, err := m.Store.GetDefinition(req.Definition)
		if err != nil {
			return nil, err
		}
		def = *loaded
		if def.Env == nil {
			def.Env = map[string]string{}
		}
		if def.Config == nil {
			def.Config = map[string]string{}
		}
	}

	// Flag overrides.
	if req.Cmd != "" {
		def.Harness = "custom"
		def.Config["cmd"] = req.Cmd
	}
	if req.Harness != "" {
		def.Harness = req.Harness
	}
	if req.Model != "" {
		def.Model = req.Model
	}
	if req.WorkDir != "" {
		def.WorkDir = req.WorkDir
	}
	def.Service = def.Service || req.Service

	if def.Harness == "" {
		return nil, Errf(ExitUsage, "no harness: pass a definition, --harness, or --cmd")
	}
	harness, err := m.Harnesses.Resolve(def.Harness)
	if err != nil {
		return nil, err
	}
	if def.Harness == "custom" && strings.TrimSpace(def.Config["cmd"]) == "" {
		return nil, Errf(ExitRuntime, "custom harness requires a command (--cmd or config cmd)")
	}

	// WorkDir: default cwd; resolved to absolute; must exist.
	workDir, err := ResolveWorkDir(def.WorkDir)
	if err != nil {
		return nil, err
	}
	def.WorkDir = workDir

	// Harness wiring files (hook configs, notify scripts — pure data)
	// must exist before the payload launches; the template reaches them
	// via {{.FilesDir}}.
	filesDir, err := MaterializeFiles(harness, m.DataDir)
	if err != nil {
		return nil, err
	}
	command, err := RenderCommand(harness, def, filesDir)
	if err != nil {
		return nil, err
	}
	if len(req.ExtraArgs) > 0 {
		command += " " + QuoteArgs(req.ExtraArgs)
	}

	// Name: --name > definition name > harness name; suffix on live collision.
	base := req.Name
	if base == "" {
		base = req.Definition
	}
	if base == "" {
		base = def.Harness
	}
	live, err := m.Store.ListSessions(false)
	if err != nil {
		return nil, err
	}
	name := SuffixName(base, func(candidate string) bool {
		for _, s := range live {
			if s.Name == candidate {
				return true
			}
		}
		return false
	})

	id, err := m.newSessionID()
	if err != nil {
		return nil, err
	}

	logDir := filepath.Join(m.DataDir, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, Errf(ExitRuntime, "create log dir: %v", err)
	}
	logPath := filepath.Join(logDir, id+".log")

	env := MergeEnv(harness.Env, def.Env, req.Env)
	env[EnvSessionID] = id
	env[EnvSessionName] = name

	startedAt := now()
	sess := &AgentSession{
		ID:         id,
		Name:       name,
		Definition: req.Definition,
		Harness:    def.Harness,
		Model:      def.Model,
		Command:    command,
		WorkDir:    workDir,
		Status:     StatusStarting,
		LogPath:    logPath,
		Service:    def.Service,
		StartedAt:  startedAt,
		LastActive: startedAt,
		Metadata:   map[string]string{},
		Env:        env,
	}
	if err := m.Store.InsertSession(sess); err != nil {
		return nil, err
	}
	if err := m.Backend.Create(sess); err != nil {
		sess.markFailed(now())
		_ = m.Store.UpdateSession(sess)
		return nil, err
	}
	if err := m.Store.UpdateSession(sess); err != nil {
		return nil, err
	}
	return sess, nil
}

func (m *Manager) newSessionID() (string, error) {
	for range 10 {
		id, err := NewID()
		if err != nil {
			return "", err
		}
		_, err = m.Store.GetSession(id)
		if ExitCode(err) == ExitNotFound {
			return id, nil
		}
		if err != nil {
			return "", Errf(ExitRuntime, "id collision check: %v", err)
		}
	}
	return "", Errf(ExitRuntime, "could not generate a unique session id")
}

// ResolveOne maps an ID-or-name reference to exactly one session.
func (m *Manager) ResolveOne(ref string) (*AgentSession, error) {
	matches, err := m.Store.FindSessions(ref)
	if err != nil {
		return nil, err
	}
	switch len(matches) {
	case 0:
		return nil, Errf(ExitNotFound, "session %q not found", ref)
	case 1:
		return matches[0], nil
	default:
		var ids []string
		for _, s := range matches {
			ids = append(ids, fmt.Sprintf("%s (%s)", s.ID, s.Status))
		}
		return nil, Errf(ExitRuntime, "name %q is ambiguous, use an ID: %s", ref, strings.Join(ids, ", "))
	}
}

// Kill immediately SIGKILLs the whole process group, harvests the exit,
// and destroys the tmux session.
func (m *Manager) Kill(sess *AgentSession) error {
	if sess.Status.Terminal() {
		return nil
	}
	signalGroup(sess, syscall.SIGKILL)
	return m.harvest(sess)
}

// signalGroup signals the payload's process group; ESRCH (already
// gone) is not an error.
func signalGroup(sess *AgentSession, sig syscall.Signal) {
	if sess.PGID > 0 {
		_ = syscall.Kill(-sess.PGID, sig)
	}
}

// Close gracefully stops a session: harness QuitKeys (skipped for
// service sessions) -> wait 2s -> SIGTERM to -PGID -> wait timeout ->
// SIGKILL to -PGID -> harvest (§8.2 af close).
func (m *Manager) Close(sess *AgentSession, timeout time.Duration) error {
	if sess.Status.Terminal() {
		return nil
	}
	if timeout <= 0 {
		timeout = m.CloseTimeout
	}
	if !sess.Service {
		// Unknown harness (config changed since open) degrades to signal-only.
		if harness, err := m.Harnesses.Resolve(sess.Harness); err == nil && len(harness.QuitKeys) > 0 {
			for _, qk := range harness.QuitKeys {
				if err := m.Backend.SendKeys(sess.ID, qk, true); err != nil {
					break // pane may already be gone; fall through to signaling
				}
			}
			if m.waitDead(sess, 2*time.Second) {
				return m.harvest(sess)
			}
		}
	}
	signalGroup(sess, syscall.SIGTERM)
	if m.waitDead(sess, timeout) {
		return m.harvest(sess)
	}
	signalGroup(sess, syscall.SIGKILL)
	return m.harvest(sess)
}

// waitDead polls until the pane reports dead (or the session is gone)
// or the timeout elapses.
func (m *Manager) waitDead(sess *AgentSession, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		alive, err := m.Backend.IsAlive(sess.ID)
		if err == nil && !alive {
			return true
		}
		if dead, _, err := m.Backend.DeadStatus(sess.ID); err == nil && dead {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// Remove deletes a terminal session's history row and its log file.
// Live sessions must be closed or killed first.
func (m *Manager) Remove(sess *AgentSession) error {
	if !sess.Status.Terminal() {
		return Errf(ExitRuntime, "session %s is %s; close or kill it before removing", sess.ID, sess.Status)
	}
	if sess.LogPath != "" {
		_ = os.Remove(sess.LogPath)
	}
	return m.Store.DeleteSession(sess.ID)
}

// Signal records a harness-reported state (T2 adapter path, §7.1 rule
// 3): awaiting-input when blocked on the user, done when the task is
// complete. The next observed meaningful output clears it back to
// working. Signal-set state always outranks T1.5 detection.
func (m *Manager) Signal(sess *AgentSession, status Status) error {
	if !status.Sticky() {
		return Errf(ExitUsage, "unknown state %q (valid: %s, %s)", status, StatusAwaitingInput, StatusDone)
	}
	if sess.Status.Terminal() {
		return nil
	}
	sess.Status = status
	sess.StatusOrigin = OriginSignal
	return m.Store.UpdateSession(sess)
}

// Send injects input into a live session without attaching.
func (m *Manager) Send(sess *AgentSession, text string, enter bool) error {
	if sess.Status.Terminal() {
		return Errf(ExitRuntime, "session %s is %s; cannot send input", sess.ID, sess.Status)
	}
	return m.Backend.SendKeys(sess.ID, text, enter)
}

// harvest waits briefly for the pane to report death, records the exit
// code (or failure), and destroys the tmux session.
func (m *Manager) harvest(sess *AgentSession) error {
	deadline := time.Now().Add(3 * time.Second)
	for {
		alive, err := m.Backend.IsAlive(sess.ID)
		if err != nil {
			return err
		}
		if !alive {
			// Session vanished without a harvestable exit code.
			sess.markFailed(now())
			return m.Store.UpdateSession(sess)
		}
		dead, code, err := m.Backend.DeadStatus(sess.ID)
		if err != nil {
			return err
		}
		if dead {
			sess.markExited(code, now())
			if err := m.Backend.Kill(sess.ID); err != nil {
				return err
			}
			return m.Store.UpdateSession(sess)
		}
		if time.Now().After(deadline) {
			return Errf(ExitRuntime, "session %s did not die after SIGKILL", sess.ID)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
