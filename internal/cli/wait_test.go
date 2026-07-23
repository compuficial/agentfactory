package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"agentfactory.sh/af/internal/core"
)

func TestWaitForIdle(t *testing.T) {
	testEnv(t)
	id := openFixture(t, "quiet", fixture(t, "quiet.sh"))

	out, errOut, code := runAF(t, "wait", id, "--for", "idle", "--timeout", "10s", "--interval", "100ms")
	if code != 0 {
		t.Fatalf("wait failed (%d): %s", code, errOut)
	}
	if strings.TrimSpace(out) != "idle" {
		t.Fatalf("want %q on stdout, got %q", "idle", out)
	}
}

func TestWaitForDoneSignaledMidWait(t *testing.T) {
	testEnv(t)
	id := openFixture(t, "echoer", fixture(t, "echoer.sh"))
	waitFor(t, 5*time.Second, "echoer ready", logContains(t, id, "ready"))

	// A peer (here: a goroutine) signals done while wait blocks — the
	// coordinator pattern from the spec.
	go func() {
		time.Sleep(500 * time.Millisecond)
		runAF(t, "signal", id, "done")
	}()
	out, errOut, code := runAF(t, "wait", id, "--for", "done", "--timeout", "10s", "--interval", "100ms")
	if code != 0 {
		t.Fatalf("wait failed (%d): %s", code, errOut)
	}
	if strings.TrimSpace(out) != "done" {
		t.Fatalf("want %q, got %q", "done", out)
	}
}

func TestWaitTimeoutExitsFive(t *testing.T) {
	testEnv(t)
	id := openFixture(t, "ticker", fixture(t, "ticker.sh"))

	// The ticker never stops, so waiting for done must time out.
	_, errOut, code := runAF(t, "wait", id, "--for", "done", "--timeout", "400ms", "--interval", "100ms")
	if code != 5 {
		t.Fatalf("want exit 5 on timeout, got %d: %s", code, errOut)
	}
}

func TestWaitTerminalOutsideTargetsExitsOne(t *testing.T) {
	testEnv(t)
	id := openFixture(t, "exiter", fixture(t, "exiter.sh"), "0", "0.2")

	out, _, code := runAF(t, "wait", id, "--for", "done", "--timeout", "10s", "--interval", "100ms")
	if code != core.ExitRuntime {
		t.Fatalf("want exit 1 for terminal-outside-targets, got %d", code)
	}
	if strings.TrimSpace(out) != "exited" {
		t.Fatalf("want final status on stdout, got %q", out)
	}
}

func TestWaitForExitedIsReachable(t *testing.T) {
	testEnv(t)
	id := openFixture(t, "exiter", fixture(t, "exiter.sh"), "7", "0.2")

	out, errOut, code := runAF(t, "wait", id, "--for", "exited", "--timeout", "10s", "--interval", "100ms", "--json")
	if code != 0 {
		t.Fatalf("wait --for exited failed (%d): %s", code, errOut)
	}
	var s core.SessionJSON
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		t.Fatalf("bad wait json: %v\n%s", err, out)
	}
	if s.Status != "exited" || s.ExitCode == nil || *s.ExitCode != 7 {
		t.Fatalf("want exited(7), got %+v", s)
	}
}

func TestWaitValidation(t *testing.T) {
	testEnv(t)
	// Unknown status name: usage error before any waiting happens.
	if _, _, code := runAF(t, "wait", "whatever", "--for", "bogus"); code != core.ExitUsage {
		t.Fatalf("unknown status: want 2, got %d", code)
	}
	// Missing session: exit 3.
	if _, _, code := runAF(t, "wait", "nosuch", "--for", "idle"); code != core.ExitNotFound {
		t.Fatalf("missing session: want 3, got %d", code)
	}
}
