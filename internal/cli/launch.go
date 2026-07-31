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
	def, definitionErr := app.Store.GetDefinition(token)
	if definitionErr == nil {
		wd := opts.workDir
		if wd == "" {
			wd = def.WorkDir
		}
		workDir, resolveErr := core.ResolveWorkDir(wd)
		if resolveErr != nil {
			return nil, false, resolveErr
		}
		if sess, resumeErr := resume(app, def.Harness, workDir, token, opts.forceNew); resumeErr != nil || sess != nil {
			return sess, sess != nil, resumeErr
		}
		sess, openErr := app.Manager.Open(core.OpenRequest{
			Definition: token, Name: opts.name, WorkDir: workDir, ExtraArgs: extra,
		})
		return sess, false, openErr
	}
	if core.ExitCode(definitionErr) != core.ExitNotFound {
		return nil, false, definitionErr
	}

	// 2. A harness by binary name, verified to be installed.
	harness, ok := app.Manager.Harnesses.MatchBinary(token)
	if !ok {
		return nil, false, core.Errf(core.ExitUsage,
			"unknown command, definition, or agent %q — see 'af --help', or 'af open --cmd %q' to run it as a session", token, token)
	}
	if _, lookPathErr := exec.LookPath(token); lookPathErr != nil {
		return nil, false, core.Errf(core.ExitEnv, "%s (harness %s) is not installed or not on PATH", token, harness.Name)
	}
	workDir, resolveErr := core.ResolveWorkDir(opts.workDir)
	if resolveErr != nil {
		return nil, false, resolveErr
	}
	if sess, resumeErr := resume(app, harness.Name, workDir, "", opts.forceNew); resumeErr != nil || sess != nil {
		return sess, sess != nil, resumeErr
	}
	name := opts.name
	if name == "" {
		name = token + "-" + filepath.Base(workDir)
	}
	sess, openErr := app.Manager.Open(core.OpenRequest{
		Harness: harness.Name, Name: name, WorkDir: workDir, ExtraArgs: extra,
	})
	return sess, false, openErr
}

// resume returns a live session to reattach to for (harness, workDir),
// or nil to signal "start a new one". Always nil when forceNew.
func resume(app *App, harness, workDir, definition string, forceNew bool) (*core.AgentSession, error) {
	if forceNew {
		return nil, nil
	}
	return app.Manager.LiveMatch(harness, workDir, definition)
}
