package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentfactory.sh/af/internal/core"
)

func logContains(t *testing.T, id, want string) func() bool {
	t.Helper()
	return func() bool {
		s := statusJSON(t, id)
		data, _ := os.ReadFile(s.LogPath)
		return strings.Contains(string(data), want)
	}
}

func TestSendRoundTrip(t *testing.T) {
	testEnv(t)
	id := openFixture(t, "echoer", fixture(t, "echoer.sh"))
	waitFor(t, 5*time.Second, "echoer ready", logContains(t, id, "ready"))

	if _, errOut, code := runAF(t, "send", id, "hello", "agent", "world"); code != 0 {
		t.Fatalf("send failed (%d): %s", code, errOut)
	}
	// Args are joined with spaces and round-trip through the fixture.
	waitFor(t, 5*time.Second, "echoed line", logContains(t, id, "echo: hello agent world"))

	// --no-enter leaves the line pending: the echoer never sees it.
	if _, _, code := runAF(t, "send", id, "--no-enter", "pending"); code != 0 {
		t.Fatal("send --no-enter failed")
	}
	time.Sleep(500 * time.Millisecond)
	if logContains(t, id, "echo: pending")() {
		t.Fatal("--no-enter must not submit the line")
	}
	// A follow-up bare Enter submits it.
	if _, _, code := runAF(t, "send", id, ""); code != 0 {
		t.Fatal("bare enter send failed")
	}
	waitFor(t, 5*time.Second, "pending line flushed", logContains(t, id, "echo: pending"))
}

func TestCloseEscalatesToKill(t *testing.T) {
	testEnv(t)
	id := openFixture(t, "stubborn", fixture(t, "stubborn.sh"))
	waitFor(t, 5*time.Second, "stubborn ready", logContains(t, id, "stubborn: ready"))

	start := time.Now()
	if _, errOut, code := runAF(t, "close", id, "--timeout", "1s"); code != 0 {
		t.Fatalf("close failed (%d): %s", code, errOut)
	}
	elapsed := time.Since(start)

	s := statusJSON(t, id)
	if s.Status != "exited" {
		t.Fatalf("want exited, got %s", s.Status)
	}
	// SIGTERM was delivered and ignored, then SIGKILL won.
	data, _ := os.ReadFile(s.LogPath)
	if !strings.Contains(string(data), "ignoring SIGTERM") {
		t.Errorf("SIGTERM never reached the fixture:\n%s", data)
	}
	if elapsed > 6*time.Second {
		t.Errorf("close with --timeout 1s took %v", elapsed)
	}
}

func TestCloseUsesQuitKeys(t *testing.T) {
	testEnv(t)
	// A user-defined harness from the config file, with quit_keys.
	confDir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "agentfactory")
	os.MkdirAll(confDir, 0o755)
	conf := fmt.Sprintf("harnesses:\n  quitter:\n    command: %q\n    quit_keys: [\"quit\"]\n", "sh "+fixture(t, "quitter.sh"))
	if err := os.WriteFile(filepath.Join(confDir, "config.yaml"), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}

	out, errOut, code := runAF(t, "open", "--harness", "quitter", "-q")
	if code != 0 {
		t.Fatalf("open failed (%d): %s", code, errOut)
	}
	id := strings.TrimSpace(out)
	waitFor(t, 5*time.Second, "quitter ready", logContains(t, id, "quitter: ready"))

	if _, errOut, code := runAF(t, "close", id); code != 0 {
		t.Fatalf("close failed (%d): %s", code, errOut)
	}
	s := statusJSON(t, id)
	// Graceful exit via quit keys: code 0, and the farewell is logged.
	if s.Status != "exited" || s.ExitCode == nil || *s.ExitCode != 0 {
		t.Fatalf("want exited(0) via quit keys, got %s %v", s.Status, s.ExitCode)
	}
	data, _ := os.ReadFile(s.LogPath)
	if !strings.Contains(string(data), "quitter: bye") {
		t.Errorf("quit keys never reached the fixture:\n%s", data)
	}
}

