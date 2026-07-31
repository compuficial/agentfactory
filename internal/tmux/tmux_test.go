package tmux

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"agentfactory.sh/af/internal/core"
)

// testBackend returns a Backend on a throwaway socket, torn down with
// the test. The user's default "af" socket is never touched.
func testBackend(t *testing.T) *Backend {
	t.Helper()
	if _, err := CheckTmux(); err != nil {
		t.Skipf("tmux unavailable: %v", err)
	}
	buf := make([]byte, 4)
	rand.Read(buf)
	socket := "af-tmuxtest-" + hex.EncodeToString(buf)
	t.Cleanup(func() {
		_ = exec.CommandContext(context.Background(), "tmux", "-L", socket, "kill-server").Run()
	})
	return New(socket, 50*time.Millisecond)
}

// testSession creates a live session running command in a temp workdir.
func testSession(t *testing.T, b *Backend, id, command string) *core.AgentSession {
	t.Helper()
	sess := &core.AgentSession{
		ID:      id,
		Name:    id,
		Command: command,
		WorkDir: t.TempDir(),
		LogPath: filepath.Join(t.TempDir(), id+".log"),
		Env:     map[string]string{"AF_SESSION_ID": id},
	}
	if err := b.Create(sess); err != nil {
		t.Fatal(err)
	}
	return sess
}

func TestVersionAtLeast(t *testing.T) {
	for _, tc := range []struct {
		have, want string
		ok         bool
	}{
		{"3.2", "3.2", true},
		{"3.2a", "3.2", true},
		{"3.7b", "3.2", true},
		{"3.1c", "3.2", false},
		{"2.9", "3.2", false},
		{"next-3.4", "3.2", true},
		{"weird", "3.2", true}, // unparseable builds pass rather than block
	} {
		if got := versionAtLeast(tc.have, tc.want); got != tc.ok {
			t.Errorf("versionAtLeast(%q, %q) = %v, want %v", tc.have, tc.want, got, tc.ok)
		}
	}
}

func TestCreateLifecycle(t *testing.T) {
	b := testBackend(t)
	sess := testSession(t, b, "lif001", "echo hello-from-tmux; sleep 600")

	if sess.PID <= 0 || sess.PGID <= 0 {
		t.Fatalf("Create must fill PID/PGID, got %d/%d", sess.PID, sess.PGID)
	}
	alive, err := b.IsAlive(sess.ID)
	if err != nil || !alive {
		t.Fatalf("fresh session must be alive: %v/%v", alive, err)
	}
	// The pipe-pane log captures output from birth.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if raw, _ := os.ReadFile(sess.LogPath); strings.Contains(string(raw), "hello-from-tmux") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("log never captured the session's output")
		}
		time.Sleep(50 * time.Millisecond)
	}
	// The rendered screen shows it too.
	screen, err := b.CapturePane(sess.ID, 0)
	if err != nil || !strings.Contains(screen, "hello-from-tmux") {
		t.Fatalf("capture-pane: %q, %v", screen, err)
	}
	if err := b.Kill(sess.ID); err != nil {
		t.Fatal(err)
	}
	if alive, _ := b.IsAlive(sess.ID); alive {
		t.Fatal("killed session must be gone")
	}
}

