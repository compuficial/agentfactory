package core

import "time"

// SessionJSON is the normative af status --json shape (§8.3). Field
// order matches the spec example.
type SessionJSON struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Definition string            `json:"definition"`
	Harness    string            `json:"harness"`
	Model      string            `json:"model"`
	Command    string            `json:"command"`
	WorkDir    string            `json:"work_dir"`
	Status     string            `json:"status"`
	PID        int               `json:"pid"`
	PGID       int               `json:"pgid"`
	ExitCode   *int              `json:"exit_code"`
	Service    bool              `json:"service"`
	LogPath    string            `json:"log_path"`
	StartedAt  string            `json:"started_at"`
	LastActive string            `json:"last_active"`
	EndedAt    *string           `json:"ended_at"`
	Metadata   map[string]string `json:"metadata"`
}

// JSON converts the session to its stable §8.3 wire shape.
func (a *AgentSession) JSON() SessionJSON {
	out := SessionJSON{
		ID:         a.ID,
		Name:       a.Name,
		Definition: a.Definition,
		Harness:    a.Harness,
		Model:      a.Model,
		Command:    a.Command,
		WorkDir:    a.WorkDir,
		Status:     string(a.Status),
		PID:        a.PID,
		PGID:       a.PGID,
		ExitCode:   a.ExitCode,
		Service:    a.Service,
		LogPath:    a.LogPath,
		StartedAt:  a.StartedAt.UTC().Format(time.RFC3339),
		LastActive: a.LastActive.UTC().Format(time.RFC3339),
		Metadata:   a.Metadata,
	}
	if a.EndedAt != nil {
		s := a.EndedAt.UTC().Format(time.RFC3339)
		out.EndedAt = &s
	}
	if out.Metadata == nil {
		out.Metadata = map[string]string{}
	}
	return out
}

// DefinitionJSON is the af defs --json shape: the same lower_snake_case
// treatment of AgentDefinition.
type DefinitionJSON struct {
	Name    string            `json:"name"`
	Harness string            `json:"harness"`
	Model   string            `json:"model"`
	WorkDir string            `json:"work_dir"`
	Env     map[string]string `json:"env"`
	Config  map[string]string `json:"config"`
	Service bool              `json:"service"`
}

// JSON converts the definition to its stable wire shape.
func (d *AgentDefinition) JSON() DefinitionJSON {
	out := DefinitionJSON{
		Name:    d.Name,
		Harness: d.Harness,
		Model:   d.Model,
		WorkDir: d.WorkDir,
		Env:     d.Env,
		Config:  d.Config,
		Service: d.Service,
	}
	if out.Env == nil {
		out.Env = map[string]string{}
	}
	if out.Config == nil {
		out.Config = map[string]string{}
	}
	return out
}