func TestServiceCloseSkipsQuitKeys(t *testing.T) {
	testEnv(t)
	// quitter.sh would exit 0 on "quit"; as a --service session close
	// must go straight to SIGTERM, which kills it (default handler).
	out, _, code := runAF(t, "open", "--cmd", "sh "+fixture(t, "quitter.sh"), "--service", "-q")
	if code != 0 {
		t.Fatal("open --service failed")
	}
	id := strings.TrimSpace(out)
	waitFor(t, 5*time.Second, "service ready", logContains(t, id, "quitter: ready"))
	if _, _, code := runAF(t, "close", id); code != 0 {
		t.Fatal("close failed")
	}
	s := statusJSON(t, id)
	data, _ := os.ReadFile(s.LogPath)
	if strings.Contains(string(data), "quitter: bye") {
		t.Error("service close must skip quit keys")
	}
	if s.Status != "exited" {
		t.Errorf("want exited, got %s", s.Status)
	}
}

func TestPeek(t *testing.T) {
	testEnv(t)
	id := openFixture(t, "ticker", fixture(t, "ticker.sh"))
	// at least 4 lines on screen, so --lines 3 truncates
	waitFor(t, 8*time.Second, "ticker output", logContains(t, id, "tick 4"))

	out, _, code := runAF(t, "peek", id)
	if code != 0 || !strings.Contains(out, "tick") {
		t.Fatalf("peek should show the rendered screen (code %d):\n%s", code, out)
	}
	out, _, code = runAF(t, "peek", id, "--lines", "3")
	if code != 0 || len(strings.Split(strings.TrimRight(out, "\n"), "\n")) != 3 {
		t.Fatalf("peek --lines 3 (code %d):\n%q", code, out)
	}
	out, _, code = runAF(t, "peek", id, "--json")
	var parsed map[string]string
	if code != 0 || json.Unmarshal([]byte(out), &parsed) != nil || !strings.Contains(parsed["screen"], "tick") {
		t.Fatalf("peek --json (code %d):\n%s", code, out)
	}
}

func TestLogs(t *testing.T) {
	testEnv(t)
	id := openFixture(t, "ticker", fixture(t, "ticker.sh"))
	waitFor(t, 8*time.Second, "some output", logContains(t, id, "tick 5"))

	out, _, code := runAF(t, "logs", id)
	if code != 0 || !strings.Contains(out, "tick 1") {
		t.Fatalf("logs (code %d):\n%s", code, out)
	}
	out, _, code = runAF(t, "logs", id, "-n", "2")
	if code != 0 {
		t.Fatal("logs -n failed")
	}
	if got := len(strings.Split(strings.TrimRight(out, "\n"), "\n")); got != 2 {
		t.Fatalf("logs -n 2 returned %d lines:\n%q", got, out)
	}
}

func TestDefineDefsOpenRmDef(t *testing.T) {
	testEnv(t)
	// define + open by definition name (payload exits 5 quickly).
	script := fixture(t, "exiter.sh")
	if _, errOut, code := runAF(t, "define", "worker", "--cmd", "sh "+script+" 5 0.2", "--model", "test-model"); code != 0 {
		t.Fatalf("define failed (%d): %s", code, errOut)
	}
	// Upsert keeps unspecified fields.
	if _, _, code := runAF(t, "define", "worker", "--model", "other-model"); code != 0 {
		t.Fatal("define upsert failed")
	}
	out, _, code := runAF(t, "defs", "--json")
	if code != 0 {
		t.Fatal("defs --json failed")
	}
	var defs []core.DefinitionJSON
	if err := json.Unmarshal([]byte(out), &defs); err != nil {
		t.Fatalf("bad defs json: %v\n%s", err, out)
	}
	if len(defs) != 1 || defs[0].Name != "worker" || defs[0].Model != "other-model" ||
		defs[0].Harness != "custom" || defs[0].Config["cmd"] == "" {
		t.Fatalf("upsert lost fields: %+v", defs)
	}

	// Open from the definition; session inherits + finishes with code 5.
	out, errOut, code := runAF(t, "open", "worker", "-q")
	if code != 0 {
		t.Fatalf("open worker failed (%d): %s", code, errOut)
	}
	id := strings.TrimSpace(out)
	waitFor(t, 8*time.Second, "worker exit", func() bool { return statusJSON(t, id).Status == "exited" })
	s := statusJSON(t, id)
	if s.Definition != "worker" || s.Name != "worker" || *s.ExitCode != 5 {
		t.Fatalf("definition not applied: %+v", s)
	}

	// rm-def; history session is unaffected; second delete is exit 3.
	if _, _, code := runAF(t, "rm-def", "worker"); code != 0 {
		t.Fatal("rm-def failed")
	}
	if _, _, code := runAF(t, "rm-def", "worker"); code != core.ExitNotFound {
		t.Fatalf("rm-def on absent def: want 3, got %d", code)
	}
	if s := statusJSON(t, id); s.Definition != "worker" {
		t.Error("rm-def must not touch session history")
	}
	// define without harness/cmd on create is a usage error.
	if _, _, code := runAF(t, "define", "broken"); code != core.ExitUsage {
		t.Fatalf("define without harness: want 2, got %d", code)
	}
}

