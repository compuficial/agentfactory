package cli

import (
	"os"
	"strings"
	"testing"

	"agentfactory.sh/af/internal/config"
	"agentfactory.sh/af/internal/core"
	"agentfactory.sh/af/internal/tmux"
)

// launchTestApp builds an App on the throwaway socket/data-dir set by
// testEnv, with a demo harness whose binary ("sh") exists on PATH so the
// launcher's install check passes. `af sh` then launches the quiet
// fixture — enough to exercise resolution, resume, naming, and
// passthrough without attaching (which would replace the test process).
func launchTestApp(t *testing.T) *App {
	t.Helper()
	cfg, err := config.Load("", os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	store, err := core.OpenStore(cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	backend := tmux.New(cfg.Socket, cfg.SendDelay.D())
	harnesses := core.NewHarnessSet(map[string]core.Harness{
		"demo": {CommandTmpl: "sh " + fixture(t, "quiet.sh"), QuitKeys: nil},
	})
	return &App{Config: cfg, Store: store, Backend: backend, Manager: &core.Manager{
		Store: store, Backend: backend, Harnesses: harnesses,
		DataDir: cfg.DataDir, IdleThreshold: cfg.IdleThreshold.D(), CloseTimeout: cfg.CloseTimeout.D(),
	}}
}

func TestLaunchResolvesResumesAndPassesArgs(t *testing.T) {
	testEnv(t)
	if _, err := tmux.CheckTmux(); err != nil {
		t.Skipf("tmux unavailable: %v", err)
	}
	app := launchTestApp(t)

	// First launch: the "sh" binary resolves to the demo harness, a new
	// session is started (not resumed), named <token>-<dir>, with the
	// passthrough args appended to the rendered command.
	sess, resumed, err := resolveLaunch(app, "sh", []string{"--flag", "a b"}, launchOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if resumed {
		t.Fatal("first launch must start, not resume")
	}
	if sess.Harness != "demo" {
		t.Fatalf("harness = %q, want demo", sess.Harness)
	}
	wantName := "sh-" + baseName(sess.WorkDir)
	if sess.Name != wantName {
		t.Fatalf("name = %q, want %q", sess.Name, wantName)
	}
	if !strings.HasSuffix(sess.Command, `'--flag' 'a b'`) {
		t.Fatalf("passthrough not appended: %q", sess.Command)
	}

	// Second launch in the same (harness, cwd): resumes the same session.
	again, resumed, err := resolveLaunch(app, "sh", nil, launchOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !resumed || again.ID != sess.ID {
		t.Fatalf("second launch must resume %s, got %s (resumed=%v)", sess.ID, again.ID, resumed)
	}

	// --new forces a distinct session.
	fresh, resumed, err := resolveLaunch(app, "sh", nil, launchOpts{forceNew: true})
	if err != nil {
		t.Fatal(err)
	}
	if resumed || fresh.ID == sess.ID {
		t.Fatalf("--new must start a fresh session, got %s (resumed=%v)", fresh.ID, resumed)
	}

	// Unknown token: usage error (preserves the old unknown-command exit).
	if _, _, err := resolveLaunch(app, "definitely-not-a-thing", nil, launchOpts{}); core.ExitCode(err) != core.ExitUsage {
		t.Fatalf("unknown token: want exit 2, got %v", err)
	}
}

func TestResolveLaunchPropagatesDefinitionLookupFailure(t *testing.T) {
	testEnv(t)
	app := launchTestApp(t)
	if err := app.Store.Close(); err != nil {
		t.Fatal(err)
	}

	_, _, err := resolveLaunch(app, "sh", nil, launchOpts{})
	if err == nil {
		t.Fatal("closed store must make definition lookup fail")
	}
	if !strings.Contains(err.Error(), "load definition") {
		t.Fatalf("definition lookup failure was replaced by fallback error: %v", err)
	}
}

func baseName(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}
