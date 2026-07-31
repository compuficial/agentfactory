package cli

import (
	"bytes"
	"strings"
	"testing"

	"agentfactory.sh/af/internal/core"
)

// Interactive commands must refuse to run inside an af session: an
// agent running `af dashboard` through its shell recurses the preview
// into a hall of mirrors, and a nested `af attach` can deadlock the
// pane it runs in. AF_SESSION_ID is injected into every session's
// environment, so its presence is the harness-agnostic tell.
func TestInteractiveCommandsRefuseInsideSession(t *testing.T) {
	testEnv(t)
	t.Setenv("AF_SESSION_ID", "zzzzzz")
	// Keep the currently unguarded frictionless path from finding tmux or
	// an agent binary before the regression is fixed.
	t.Setenv("PATH", t.TempDir())

	for _, args := range [][]string{{"dashboard"}, {"attach", "whatever"}, {"claude"}} {
		root := NewRoot()
		root.SetArgs(args)
		err := root.Execute()
		if core.ExitCode(err) != core.ExitRuntime {
			t.Fatalf("%v inside a session: want exit 1, got %v", args, err)
		}
		if !strings.Contains(err.Error(), "inside af session") {
			t.Fatalf("%v: error must explain the nesting refusal, got %q", args, err)
		}
	}
}

func TestNestedAttachHintUsesCommandErrorWriter(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux/default,123,0")
	root := NewRoot()
	var stderr bytes.Buffer
	root.SetErr(&stderr)

	writeNestedAttachHint(root)
	if !strings.Contains(stderr.String(), "detach with C-b twice then d") {
		t.Fatalf("nested attach hint not captured by command stderr: %q", stderr.String())
	}
}

func TestWaitRejectsNegativeTimeout(t *testing.T) {
	testEnv(t)
	t.Setenv("PATH", t.TempDir())
	root := NewRoot()
	root.SetArgs([]string{"wait", "session", "--timeout=-1s"})
	err := root.Execute()
	if core.ExitCode(err) != core.ExitUsage {
		t.Fatalf("negative timeout exit code = %d, want %d: %v", core.ExitCode(err), core.ExitUsage, err)
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("negative timeout error must name timeout, got %q", err)
	}
}

func TestLogsRejectsNegativeLines(t *testing.T) {
	testEnv(t)
	t.Setenv("PATH", t.TempDir())
	root := NewRoot()
	root.SetArgs([]string{"logs", "session", "--lines=-1"})
	err := root.Execute()
	if core.ExitCode(err) != core.ExitUsage {
		t.Fatalf("negative lines exit code = %d, want %d: %v", core.ExitCode(err), core.ExitUsage, err)
	}
	if !strings.Contains(err.Error(), "lines") {
		t.Fatalf("negative lines error must name lines, got %q", err)
	}
}
