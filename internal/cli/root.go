// Package cli implements the af command tree. The TUI command bar
// routes through the same root, so every command writes via
// cmd.OutOrStdout()/ErrOrStderr(), never os.Stdout directly.
package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"agentfactory.sh/af/internal/config"
	"agentfactory.sh/af/internal/core"
	"agentfactory.sh/af/internal/tmux"
)

// Set via -ldflags at build time.
var (
	Version = "dev"
	Commit  = "none"
)

// NewRoot builds the af command tree.
func NewRoot() *cobra.Command {
	var opts launchOpts
	root := &cobra.Command{
		Use:   "af [flags] <agent|command> [agent args...]",
		Short: "AgentFactory: a local-first agent session manager on tmux",
		Long: `AgentFactory manages terminal AI agents as tmux sessions.

Run a subcommand (af status, af open, ...), or launch an agent directly:
'af claude' opens (or resumes) a claude session in the current directory
and attaches. Anything after the agent name is passed to it verbatim
('af claude --model opus'); af's own flags go before it ('af --new claude').`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			// args[0] is not a known subcommand (cobra would have
			// dispatched); treat it as a frictionless agent launch.
			app, err := newApp(cmd)
			if err != nil {
				return err
			}
			sess, _, err := resolveLaunch(app, args[0], args[1:], opts)
			if err != nil {
				app.Close()
				return err
			}
			app.Close() // release the DB before exec replaces the process
			return app.Backend.Attach(sess.ID)
		},
	}
	// Flags after the agent name belong to the agent, not af, so stop
	// parsing af flags at the first positional.
	root.Flags().SetInterspersed(false)
	root.Flags().BoolVar(&opts.forceNew, "new", false, "force a new agent session instead of resuming an existing one")
	root.Flags().StringVarP(&opts.workDir, "workdir", "C", "", "working directory for the agent (default: current)")
	root.Flags().StringVar(&opts.name, "name", "", "session name (default: <agent>-<dir>)")
	root.PersistentFlags().String("socket", "", "tmux socket name (default: af)")
	root.PersistentFlags().String("data-dir", "", "db + logs directory (default: ~/.local/share/agentfactory)")
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return core.Errf(core.ExitUsage, "%v", err)
	})
	root.AddCommand(
		newOpenCmd(), newStatusCmd(), newAttachCmd(), newKillCmd(),
		newCloseCmd(), newLogsCmd(), newSendCmd(), newPeekCmd(),
		newRmCmd(), newPruneCmd(), newWaitCmd(),
		newDefineCmd(), newDefsCmd(), newRmDefCmd(), newDashboardCmd(),
		newSignalCmd(), newMCPCmd(), newDoctorCmd(), newVersionCmd(),
	)
	wireCompletions(root)
	return root
}

// writeJSON pretty-prints v to the command's stdout.
func writeJSON(cmd *cobra.Command, v any) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// printOpened reports a freshly opened session (ID only with -q, §8.2).
func printOpened(cmd *cobra.Command, sess *core.AgentSession, quiet bool) {
	if quiet {
		fmt.Fprintln(cmd.OutOrStdout(), sess.ID)
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "%s  %s\n", sess.ID, sess.Name)
	}
}

// resolveSession builds the App and resolves a session reference in one
// step — the opening move of most session commands. On error the App is
// already closed; on success the caller owns Close.
func resolveSession(cmd *cobra.Command, ref string) (*App, *core.AgentSession, error) {
	app, err := newApp(cmd)
	if err != nil {
		return nil, nil, err
	}
	sess, err := app.Manager.ResolveOne(ref)
	if err != nil {
		app.Close()
		return nil, nil, err
	}
	return app, sess, nil
}

// refuseInsideSession blocks interactive commands when running inside
// an af session (AF_SESSION_ID is injected into every session's env):
// a dashboard inside a session recurses its own preview into a hall of
// mirrors, and a nested attach can deadlock the pane it runs in.
// Agents drive sibling sessions with peek/send/wait instead.
func refuseInsideSession(what string) error {
	if id := os.Getenv("AF_SESSION_ID"); id != "" {
		return core.Errf(core.ExitRuntime,
			"running inside af session %s — %s can't nest; use af peek/send/wait from in here, or run %s from a terminal outside af",
			id, what, what)
	}
	return nil
}