func TestCreateRollsBackSessionAfterSetupFailure(t *testing.T) {
	fakeDir := t.TempDir()
	statePath := filepath.Join(fakeDir, "session-state")
	fakeTmux := filepath.Join(fakeDir, "tmux")
	fakeScript := `#!/bin/sh
while [ "$#" -gt 0 ]; do
	case "$1" in
		-L|-f) shift 2 ;;
		*) break ;;
	esac
done
case "$1" in
	start-server|pipe-pane) exit 0 ;;
	new-session) : > "$AF_FAKE_TMUX_STATE" ;;
	kill-session) rm -f "$AF_FAKE_TMUX_STATE" ;;
	has-session) test -f "$AF_FAKE_TMUX_STATE" ;;
	list-panes) printf '12345\n' ;;
	*) exit 1 ;;
esac
`
	if err := os.WriteFile(fakeTmux, []byte(fakeScript), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AF_FAKE_TMUX_STATE", statePath)
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	backend := New("fake-socket", 0)
	blockedParent := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockedParent, []byte("block child paths"), 0o600); err != nil {
		t.Fatal(err)
	}
	sess := &core.AgentSession{
		ID:      "rollback001",
		Name:    "rollback",
		Command: "sleep 600",
		WorkDir: t.TempDir(),
		LogPath: filepath.Join(blockedParent, "session.log"),
		Env:     map[string]string{"AF_SESSION_ID": "rollback001"},
	}

	if err := backend.Create(sess); err == nil {
		t.Fatal("Create must fail when it cannot release the payload gate")
	}
	alive, err := backend.IsAlive(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if alive {
		t.Fatal("failed Create left an orphaned tmux session")
	}
}

func TestSendKeysRoundTrip(t *testing.T) {
	b := testBackend(t)
	sess := testSession(t, b, "snd001", `while IFS= read -r l; do echo "got: $l"; done`)

	if err := b.SendKeys(sess.ID, "ping", true); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if screen, _ := b.CapturePane(sess.ID, 0); strings.Contains(screen, "got: ping") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("sent input never echoed back")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// The af server must enable mouse so the wheel reaches the agent TUI
// (agents own their transcript scrollback; without this, scroll events
// never make it to the app). See EnsureServer.
func TestServerEnablesMouse(t *testing.T) {
	b := testBackend(t)
	if err := b.EnsureServer(); err != nil {
		t.Fatal(err)
	}
	out, err := b.run("show-options", "-g", "mouse")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "on") {
		t.Fatalf("af server must enable mouse for TUI scroll; got %q", out)
	}
}

func TestDeadStatusHarvest(t *testing.T) {
	b := testBackend(t)
	// Brief sleep before exiting: a payload that exits instantly can die
	// before tmux finishes wiring up the pane's exit-status tracking, and
	// then pane_dead_status is never captured (seen on tmux 3.4 under CI
	// load). The exiter.sh fixture delays for the same reason.
	sess := testSession(t, b, "ded001", "sleep 0.5; exit 7")

	// remain-on-exit holds the dead pane so the exit code is readable.
	// pane_dead can also flip a beat before pane_dead_status populates, so
	// poll until a real status lands (code != -1) rather than trusting the
	// first "dead" tick.
	deadline := time.Now().Add(10 * time.Second)
	for {
		dead, code, err := b.DeadStatus(sess.ID)
		if err != nil {
			t.Fatal(err)
		}
		if dead && code != -1 {
			if code != 7 {
				t.Fatalf("want exit code 7, got %d", code)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane never reported a settled exit status (last: dead=%v code=%d)", dead, code)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// AttachEnv strips tmux's own environment markers so a nested attach
// (user's tmux -> af session) works instead of tmux refusing with
// "sessions should be nested with care".
func TestAttachEnvStripsTmuxVars(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-1000/default,123,0")
	t.Setenv("TMUX_PANE", "%1")
	t.Setenv("AF_KEEP_ME", "yes")

	env := AttachEnv()
	for _, kv := range env {
		if strings.HasPrefix(kv, "TMUX=") || strings.HasPrefix(kv, "TMUX_PANE=") {
			t.Fatalf("AttachEnv must strip %q", kv)
		}
	}
	if !slices.Contains(env, "AF_KEEP_ME=yes") {
		t.Fatal("AttachEnv must keep unrelated variables")
	}
}

func TestPrepareAttachReturnsSanitizedCommand(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-1000/default,123,0")
	t.Setenv("TMUX_PANE", "%1")
	t.Setenv("AF_KEEP_ME", "yes")

	spec, err := New("attach-socket", 0).PrepareAttach("abc123")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(spec.Path) != "tmux" {
		t.Fatalf("attach executable = %q, want tmux", spec.Path)
	}
	wantArgv := []string{
		"tmux", "-L", "attach-socket", "-f", "/dev/null",
		"set-option", "-w", "-t", "=af-abc123:", "window-size", "latest", ";",
		"attach-session", "-t", "=af-abc123",
	}
	if !slices.Equal(spec.Argv, wantArgv) {
		t.Fatalf("attach argv = %v, want %v", spec.Argv, wantArgv)
	}
	for _, value := range spec.Env {
		if strings.HasPrefix(value, "TMUX=") || strings.HasPrefix(value, "TMUX_PANE=") {
			t.Fatalf("attach environment retained %q", value)
		}
	}
	if !slices.Contains(spec.Env, "AF_KEEP_ME=yes") {
		t.Fatal("attach environment dropped unrelated variables")
	}
}

func TestIsAliveDistinguishesMissingSessionFromCommandFailure(t *testing.T) {
	fakeDir := t.TempDir()
	fakeTmux := filepath.Join(fakeDir, "tmux")
	if err := os.WriteFile(fakeTmux, []byte(`#!/bin/sh
printf 'permission denied\n' >&2
exit "${AF_FAKE_TMUX_EXIT:-1}"
`), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	backend := New("fake-socket", 0)

	t.Setenv("AF_FAKE_TMUX_EXIT", "1")
	if alive, err := backend.IsAlive("missing"); err != nil || alive {
		t.Fatalf("exit 1 must mean missing session, got alive=%v err=%v", alive, err)
	}

	t.Setenv("AF_FAKE_TMUX_EXIT", "2")
	if alive, err := backend.IsAlive("broken"); core.ExitCode(err) != core.ExitEnv || alive {
		t.Fatalf("exit 2 must be an environment error, got alive=%v code=%d err=%v", alive, core.ExitCode(err), err)
	}
}
