package cli

import (
	"slices"
	"testing"

	"agentfactory.sh/af/internal/core"
)

func TestSessionCommandCompletionsCoverWaitAndSignalStates(t *testing.T) {
	root := NewRoot()
	wait, _, err := root.Find([]string{"wait"})
	if err != nil {
		t.Fatal(err)
	}
	if wait.ValidArgsFunction == nil {
		t.Fatal("wait must complete live session references")
	}

	signal, _, err := root.Find([]string{"signal"})
	if err != nil {
		t.Fatal(err)
	}
	values, _ := signal.ValidArgsFunction(signal, []string{"session"}, "")
	for _, status := range []string{string(core.StatusAwaitingInput), string(core.StatusDone)} {
		if !slices.Contains(values, status) {
			t.Fatalf("signal status completions %v omit %q", values, status)
		}
	}
}

func TestDefineFromCompletesSessions(t *testing.T) {
	root := NewRoot()
	define, _, err := root.Find([]string{"define"})
	if err != nil {
		t.Fatal(err)
	}
	completion, ok := define.GetFlagCompletionFunc("from")
	if !ok || completion == nil {
		t.Fatal("define --from must complete session references")
	}
}
