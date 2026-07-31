package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"agentfactory.sh/af/internal/core"
)

// testEnv isolates a test on a throwaway tmux socket and temp data dir.
// The user's default "af" socket is never touched.
func testEnv(t *testing.T) {
	t.Helper()
	buf := make([]byte, 4)
	rand.Read(buf)
	socket := "af-test-" + hex.EncodeToString(buf)
	t.Setenv("AF_SOCKET", socket)
	t.Setenv("AF_DATA_DIR", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // ignore any user config file
	t.Setenv("AF_IDLE_THRESHOLD", "1s")
	t.Cleanup(func() {
		_ = exec.CommandContext(context.Background(), "tmux", "-L", socket, "kill-server").Run()
		_ = os.Remove(filepath.Join(fmt.Sprintf("/tmp/tmux-%d", os.Getuid()), socket))
	})
}

// runAF executes one af invocation through a fresh command tree,
// exactly like the binary would.
func runAF(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	root := NewRoot()
	var out, errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), errBuf.String(), core.ExitCode(err)
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "testdata", "fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return p
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func statusJSON(t *testing.T, ref string) core.SessionJSON {
	t.Helper()
	out, errOut, code := runAF(t, "status", ref, "--json", "--all")
	if code != 0 {
		t.Fatalf("status %s failed (%d): %s", ref, code, errOut)
	}
	var s core.SessionJSON
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		t.Fatalf("bad status json: %v\n%s", err, out)
	}
	return s
}

func openFixture(t *testing.T, name string, script string, args ...string) string {
	t.Helper()
	// exec makes the fixture shell THE pane process on every /bin/sh.
	// Without it, dash (Ubuntu's sh) may keep a wrapper shell as the
	// pane: signal-delivery tests then hit the wrapper, not the
	// fixture's traps.
	cmd := "exec sh " + script
	if len(args) > 0 {
		cmd += " " + strings.Join(args, " ")
	}
	out, errOut, code := runAF(t, "open", "--cmd", cmd, "--name", name, "-q")
	if code != 0 {
		t.Fatalf("open failed (%d): %s", code, errOut)
	}
	return strings.TrimSpace(out)
}

func TestDoctorPasses(t *testing.T) {
	testEnv(t)
	out, errOut, code := runAF(t, "doctor")
	if code != 0 {
		t.Fatalf("doctor failed (%d): %s\n%s", code, out, errOut)
	}
	for _, check := range []string{"tmux", "socket", "data_dir", "database", "reconciliation"} {
		if !strings.Contains(out, check) {
			t.Errorf("doctor output missing %q check:\n%s", check, out)
		}
	}
}

func TestOpenCreatesLiveTmuxSessionWithEnv(t *testing.T) {
	testEnv(t)
	id := openFixture(t, "t1", fixture(t, "ticker.sh"))

	// Live tmux session on the af socket (M1 acceptance).
	socket := os.Getenv("AF_SOCKET")
	if err := exec.CommandContext(context.Background(), "tmux", "-L", socket, "has-session", "-t", "=af-"+id).Run(); err != nil {
		t.Fatalf("tmux session af-%s not alive: %v", id, err)
	}

	// Env injected: ask tmux for the session environment.
	out, err := exec.CommandContext(context.Background(), "tmux", "-L", socket, "show-environment", "-t", "=af-"+id).Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "AF_SESSION_ID="+id) || !strings.Contains(string(out), "AF_SESSION_NAME=t1") {
		t.Fatalf("AF_* env not injected:\n%s", out)
	}
}

func TestStatusWorkingThenIdle(t *testing.T) {
	testEnv(t)
	tickerID := openFixture(t, "chatty", fixture(t, "ticker.sh"))
	quietID := openFixture(t, "sleepy", fixture(t, "quiet.sh"))

	// ticker keeps producing output => stays working
	waitFor(t, 5*time.Second, "ticker working", func() bool {
		return statusJSON(t, tickerID).Status == "working"
	})
	// quiet goes silent => idle after idle_threshold (1s here)
	waitFor(t, 8*time.Second, "quiet idle", func() bool {
		return statusJSON(t, quietID).Status == "idle"
	})
	// and the ticker is still working
	if s := statusJSON(t, tickerID); s.Status != "working" {
		t.Fatalf("ticker should still be working, got %s", s.Status)
	}
}

