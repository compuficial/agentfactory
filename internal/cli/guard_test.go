package cli

import (
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

	for _, args := range [][]string{{"dashboard"}, {"attach", "whatever"}} {
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
