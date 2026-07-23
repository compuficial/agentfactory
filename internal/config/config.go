// Package config loads af configuration with precedence:
// flags > AF_* env > YAML file > defaults. Flags are applied by the
// CLI layer after Load returns.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"agentfactory.sh/af/internal/core"
)

// Duration is a time.Duration that unmarshals from YAML strings like "5s".
type Duration time.Duration

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

// D returns the plain time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

// HarnessConfig is a user-defined harness: pure data, merged over the
// built-ins by name.
type HarnessConfig struct {
	Command  string            `yaml:"command"`
	Env      map[string]string `yaml:"env"`
	QuitKeys []string          `yaml:"quit_keys"`
	Detect   core.DetectRules  `yaml:"detect"`
	Files    map[string]string `yaml:"files"`
}

// TUIConfig holds dashboard settings.
type TUIConfig struct {
	Tick Duration `yaml:"tick"`
}

// Config is the fully resolved af configuration.
type Config struct {
	Socket        string                   `yaml:"socket"`
	DataDir       string                   `yaml:"data_dir"`
	IdleThreshold Duration                 `yaml:"idle_threshold"`
	CloseTimeout  Duration                 `yaml:"close_timeout"`
	SendDelay     Duration                 `yaml:"send_delay"`
	TUI           TUIConfig                `yaml:"tui"`
	Harnesses     map[string]HarnessConfig `yaml:"harnesses"`
	// Detect toggles T1.5 screen-pattern detection globally.
	// nil = default (enabled); the pointer distinguishes an explicit
	// `detect: false` from the key being absent.
	Detect *bool `yaml:"detect"`
	// Signals configures T1.75 terminal-signal detection.
	Signals SignalsConfig `yaml:"signals"`

	// Warnings collected during load (unknown keys etc.); the CLI
	// prints them to stderr once.
	Warnings []string `yaml:"-"`
}

// DetectEnabled reports whether T1.5 detection is on (the default).
func (c *Config) DetectEnabled() bool { return c.Detect == nil || *c.Detect }

// SignalsConfig is the T1.75 terminal-signals block. NotifyAwaiting
// narrows which OSC 9/777 notification payloads count as
// awaiting-input; empty = all of them (a terminal notification is by
// definition an attention request).
type SignalsConfig struct {
	Enabled        *bool    `yaml:"enabled"`
	NotifyAwaiting []string `yaml:"notify_awaiting"`
}

// SignalsEnabled reports whether T1.75 is on (the default).
func (c *Config) SignalsEnabled() bool { return c.Signals.Enabled == nil || *c.Signals.Enabled }

// Defaults returns the built-in configuration (every key has one).
func Defaults() *Config {
	return &Config{
		Socket:        "af",
		DataDir:       "~/.local/share/agentfactory",
		IdleThreshold: Duration(5 * time.Second),
		CloseTimeout:  Duration(10 * time.Second),
		SendDelay:     Duration(50 * time.Millisecond),
		TUI:           TUIConfig{Tick: Duration(time.Second)},
		Harnesses:     map[string]HarnessConfig{},
	}
}

// DefaultPath returns ~/.config/agentfactory/config.yaml.
func DefaultPath() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "agentfactory", "config.yaml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "agentfactory", "config.yaml")
}

// Load builds the config from defaults, the YAML file at path (skipped
// if path is "" or missing), and AF_* environment variables.
func Load(path string, env func(string) string) (*Config, error) {
	cfg := Defaults()
	if path != "" {
		if err := applyFile(cfg, path); err != nil {
			return nil, err
		}
	}
	if err := applyEnv(cfg, env); err != nil {
		return nil, err
	}
	cfg.DataDir = core.ExpandHome(cfg.DataDir)
	return cfg, nil
}

func applyFile(cfg *Config, path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read config: %w", err)
	}
	// Unknown keys are a warning, not an error: strict-decode a throwaway
	// copy to collect the complaint, then decode for real without KnownFields.
	strict := yaml.NewDecoder(bytes.NewReader(raw))
	strict.KnownFields(true)
	var probe Config
	if err := strict.Decode(&probe); err != nil && !errors.Is(err, io.EOF) {
		if strings.Contains(err.Error(), "not found in type") {
			cfg.Warnings = append(cfg.Warnings, fmt.Sprintf("config %s: %v", path, err))
		} else {
			return fmt.Errorf("parse config %s: %w", path, err)
		}
	}
	if err := yaml.Unmarshal(raw, cfg); err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}
	return nil
}

func applyEnv(cfg *Config, env func(string) string) error {
	if v := env("AF_SOCKET"); v != "" {
		cfg.Socket = v
	}
	if v := env("AF_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
	for _, e := range []struct {
		name string
		dst  *Duration
	}{
		{"AF_IDLE_THRESHOLD", &cfg.IdleThreshold},
		{"AF_CLOSE_TIMEOUT", &cfg.CloseTimeout},
		{"AF_SEND_DELAY", &cfg.SendDelay},
	} {
		v := env(e.name)
		if v == "" {
			continue
		}
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("%s: invalid duration %q", e.name, v)
		}
		*e.dst = Duration(d)
	}
	if v := env("AF_DETECT"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("AF_DETECT: invalid bool %q", v)
		}
		cfg.Detect = &b
	}
	if v := env("AF_SIGNALS"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("AF_SIGNALS: invalid bool %q", v)
		}
		cfg.Signals.Enabled = &b
	}
	return nil
}

// Resolved renders the fully resolved config as YAML (for af doctor).
func (c *Config) Resolved() string {
	type out struct {
		Socket        string                   `yaml:"socket"`
		DataDir       string                   `yaml:"data_dir"`
		IdleThreshold string                   `yaml:"idle_threshold"`
		CloseTimeout  string                   `yaml:"close_timeout"`
		SendDelay     string                   `yaml:"send_delay"`
		Detect        bool                     `yaml:"detect"`
		Signals       bool                     `yaml:"signals"`
		TUI           map[string]string        `yaml:"tui"`
		Harnesses     map[string]HarnessConfig `yaml:"harnesses,omitempty"`
	}
	b, err := yaml.Marshal(out{
		Socket:        c.Socket,
		DataDir:       c.DataDir,
		IdleThreshold: c.IdleThreshold.D().String(),
		CloseTimeout:  c.CloseTimeout.D().String(),
		SendDelay:     c.SendDelay.D().String(),
		Detect:        c.DetectEnabled(),
		Signals:       c.SignalsEnabled(),
		TUI:           map[string]string{"tick": c.TUI.Tick.D().String()},
		Harnesses:     c.Harnesses,
	})
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	return string(b)
}
