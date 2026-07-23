package cli

import (
	"testing"
	"time"

	"agentfactory.sh/af/internal/core"
)

func TestSignalAwaitingInputAndAutoClear(t *testing.T) {
	testEnv(t)
	id := openFixture(t, "echoer", fixture(t, "echoer.sh"))
	waitFor(t, 5*time.Second, "echoer ready", logContains(t, id, "ready"))

	// The adapter (e.g. a Claude Code Stop hook) flips the status.
	if _, errOut, code := runAF(t, "signal", id, "awaiting-input"); code != 0 {
		t.Fatalf("signal failed (%d): %s", code, errOut)
	}
	if s := statusJSON(t, id); s.Status != "awaiting-input" {
		t.Fatalf("want awaiting-input, got %s", s.Status)
	}

	// awaiting-input persists across reconciliations without output...
	time.Sleep(1500 * time.Millisecond) // > idle_threshold (1s in tests)
	if s := statusJSON(t, id); s.Status != "awaiting-input" {
		t.Fatalf("awaiting-input must not decay to idle, got %s", s.Status)
	}

	// ...and the next observed log growth clears it automatically.
	if _, _, code := runAF(t, "send", id, "resume"); code != 0 {
		t.Fatal("send failed")
	}
	waitFor(t, 5*time.Second, "signal cleared by output", func() bool {
		return statusJSON(t, id).Status == "working"
	})
}

func TestSignalDone(t *testing.T) {
	testEnv(t)
	id := openFixture(t, "echoer", fixture(t, "echoer.sh"))
	waitFor(t, 5*time.Second, "echoer ready", logContains(t, id, "ready"))

	// The agent itself reports completion (af signal $AF_SESSION_ID done).
	if _, errOut, code := runAF(t, "signal", id, "done"); code != 0 {
		t.Fatalf("signal done failed (%d): %s", code, errOut)
	}
	if s := statusJSON(t, id); s.Status != "done" {
		t.Fatalf("want done, got %s", s.Status)
	}

	// done persists across reconciliations, then output clears it.
	time.Sleep(1500 * time.Millisecond) // > idle_threshold (1s in tests)
	if s := statusJSON(t, id); s.Status != "done" {
		t.Fatalf("done must not decay to idle, got %s", s.Status)
	}
	if _, _, code := runAF(t, "send", id, "next task"); code != 0 {
		t.Fatal("send failed")
	}
	waitFor(t, 5*time.Second, "done cleared by output", func() bool {
		return statusJSON(t, id).Status == "working"
	})
}

func TestTerminalSignals(t *testing.T) {
	testEnv(t)
	id := openFixture(t, "ringer", fixture(t, "ringer.sh"))
	waitFor(t, 5*time.Second, "ringer ready", logContains(t, id, "ready"))

	// A bell at turn end flips the session to awaiting-input — no
	// hooks, no screen patterns, no idle-threshold latency.
	if _, _, code := runAF(t, "send", id, "ring"); code != 0 {
		t.Fatal("send failed")
	}
	waitFor(t, 5*time.Second, "bell detected", func() bool {
		return statusJSON(t, id).Status == "awaiting-input"
	})

	// A command-start mark reads as working again.
	if _, _, code := runAF(t, "send", id, "work"); code != 0 {
		t.Fatal("send failed")
	}
	waitFor(t, 5*time.Second, "cmd-start detected", func() bool {
		return statusJSON(t, id).Status == "working"
	})

	// An OSC 9 notification raises awaiting-input too.
	if _, _, code := runAF(t, "send", id, "notify"); code != 0 {
		t.Fatal("send failed")
	}
	waitFor(t, 5*time.Second, "notification detected", func() bool {
		return statusJSON(t, id).Status == "awaiting-input"
	})

	// The kill switch turns the tier off: with AF_SIGNALS=false the
	// next bell is just output.
	t.Setenv("AF_SIGNALS", "false")
	if _, _, code := runAF(t, "send", id, "work"); code != 0 {
		t.Fatal("send failed")
	}
	waitFor(t, 5*time.Second, "back to working", func() bool {
		return statusJSON(t, id).Status == "working"
	})
	if _, _, code := runAF(t, "send", id, "ring"); code != 0 {
		t.Fatal("send failed")
	}
	time.Sleep(500 * time.Millisecond) // > a few reconcile chances
	if s := statusJSON(t, id); s.Status == "awaiting-input" {
		t.Fatal("AF_SIGNALS=false must disable the tier")
	}
}

func TestSignalValidation(t *testing.T) {
	testEnv(t)
	// Unknown state: usage error (exit 2).
	id := openFixture(t, "quiet", fixture(t, "quiet.sh"))
	if _, _, code := runAF(t, "signal", id, "thinking"); code != core.ExitUsage {
		t.Fatalf("unknown state: want 2, got %d", code)
	}
	// Missing session: exit 3.
	if _, _, code := runAF(t, "signal", "nosuch", "awaiting-input"); code != core.ExitNotFound {
		t.Fatalf("missing session: want 3, got %d", code)
	}
	// Terminal session: no-op with warning, exit 0.
	if _, _, code := runAF(t, "kill", id, "-q"); code != 0 {
		t.Fatal("kill failed")
	}
	_, errOut, code := runAF(t, "signal", id, "awaiting-input")
	if code != 0 || errOut == "" {
		t.Fatalf("terminal signal: want exit 0 + warning, got %d %q", code, errOut)
	}
	if s := statusJSON(t, id); s.Status != "exited" {
		t.Fatalf("terminal status must not change, got %s", s.Status)
	}
}