func TestDefineOpenAndMultiOpen(t *testing.T) {
	testEnv(t)
	script := "sh " + fixture(t, "quiet.sh")

	// define --open creates the definition AND a running session.
	out, errOut, code := runAF(t, "define", "alpha", "--cmd", script, "--open", "-q")
	if code != 0 {
		t.Fatalf("define --open failed (%d): %s", code, errOut)
	}
	id := strings.TrimSpace(out)
	if s := statusJSON(t, id); s.Definition != "alpha" || s.Status == "failed" {
		t.Fatalf("define --open session wrong: %+v", s)
	}

	// open several definitions at once.
	if _, _, betaCode := runAF(t, "define", "beta", "--cmd", script, "-q"); betaCode != 0 {
		t.Fatal("define beta failed")
	}
	out, errOut, code = runAF(t, "open", "alpha", "beta", "-q")
	if code != 0 {
		t.Fatalf("multi open failed (%d): %s", code, errOut)
	}
	ids := strings.Fields(out)
	if len(ids) != 2 {
		t.Fatalf("expected 2 ids, got %q", out)
	}
	if a, b := statusJSON(t, ids[0]), statusJSON(t, ids[1]); a.Definition != "alpha" || b.Definition != "beta" {
		t.Fatalf("wrong definitions: %s / %s", a.Definition, b.Definition)
	}

	// --name with multiple definitions is a usage error.
	if _, _, code := runAF(t, "open", "alpha", "beta", "--name", "x"); code != core.ExitUsage {
		t.Fatalf("--name with multiple defs: want 2, got %d", code)
	}
	// unknown definition in the list: exit 3.
	if _, _, code := runAF(t, "open", "alpha", "ghost", "beta"); code != core.ExitNotFound {
		t.Fatalf("unknown def: want 3, got %d", code)
	}
}

func TestKillAllAndPrune(t *testing.T) {
	testEnv(t)
	a := openFixture(t, "a", fixture(t, "quiet.sh"))
	b := openFixture(t, "b", fixture(t, "ticker.sh"))
	c := openFixture(t, "c", fixture(t, "stubborn.sh"))

	// kill needs a session or --all, not neither/both.
	if _, _, code := runAF(t, "kill"); code != core.ExitUsage {
		t.Fatalf("kill without target: want 2, got %d", code)
	}
	if _, _, code := runAF(t, "kill", a, "--all"); code != core.ExitUsage {
		t.Fatalf("kill with both: want 2, got %d", code)
	}

	out, errOut, code := runAF(t, "kill", "--all")
	if code != 0 {
		t.Fatalf("kill --all failed (%d): %s", code, errOut)
	}
	for _, id := range []string{a, b, c} {
		if !strings.Contains(out, id) {
			t.Errorf("kill --all output missing %s:\n%s", id, out)
		}
		if s := statusJSON(t, id); s.Status != "exited" {
			t.Errorf("%s should be exited, got %s", id, s.Status)
		}
	}
	// ...and prune sweeps the history: the full wipe combo.
	if _, _, code := runAF(t, "prune"); code != 0 {
		t.Fatal("prune failed")
	}
	list, _, _ := runAF(t, "status", "--all")
	for _, id := range []string{a, b, c} {
		if strings.Contains(list, id) {
			t.Errorf("history should be empty after prune:\n%s", list)
		}
	}
	// kill --all with nothing running is a friendly no-op.
	if out, _, code := runAF(t, "kill", "--all"); code != 0 || !strings.Contains(out, "no live sessions") {
		t.Fatalf("empty kill --all: code %d, out %q", code, out)
	}
}