func TestExitCodeHarvest(t *testing.T) {
	testEnv(t)
	id := openFixture(t, "exiter", fixture(t, "exiter.sh"), "7", "0.3")

	waitFor(t, 8*time.Second, "exited status", func() bool {
		return statusJSON(t, id).Status == "exited"
	})
	s := statusJSON(t, id)
	if s.ExitCode == nil || *s.ExitCode != 7 {
		t.Fatalf("want exit_code 7, got %+v", s.ExitCode)
	}
	if s.EndedAt == nil {
		t.Fatal("EndedAt not set")
	}

	// Harvested session is gone from tmux...
	socket := os.Getenv("AF_SOCKET")
	if err := exec.CommandContext(context.Background(), "tmux", "-L", socket, "has-session", "-t", "=af-"+id).Run(); err == nil {
		t.Fatal("tmux session should be killed after harvest")
	}
	// ...hidden from the default list, but kept in --all history.
	list, _, _ := runAF(t, "status")
	if strings.Contains(list, id) {
		t.Errorf("default status should hide terminal sessions:\n%s", list)
	}
	all, _, _ := runAF(t, "status", "--all")
	if !strings.Contains(all, id) {
		t.Errorf("status --all should include history:\n%s", all)
	}

	// Attaching to a terminal session is an error (checked before exec).
	_, _, code := runAF(t, "attach", id)
	if code != core.ExitRuntime {
		t.Fatalf("attach on exited session: want exit 1, got %d", code)
	}

	// A name that only matches history still resolves: kill/close are
	// no-op warnings (exit 0), never "not found".
	_, errOut, code := runAF(t, "kill", "exiter")
	if code != 0 || !strings.Contains(errOut, "already") {
		t.Fatalf("kill by history name: want no-op warning, got code %d: %s", code, errOut)
	}
}

func TestKillRemovesWholeProcessGroup(t *testing.T) {
	testEnv(t)
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	id := openFixture(t, "spawner", fixture(t, "spawner.sh"), pidFile)

	waitFor(t, 5*time.Second, "child pid file", func() bool {
		b, err := os.ReadFile(pidFile)
		return err == nil && len(bytes.TrimSpace(b)) > 0
	})
	b, _ := os.ReadFile(pidFile)
	childPID, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatalf("bad child pid %q", b)
	}

	if _, errOut, code := runAF(t, "kill", id); code != 0 {
		t.Fatalf("kill failed (%d): %s", code, errOut)
	}
	// The child was in the same process group and must be dead too.
	waitFor(t, 3*time.Second, "child death", func() bool {
		return syscall.Kill(childPID, 0) != nil
	})
	if s := statusJSON(t, id); s.Status != "exited" {
		t.Fatalf("killed session should be exited, got %s", s.Status)
	}
}

func TestKillSigtermIgnorer(t *testing.T) {
	testEnv(t)
	id := openFixture(t, "stubborn", fixture(t, "stubborn.sh"))
	waitFor(t, 5*time.Second, "stubborn ready", func() bool {
		return statusJSON(t, id).Status == "working" || statusJSON(t, id).Status == "idle"
	})
	if _, errOut, code := runAF(t, "kill", id); code != 0 {
		t.Fatalf("kill failed (%d): %s", code, errOut)
	}
	if s := statusJSON(t, id); s.Status != "exited" {
		t.Fatalf("want exited, got %s", s.Status)
	}
}

func TestSessionsSurviveAcrossInvocations(t *testing.T) {
	// Every runAF builds a fresh command tree: state lives only in
	// tmux + SQLite, so sessions are re-adopted by any new af process.
	testEnv(t)
	id := openFixture(t, "survivor", fixture(t, "ticker.sh"))
	for range 3 {
		if s := statusJSON(t, id); s.Status == "exited" || s.Status == "failed" {
			t.Fatalf("session lost between invocations: %s", s.Status)
		}
	}
}

func TestExitCodes(t *testing.T) {
	testEnv(t)
	// 3: session not found
	if _, _, code := runAF(t, "status", "nosuch"); code != core.ExitNotFound {
		t.Errorf("status nosuch: want 3, got %d", code)
	}
	// 3: unknown definition
	if _, _, code := runAF(t, "open", "ghost-def"); code != core.ExitNotFound {
		t.Errorf("open ghost-def: want 3, got %d", code)
	}
	// 2: bad flag
	if _, _, code := runAF(t, "status", "--bogus"); code != core.ExitUsage {
		t.Errorf("bad flag: want 2, got %d", code)
	}
	// 2: unknown command
	if _, _, code := runAF(t, "frobnicate"); code != core.ExitUsage {
		t.Errorf("unknown command: want 2, got %d", code)
	}
	// 2: too many args
	if _, _, code := runAF(t, "kill"); code != core.ExitUsage {
		t.Errorf("kill without args: want 2, got %d", code)
	}
	// 1: unknown harness
	if _, _, code := runAF(t, "open", "--harness", "bogus"); code != core.ExitRuntime {
		t.Errorf("unknown harness: want 1, got %d", code)
	}
	// 1: workdir missing
	if _, _, code := runAF(t, "open", "--cmd", "true", "-C", "/no/such/dir"); code != core.ExitRuntime {
		t.Errorf("bad workdir: want 1, got %d", code)
	}
	// 0 + quiet: open prints only the ID
	out, _, code := runAF(t, "open", "--cmd", "sh "+fixture(t, "quiet.sh"), "-q")
	if code != 0 || len(strings.Fields(out)) != 1 {
		t.Errorf("open -q should print only the id, got %q (code %d)", out, code)
	}
}
