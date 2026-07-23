package core

import (
	"bytes"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/template"
)

// Builtins returns the compiled-in harnesses: the four major agent
// CLIs plus "custom" for anything else. Users can override any of
// these (or add more) via config; same name wins.
func Builtins() map[string]Harness {
	return map[string]Harness{
		"claude-code": {
			Name: "claude-code",
			// Hook auto-wiring, expressed purely as harness data: Files
			// materializes a settings file whose Stop/Notification hooks
			// flip the session to awaiting-input the moment the agent
			// blocks — no user setup, no dotfile edits. Any harness gets
			// the same treatment via config (files: + {{.FilesDir}});
			// overriding this harness in config opts out.
			CommandTmpl: `claude{{if .FilesDir}} --settings {{.FilesDir}}/settings.json{{end}}{{if .Model}} --model {{.Model}}{{end}}`,
			Env:         map[string]string{},
			QuitKeys:    []string{"/exit"},
			Files: map[string]string{
				"settings.json": `{
  "hooks": {
    "Stop": [
      {"hooks": [{"type": "command", "command": "af signal \"$AF_SESSION_ID\" awaiting-input || true"}]}
    ],
    "Notification": [
      {"hooks": [{"type": "command", "command": "af signal \"$AF_SESSION_ID\" awaiting-input || true"}]}
    ]
  }
}
`,
			},
			// T1.5 defaults for the Claude Code TUI: permission and
			// plan dialogs, numbered choice menus, and the idle prompt
			// footer read as awaiting-input; the spinner's interrupt
			// hint suppresses (model still running). Heuristic —
			// verified against the live TUI, overridable via config.
			Detect: DetectRules{
				AwaitingInput: []string{
					`(?i)do you want`,
					`(?i)would you like`,
					`❯ 1\.`,
					`\? for shortcuts`,
				},
				Working: []string{
					`esc to interrupt`,
				},
			},
		},
		"codex": {
			Name:        "codex",
			CommandTmpl: `codex{{if .Model}} --model {{.Model}}{{end}}`,
			Env:         map[string]string{},
			QuitKeys:    []string{"/quit"},
		},
		"grok": {
			Name:        "grok",
			CommandTmpl: `grok{{if .Model}} --model {{.Model}}{{end}}`,
			Env:         map[string]string{},
			QuitKeys:    []string{"/exit"},
		},
		"opencode": {
			Name:        "opencode",
			CommandTmpl: `opencode{{if .Model}} --model {{.Model}}{{end}}`,
			Env:         map[string]string{},
			QuitKeys:    []string{"/exit"},
		},
		"custom": {
			Name:        "custom",
			CommandTmpl: `{{index .Config "cmd"}}`,
			Env:         map[string]string{},
			QuitKeys:    []string{},
		},
	}
}

// HarnessSet is the merged view of built-in and user-configured
// harnesses (user config wins on name collision).
type HarnessSet map[string]Harness

// NewHarnessSet merges user-configured harnesses over the built-ins.
func NewHarnessSet(user map[string]Harness) HarnessSet {
	set := HarnessSet(Builtins())
	for name, h := range user {
		h.Name = name
		if h.Env == nil {
			h.Env = map[string]string{}
		}
		set[name] = h
	}
	return set
}

// Names lists the known harness names, sorted.
func (s HarnessSet) Names() []string { return slices.Sorted(maps.Keys(s)) }

// Resolve returns the named harness or an exit-1 error listing known ones.
func (s HarnessSet) Resolve(name string) (Harness, error) {
	h, ok := s[name]
	if !ok {
		return Harness{}, Errf(ExitRuntime, "unknown harness %q (known: %s)", name, strings.Join(s.Names(), ", "))
	}
	return h, nil
}

// RenderCommand executes the harness command template. Template data is
// the resolved definition plus FilesDir — where MaterializeFiles put the
// harness's Files (empty at define-time validation and for harnesses
// without files, so {{if .FilesDir}} guards keep those renders clean).
// Render errors and empty results are validation failures (exit 1).
func RenderCommand(h Harness, def AgentDefinition, filesDir string) (string, error) {
	tmpl, err := template.New(h.Name).Parse(h.CommandTmpl)
	if err != nil {
		return "", Errf(ExitRuntime, "harness %s: bad command template: %v", h.Name, err)
	}
	var buf bytes.Buffer
	data := struct {
		AgentDefinition
		FilesDir string
	}{def, filesDir}
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", Errf(ExitRuntime, "harness %s: render command: %v", h.Name, err)
	}
	cmd := strings.TrimSpace(buf.String())
	if cmd == "" {
		return "", Errf(ExitRuntime, "harness %s: rendered command is empty", h.Name)
	}
	return cmd, nil
}

// MaterializeFiles writes the harness's Files under
// <dataDir>/harnesses/<name>/ and returns that directory ("" when the
// harness declares none). Unconditional rewrite: idempotent,
// self-healing after upgrades, and same-content races are benign. File
// names must be bare names — this is a wiring surface, not an archive
// format.
func MaterializeFiles(h Harness, dataDir string) (string, error) {
	if len(h.Files) == 0 {
		return "", nil
	}
	dir := filepath.Join(dataDir, "harnesses", h.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", Errf(ExitRuntime, "create harness files dir: %v", err)
	}
	for name, content := range h.Files {
		if name == "" || name == "." || name == ".." || name != filepath.Base(name) {
			return "", Errf(ExitRuntime, "harness %s: invalid file name %q (bare names only)", h.Name, name)
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return "", Errf(ExitRuntime, "write harness file %s: %v", path, err)
		}
	}
	return dir, nil
}

// ValidateDefinition applies af open's validation minus the workdir
// existence check (that happens at open time, not define time).
func (s HarnessSet) ValidateDefinition(def *AgentDefinition) error {
	if def.Harness == "" {
		return Errf(ExitUsage, "definition %q needs --harness (or --cmd)", def.Name)
	}
	harness, err := s.Resolve(def.Harness)
	if err != nil {
		return err
	}
	if def.Harness == "custom" && strings.TrimSpace(def.Config["cmd"]) == "" {
		return Errf(ExitRuntime, "custom harness requires a command (--cmd or --config cmd=...)")
	}
	_, err = RenderCommand(harness, *def, "")
	return err
}

// MergeEnv layers maps left to right (rightmost wins).
func MergeEnv(layers ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, l := range layers {
		maps.Copy(out, l)
	}
	return out
}

// ParseKV parses a K=V flag value.
func ParseKV(s string) (string, string, error) {
	k, v, ok := strings.Cut(s, "=")
	if !ok || k == "" {
		return "", "", Errf(ExitUsage, "expected K=V, got %q", s)
	}
	return k, v, nil
}