func TestRmAndPrune(t *testing.T) {
	testEnv(t)
	live := openFixture(t, "keeper", fixture(t, "quiet.sh"))
	dead1 := openFixture(t, "done1", fixture(t, "exiter.sh"), "0", "0.1")
	dead2 := openFixture(t, "done2", fixture(t, "exiter.sh"), "0", "0.1")
	waitFor(t, 8*time.Second, "fixtures exited", func() bool {
		return statusJSON(t, dead1).Status == "exited" && statusJSON(t, dead2).Status == "exited"
	})
	logPath := statusJSON(t, dead1).LogPath

	// rm on a live session refuses.
	if _, _, code := runAF(t, "rm", live); code != core.ExitRuntime {
		t.Fatalf("rm live: want 1, got %d", code)
	}
	// rm by name removes row + log.
	if _, errOut, code := runAF(t, "rm", "done1"); code != 0 {
		t.Fatalf("rm failed (%d): %s", code, errOut)
	}
	if _, _, code := runAF(t, "status", dead1, "--all"); code != core.ExitNotFound {
		t.Fatal("removed session should be gone from history")
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatal("rm should delete the log file")
	}
	// prune sweeps remaining terminal sessions, leaves live ones.
	out, _, code := runAF(t, "prune")
	if code != 0 || !strings.Contains(out, "pruned 1") {
		t.Fatalf("prune: code %d, out %q", code, out)
	}
	if _, _, code := runAF(t, "status", dead2, "--all"); code != core.ExitNotFound {
		t.Fatal("prune should remove exited sessions")
	}
	if s := statusJSON(t, live); s.Status == "failed" || s.Status == "exited" {
		t.Fatal("prune must not touch live sessions")
	}
}

// TestStatusJSONGolden pins the §8.3 byte shape with fully
// deterministic session data.
func TestStatusJSONGolden(t *testing.T) {
	testEnv(t)
	store, err := core.OpenStore(os.Getenv("AF_DATA_DIR"))
	if err != nil {
		t.Fatal(err)
	}
	exitCode := 7
	started, _ := time.Parse(time.RFC3339, "2026-07-10T14:02:11Z")
	lastActive, _ := time.Parse(time.RFC3339, "2026-07-10T14:31:47Z")
	ended, _ := time.Parse(time.RFC3339, "2026-07-10T14:40:00Z")
	sess := &core.AgentSession{
		ID: "k3x9p2", Name: "planner", Definition: "planner", Harness: "claude-code",
		Model: "opus", Command: "claude --model opus", WorkDir: "/home/user/src/api",
		Status: core.StatusExited, PID: 41823, PGID: 41823, ExitCode: &exitCode,
		LogPath: "/data/logs/k3x9p2.log", StartedAt: started, LastActive: lastActive,
		EndedAt: &ended, Metadata: map[string]string{},
	}
	if insertErr := store.InsertSession(sess); insertErr != nil {
		t.Fatal(insertErr)
	}
	store.Close()

	out, errOut, code := runAF(t, "status", "--all", "--json")
	if code != 0 {
		t.Fatalf("status --json failed (%d): %s", code, errOut)
	}
	golden, err := os.ReadFile(filepath.Join("..", "..", "testdata", "status_golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	if out != string(golden) {
		t.Fatalf("status --json does not match golden byte shape.\ngot:\n%s\nwant:\n%s", out, golden)
	}
}

func TestExitCodeEnvProblem(t *testing.T) {
	testEnv(t)
	// Point the data dir at a file: DB unopenable => exit 4.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AF_DATA_DIR", blocker)
	if _, _, code := runAF(t, "status"); code != core.ExitEnv {
		t.Fatalf("unopenable db: want 4, got %d", code)
	}
}

func TestSocketFlagOverridesEnv(t *testing.T) {
	testEnv(t)
	override := os.Getenv("AF_SOCKET") + "-flag"
	t.Cleanup(func() {
		exec.Command("tmux", "-L", override, "kill-server").Run()
		os.Remove(filepath.Join(fmt.Sprintf("/tmp/tmux-%d", os.Getuid()), override))
	})
	out, _, code := runAF(t, "--socket", override, "open", "--cmd", "sleep 30", "-q")
	if code != 0 {
		t.Fatal("open with --socket failed")
	}
	id := strings.TrimSpace(out)
	// Session exists on the flag socket, not the env socket.
	if _, _, code := runAF(t, "--socket", override, "status", id); code != 0 {
		t.Error("session should be visible via --socket")
	}
	s := statusJSON(t, id) // env socket: reconcile marks it failed (session not on this socket)
	if s.Status != "failed" {
		t.Errorf("session leaked onto the env socket? status=%s", s.Status)
	}
}