// exactArgs is cobra.ExactArgs with exit code 2.
func exactArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) != n {
			return core.Errf(core.ExitUsage, "%s: expected %d argument(s), got %d", cmd.Name(), n, len(args))
		}
		return nil
	}
}

func maxArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) > n {
			return core.Errf(core.ExitUsage, "%s: expected at most %d argument(s), got %d", cmd.Name(), n, len(args))
		}
		return nil
	}
}

func minArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) < n {
			return core.Errf(core.ExitUsage, "%s: expected at least %d argument(s), got %d", cmd.Name(), n, len(args))
		}
		return nil
	}
}

// App is everything a command needs, resolved per invocation
// (daemonless: nothing outlives the command).
type App struct {
	Config  *config.Config
	Store   *core.Store
	Backend *tmux.Backend
	Manager *core.Manager
}

// Close releases the App's store handle; safe on a partially built App.
func (a *App) Close() {
	if a.Store != nil {
		_ = a.Store.Close() // nothing to do with a close error at teardown
	}
}

// loadConfig resolves config with flag > env > file > default precedence
// for the --socket and --data-dir global flags.
func loadConfig(cmd *cobra.Command) (*config.Config, error) {
	cfg, err := config.Load(config.DefaultPath(), os.Getenv)
	if err != nil {
		return nil, err
	}
	if sock, _ := cmd.Flags().GetString("socket"); sock != "" {
		cfg.Socket = sock
	}
	if dir, _ := cmd.Flags().GetString("data-dir"); dir != "" {
		cfg.DataDir = core.ExpandHome(dir)
	}
	for _, w := range cfg.Warnings {
		fmt.Fprintln(cmd.ErrOrStderr(), "warning:", w)
	}
	return cfg, nil
}

// newApp builds the full stack (config, tmux check, store, manager) and
// runs one reconciliation pass. Every command that answers questions
// about sessions goes through here.
func newApp(cmd *cobra.Command) (*App, error) {
	app, err := newStoreApp(cmd)
	if err != nil {
		return nil, err
	}
	if _, err := tmux.CheckTmux(); err != nil {
		app.Close()
		return nil, err
	}
	if err := app.Manager.Reconcile(); err != nil {
		app.Close()
		return nil, err
	}
	return app, nil
}

// newStoreApp builds config + store only (no tmux requirement, no
// reconciliation) for commands that never touch the backend.
func newStoreApp(cmd *cobra.Command) (*App, error) {
	cfg, err := loadConfig(cmd)
	if err != nil {
		return nil, err
	}
	store, err := core.OpenStore(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	backend := tmux.New(cfg.Socket, cfg.SendDelay.D())
	harnesses := harnessSet(cfg)
	warn := func(w string) { fmt.Fprintln(cmd.ErrOrStderr(), "warning:", w) }
	var detect map[string]*core.CompiledDetect
	if cfg.DetectEnabled() {
		detect = core.CompileDetect(harnesses, warn)
	}
	var signals *core.CompiledSignals
	if cfg.SignalsEnabled() {
		signals = core.CompileSignals(cfg.Signals.NotifyAwaiting, warn)
	}
	return &App{
		Config:  cfg,
		Store:   store,
		Backend: backend,
		Manager: &core.Manager{
			Store:         store,
			Backend:       backend,
			Harnesses:     harnesses,
			DataDir:       cfg.DataDir,
			IdleThreshold: cfg.IdleThreshold.D(),
			CloseTimeout:  cfg.CloseTimeout.D(),
			Detect:        detect,
			Signals:       signals,
		},
	}, nil
}

func harnessSet(cfg *config.Config) core.HarnessSet {
	user := map[string]core.Harness{}
	for name, h := range cfg.Harnesses {
		user[name] = core.Harness{
			Name:        name,
			CommandTmpl: h.Command,
			Env:         h.Env,
			QuitKeys:    h.QuitKeys,
			Detect:      h.Detect,
			Files:       h.Files,
		}
	}
	return core.NewHarnessSet(user)
}
