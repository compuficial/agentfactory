package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func noEnv(string) string { return "" }

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDefaults(t *testing.T) {
	cfg, err := Load("", noEnv)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Socket != "af" {
		t.Errorf("socket = %q", cfg.Socket)
	}
	if cfg.IdleThreshold.D() != 5*time.Second {
		t.Errorf("idle_threshold = %v", cfg.IdleThreshold.D())
	}
	if cfg.CloseTimeout.D() != 10*time.Second {
		t.Errorf("close_timeout = %v", cfg.CloseTimeout.D())
	}
	if cfg.SendDelay.D() != 50*time.Millisecond {
		t.Errorf("send_delay = %v", cfg.SendDelay.D())
	}
	if cfg.TUI.Tick.D() != time.Second {
		t.Errorf("tui.tick = %v", cfg.TUI.Tick.D())
	}
	if !strings.HasSuffix(cfg.DataDir, "/.local/share/agentfactory") {
		t.Errorf("data_dir = %q (should expand ~)", cfg.DataDir)
	}
}

func TestFileOverridesDefaults(t *testing.T) {
	path := writeConfig(t, `
socket: myaf
idle_threshold: 9s
tui:
  tick: 250ms
harnesses:
  opencode:
    command: "opencode"
    quit_keys: ["/quit"]
`)
	cfg, err := Load(path, noEnv)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Socket != "myaf" || cfg.IdleThreshold.D() != 9*time.Second || cfg.TUI.Tick.D() != 250*time.Millisecond {
		t.Errorf("file values not applied: %+v", cfg)
	}
	if cfg.CloseTimeout.D() != 10*time.Second {
		t.Errorf("unset keys should keep defaults, close_timeout = %v", cfg.CloseTimeout.D())
	}
	oc, ok := cfg.Harnesses["opencode"]
	if !ok || oc.Command != "opencode" || len(oc.QuitKeys) != 1 || oc.QuitKeys[0] != "/quit" {
		t.Errorf("harness not loaded: %+v", cfg.Harnesses)
	}
}

func TestEnvOverridesFile(t *testing.T) {
	path := writeConfig(t, "socket: fromfile\nidle_threshold: 9s\n")
	env := map[string]string{
		"AF_SOCKET":         "fromenv",
		"AF_IDLE_THRESHOLD": "2s",
		"AF_CLOSE_TIMEOUT":  "3s",
		"AF_SEND_DELAY":     "1ms",
		"AF_DATA_DIR":       "/tmp/afdata",
	}
	cfg, err := Load(path, func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Socket != "fromenv" {
		t.Errorf("env should beat file: socket = %q", cfg.Socket)
	}
	if cfg.IdleThreshold.D() != 2*time.Second || cfg.CloseTimeout.D() != 3*time.Second ||
		cfg.SendDelay.D() != time.Millisecond || cfg.DataDir != "/tmp/afdata" {
		t.Errorf("env values not applied: %+v", cfg)
	}
}

func TestUnknownKeysWarnNotError(t *testing.T) {
	path := writeConfig(t, "socket: ok\nfrobnicator: 12\n")
	cfg, err := Load(path, noEnv)
	if err != nil {
		t.Fatalf("unknown keys must not be fatal: %v", err)
	}
	if cfg.Socket != "ok" {
		t.Errorf("known keys should still load, socket = %q", cfg.Socket)
	}
	if len(cfg.Warnings) == 0 {
		t.Error("expected a warning about the unknown key")
	}
}

func TestBadDurationIsError(t *testing.T) {
	path := writeConfig(t, "idle_threshold: banana\n")
	if _, err := Load(path, noEnv); err == nil {
		t.Fatal("expected an error for an invalid duration")
	}
	if _, err := Load("", func(k string) string {
		if k == "AF_SEND_DELAY" {
			return "banana"
		}
		return ""
	}); err == nil {
		t.Fatal("expected an error for an invalid env duration")
	}
}

func TestMissingFileIsFine(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.yaml"), noEnv)
	if err != nil || cfg.Socket != "af" {
		t.Fatalf("missing config file should mean defaults: %v %+v", err, cfg)
	}
}

func TestDetectConfig(t *testing.T) {
	noEnv := func(string) string { return "" }

	// Default: enabled.
	cfg, err := Load("", noEnv)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.DetectEnabled() {
		t.Fatal("detect must default to enabled")
	}

	// File kill switch + per-harness rules.
	path := filepath.Join(t.TempDir(), "config.yaml")
	yaml := "detect: false\nharnesses:\n  myagent:\n    command: myagent\n    detect:\n      awaiting_input: [\"foo\"]\n      working: [\"bar\"]\n"
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(path, noEnv)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DetectEnabled() {
		t.Fatal("detect: false must disable detection")
	}
	h := cfg.Harnesses["myagent"]
	if len(h.Detect.AwaitingInput) != 1 || h.Detect.AwaitingInput[0] != "foo" || len(h.Detect.Working) != 1 {
		t.Fatalf("harness detect rules not parsed: %+v", h.Detect)
	}

	// AF_DETECT env overrides the file; bad values are errors.
	cfg, err = Load(path, func(k string) string {
		if k == "AF_DETECT" {
			return "true"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.DetectEnabled() {
		t.Fatal("AF_DETECT=true must override the file")
	}
	if _, err := Load(path, func(k string) string {
		if k == "AF_DETECT" {
			return "maybe"
		}
		return ""
	}); err == nil {
		t.Fatal("bad AF_DETECT must error")
	}
}

func TestSignalsConfig(t *testing.T) {
	noEnv := func(string) string { return "" }

	// Default: enabled, no keyword filter.
	cfg, err := Load("", noEnv)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.SignalsEnabled() || len(cfg.Signals.NotifyAwaiting) != 0 {
		t.Fatalf("signals must default to enabled/unfiltered: %+v", cfg.Signals)
	}

	// File: kill switch + keyword filter.
	path := filepath.Join(t.TempDir(), "config.yaml")
	yaml := "signals:\n  enabled: false\n  notify_awaiting: [\"(?i)permission\"]\n"
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(path, noEnv)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SignalsEnabled() {
		t.Fatal("signals.enabled: false must disable the tier")
	}
	if len(cfg.Signals.NotifyAwaiting) != 1 || cfg.Signals.NotifyAwaiting[0] != "(?i)permission" {
		t.Fatalf("notify_awaiting not parsed: %+v", cfg.Signals)
	}

	// AF_SIGNALS overrides the file; bad values are errors.
	cfg, err = Load(path, func(k string) string {
		if k == "AF_SIGNALS" {
			return "1"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.SignalsEnabled() {
		t.Fatal("AF_SIGNALS=1 must override the file")
	}
	if _, err := Load(path, func(k string) string {
		if k == "AF_SIGNALS" {
			return "maybe"
		}
		return ""
	}); err == nil {
		t.Fatal("bad AF_SIGNALS must error")
	}
}
