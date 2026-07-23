package tmux

import (
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
		exec.Command("tmux", "-L", socket, "kill-server").Run()
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

func TestDeadStatusHarvest(t *testing.T) {
	b := testBackend(t)
	sess := testSession(t, b, "ded001", "exit 7")

	// remain-on-exit holds the dead pane so the exit code is readable.
	deadline := time.Now().Add(5 * time.Second)
	for {
		dead, code, err := b.DeadStatus(sess.ID)
		if err != nil {
			t.Fatal(err)
		}
		if dead {
			if code != 7 {
				t.Fatalf("want exit code 7, got %d", code)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("pane never reported dead")
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
