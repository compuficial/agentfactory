// Package core is the af library: definitions, sessions, state,
// reconciliation. Nothing in this package knows tmux exists; all
// substrate access goes through SessionBackend.
package core

import "time"

// Harness is a concrete recipe for launching a type of agent.
type Harness struct {
	Name        string            `json:"name"`
	CommandTmpl string            `json:"command_tmpl"`
	Env         map[string]string `json:"env"`
	// QuitKeys are sent via SendKeys on close, before signaling.
	// Each entry is sent literally + Enter. Empty = signal-only.
	QuitKeys []string `json:"quit_keys"`
	// Detect holds T1.5 screen-pattern rules (detect.go); zero value =
	// harness-specific detection off (universal rules still apply).
	Detect DetectRules `json:"detect"`
	// Files are materialized under <data_dir>/harnesses/<name>/ before
	// launch; the command template reaches them via {{.FilesDir}}. This
	// is how harness-side wiring (hook configs, notify scripts) ships as
	// pure data — af has no per-harness code paths.
	Files map[string]string `json:"files"`
}

// AgentDefinition is a named, reusable launch template.
type AgentDefinition struct {
	Name    string            `json:"name"`
	Harness string            `json:"harness"`
	Model   string            `json:"model"`
	WorkDir string            `json:"work_dir"`
	Env     map[string]string `json:"env"`
	Config  map[string]string `json:"config"`
	Service bool              `json:"service"`
}

// Per-session environment variables af injects at open time. They
// identify the specific session, so they must never be captured into a
// reusable definition (see SeedFromSession).
const (
	EnvSessionID   = "AF_SESSION_ID"
	EnvSessionName = "AF_SESSION_NAME"
)

// SeedFromSession copies a live session's launch configuration onto the
// definition: harness, model, workdir, service, the merged env (minus
// the per-session vars af injects), and — for a custom session — the
// exact command. Shared by `af define --from` and the dashboard's
// save-as-definition action so both capture identical fields.
func (d *AgentDefinition) SeedFromSession(s *AgentSession) {
	d.Harness = s.Harness
	d.Model = s.Model
	d.WorkDir = s.WorkDir
	d.Service = s.Service
	if d.Env == nil {
		d.Env = map[string]string{}
	}
	for k, v := range s.Env {
		if k == EnvSessionID || k == EnvSessionName {
			continue
		}
		d.Env[k] = v
	}
	if s.Harness == "custom" {
		if d.Config == nil {
			d.Config = map[string]string{}
		}
		d.Config["cmd"] = s.Command
	}
}

// Status is a session's lifecycle state (spec §7.1).
type Status string

// Session statuses; Exited and Failed are terminal.
const (
	StatusStarting      Status = "starting"
	StatusWorking       Status = "working"
	StatusIdle          Status = "idle"
	StatusAwaitingInput Status = "awaiting-input"
	StatusDone          Status = "done"
	StatusExited        Status = "exited"
	StatusFailed        Status = "failed"
)

// Terminal reports whether s is a terminal status.
func (s Status) Terminal() bool { return s == StatusExited || s == StatusFailed }

// Sticky reports whether s is a harness-reported state that the T1
// heuristic must not overwrite on its own (§7.1 rule 3): awaiting-input
// (blocked on the user) and done (task complete, payload still alive).
func (s Status) Sticky() bool { return s == StatusAwaitingInput || s == StatusDone }

// StatusOrigin values: who set a sticky status. Precedence: signal
// (T2, explicit) > term (T1.75, terminal protocol) > detect (T1.5,
// screen patterns). Reconciliation holds signal-set state until output
// clears it, holds term-set state until output or a protocol event
// updates it, and re-evaluates detect-set state every pass.
const (
	OriginSignal = "signal"
	OriginTerm   = "term"
	OriginDetect = "detect"
)

// ParseStatus validates a user-supplied status name (af wait --for).
func ParseStatus(s string) (Status, error) {
	switch Status(s) {
	case StatusStarting, StatusWorking, StatusIdle, StatusAwaitingInput,
		StatusDone, StatusExited, StatusFailed:
		return Status(s), nil
	}
	return "", Errf(ExitUsage, "unknown status %q", s)
}

// AgentSession is a running (or ended) instance launched from a
// definition or ad-hoc flags. The tmux session name is "af-"+ID.
type AgentSession struct {
	ID         string
	Name       string
	Definition string
	Harness    string
	Model      string
	Command    string // fully rendered command actually executed
	WorkDir    string
	Status     Status
	PID        int
	PGID       int
	ExitCode   *int
	LogPath    string
	Service    bool
	StartedAt  time.Time
	LastActive time.Time
	EndedAt    *time.Time
	Metadata   map[string]string

	// Env is the fully merged environment injected at open time.
	// Persisted for the TUI detail view; not part of the JSON schema.
	Env map[string]string
	// LogOffset is the T1 high-water mark of the log file size.
	// Persisted; not part of the JSON schema.
	LogOffset int64
	// StatusOrigin records who set a sticky status (OriginSignal or
	// OriginDetect; "" otherwise). Persisted; not part of the JSON schema.
	StatusOrigin string
}

// markExited records a harvested process exit at t: terminal status,
// exit code, end time.
func (a *AgentSession) markExited(code int, t time.Time) {
	a.Status = StatusExited
	a.ExitCode = &code
	a.EndedAt = &t
}

// markFailed records the substrate losing the session at t (tmux gone
// without a harvestable exit); ExitCode deliberately stays nil.
func (a *AgentSession) markFailed(t time.Time) {
	a.Status = StatusFailed
	a.EndedAt = &t
}

// SessionBackend is the substrate interface. v0.1 ships exactly one
// implementation: tmux on a dedicated socket.
type SessionBackend interface {
	Create(sess *AgentSession) error                  // detached session, -c workdir
	Attach(id string) error                           // execs into tmux attach
	CapturePane(id string, lines int) (string, error) // lines<=0 = full visible screen
	SendKeys(id string, input string, enter bool) error
	IsAlive(id string) (bool, error)
	DeadStatus(id string) (dead bool, exitCode int, err error)
	Kill(id string) error
}
