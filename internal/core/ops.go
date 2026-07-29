package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	// quitGrace is how long Close waits for a harness to exit after its
	// QuitKeys are sent, before escalating to SIGTERM.
	quitGrace = 2 * time.Second
	// harvestTimeout bounds how long harvest waits for the pane to report
	// death after SIGKILL before giving up.
	harvestTimeout = 3 * time.Second
	// deadPollInterval is the poll cadence while waiting for a pane to die.
	deadPollInterval = 50 * time.Millisecond
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
	def, err := m.buildDefinition(req)
	if err != nil {
		return nil, err
	}
	harness, err := m.resolveHarness(def)
	if err != nil {
		return nil, err
	}

	// WorkDir: default cwd; resolved to absolute; must exist. Set on def
	// before rendering so the command template can reach it.
	workDir, err := ResolveWorkDir(def.WorkDir)
	if err != nil {
		return nil, err
	}
	def.WorkDir = workDir

	command, err := m.renderCommand(harness, def, req.ExtraArgs)
	if err != nil {
		return nil, err
	}

	name, err := m.uniqueName(req, def)
	if err != nil {
		return nil, err
	}

	id, err := m.newSessionID()
	if err != nil {
		return nil, err
	}

	logDir := filepath.Join(m.DataDir, "logs")
	if err := os.MkdirAll(logDir, dirPerm); err != nil {
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

// buildDefinition loads req's named definition (if any) and layers the
// ad-hoc flag overrides on top, returning the effective launch config.
func (m *Manager) buildDefinition(req OpenRequest) (AgentDefinition, error) {
	def := AgentDefinition{Env: map[string]string{}, Config: map[string]string{}}
	if req.Definition != "" {
		loaded, err := m.Store.GetDefinition(req.Definition)
		if err != nil {
			return def, err
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
		def.Harness = HarnessCustom
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
	return def, nil
}

// resolveHarness checks that def names a harness that exists, and that a
// custom harness carries a command.
func (m *Manager) resolveHarness(def AgentDefinition) (Harness, error) {
	if def.Harness == "" {
		return Harness{}, Errf(ExitUsage, "no harness: pass a definition, --harness, or --cmd")
	}
	harness, err := m.Harnesses.Resolve(def.Harness)
	if err != nil {
		return Harness{}, err
	}
	if def.Harness == HarnessCustom && strings.TrimSpace(def.Config["cmd"]) == "" {
		return Harness{}, Errf(ExitRuntime, "custom harness requires a command (--cmd or config cmd)")
	}
	return harness, nil
}

// renderCommand materializes the harness's wiring files (hook configs,
// notify scripts — pure data) so the template can reach them via
// {{.FilesDir}}, renders the command against def, and appends any
// passthrough args.
func (m *Manager) renderCommand(harness Harness, def AgentDefinition, extraArgs []string) (string, error) {
	filesDir, err := MaterializeFiles(harness, m.DataDir)
	if err != nil {
		return "", err
	}
	command, err := RenderCommand(harness, def, filesDir)
	if err != nil {
		return "", err
	}
	if len(extraArgs) > 0 {
		command += " " + QuoteArgs(extraArgs)
	}
	return command, nil
}

// uniqueName picks the session's display name (--name > definition name
// > harness name) and suffixes it on collision with a live session.
func (m *Manager) uniqueName(req OpenRequest, def AgentDefinition) (string, error) {
	base := req.Name
	if base == "" {
		base = req.Definition
	}
	if base == "" {
		base = def.Harness
	}
	live, err := m.Store.ListSessions(false)
	if err != nil {
		return "", err
	}
	return SuffixName(base, func(candidate string) bool {
		for _, s := range live {
			if s.Name == candidate {
				return true
			}
		}
		return false
	}), nil
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
			if m.waitDead(sess, quitGrace) {
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
		time.Sleep(deadPollInterval)
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
	deadline := time.Now().Add(harvestTimeout)
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
		time.Sleep(deadPollInterval)
	}
}
