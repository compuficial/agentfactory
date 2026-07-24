package cli

import (
	"os/exec"
	"path/filepath"

	"agentfactory.sh/af/internal/core"
)

// launchOpts are af's own flags for the frictionless `af <agent>` form
// (they precede the agent name on the command line).
type launchOpts struct {
	forceNew bool
	workDir  string
	name     string
}

// resolveLaunch implements `af <token> [args...]`: resolve the token to a
// definition or a harness (by binary name), then resume a matching live
// session or start a new one. It does NOT attach — the caller does — so
// the resolution and lifecycle stay testable. Returns whether an
// existing session was resumed.
func resolveLaunch(app *App, token string, extra []string, opts launchOpts) (*core.AgentSession, bool, error) {
	// 1. An explicit definition wins over the generic harness.
	if def, err := app.Store.GetDefinition(token); err == nil {
		wd := opts.workDir
		if wd == "" {
			wd = def.WorkDir
		}
		workDir, err := core.ResolveWorkDir(wd)
		if err != nil {
			return nil, false, err
		}
		if sess, err := resume(app, def.Harness, workDir, opts.forceNew); err != nil || sess != nil {
			return sess, sess != nil, err
		}
		sess, err := app.Manager.Open(core.OpenRequest{
			Definition: token, Name: opts.name, WorkDir: workDir, ExtraArgs: extra,
		})
		return sess, false, err
	}

	// 2. A harness by binary name, verified to be installed.
	harness, ok := app.Manager.Harnesses.MatchBinary(token)
	if !ok {
		return nil, false, core.Errf(core.ExitUsage,
			"unknown command, definition, or agent %q — see 'af --help', or 'af open --cmd %q' to run it as a session", token, token)
	}
	if _, err := exec.LookPath(token); err != nil {
		return nil, false, core.Errf(core.ExitEnv, "%s (harness %s) is not installed or not on PATH", token, harness.Name)
	}
	workDir, err := core.ResolveWorkDir(opts.workDir)
	if err != nil {
		return nil, false, err
	}
	if sess, err := resume(app, harness.Name, workDir, opts.forceNew); err != nil || sess != nil {
		return sess, sess != nil, err
	}
	name := opts.name
	if name == "" {
		name = token + "-" + filepath.Base(workDir)
	}
	sess, err := app.Manager.Open(core.OpenRequest{
		Harness: harness.Name, Name: name, WorkDir: workDir, ExtraArgs: extra,
	})
	return sess, false, err
}

// resume returns a live session to reattach to for (harness, workDir),
// or nil to signal "start a new one". Always nil when forceNew.
func resume(app *App, harness, workDir string, forceNew bool) (*core.AgentSession, error) {
	if forceNew {
		return nil, nil
	}
	return app.Manager.LiveMatch(harness, workDir)
}
