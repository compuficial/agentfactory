package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"agentfactory.sh/af/internal/core"
)

func newCloseCmd() *cobra.Command {
	var (
		timeout time.Duration
		quiet   bool
		all     bool
	)
	c := &cobra.Command{
		Use:   "close <session> | --all",
		Short: "Gracefully stop a session (quit keys, then SIGTERM, then SIGKILL)",
		Args:  maxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return stopSessions(cmd, args, all, quiet, "closed", func(app *App, sess *core.AgentSession) error {
				return app.Manager.Close(sess, timeout)
			})
		},
	}
	c.Flags().DurationVar(&timeout, "timeout", 0, "SIGTERM->SIGKILL escalation timeout (default: close_timeout)")
	c.Flags().BoolVar(&all, "all", false, "close every live session")
	c.Flags().BoolVarP(&quiet, "quiet", "q", false, "suppress non-essential output")
	return c
}

func newKillCmd() *cobra.Command {
	var (
		quiet bool
		all   bool
	)
	c := &cobra.Command{
		Use:   "kill <session> | --all",
		Short: "Immediately SIGKILL a session's whole process group",
		Args:  maxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return stopSessions(cmd, args, all, quiet, "killed", func(app *App, sess *core.AgentSession) error {
				return app.Manager.Kill(sess)
			})
		},
	}
	c.Flags().BoolVar(&all, "all", false, "kill every live session")
	c.Flags().BoolVarP(&quiet, "quiet", "q", false, "suppress non-essential output")
	return c
}

// stopSessions runs a close/kill action on one session or on every
// live session with --all.
func stopSessions(cmd *cobra.Command, args []string, all, quiet bool, verb string, stop func(*App, *core.AgentSession) error) error {
	if all == (len(args) == 1) {
		return core.Errf(core.ExitUsage, "%s takes a session or --all", cmd.Name())
	}
	app, err := newApp(cmd)
	if err != nil {
		return err
	}
	defer app.Close()

	var targets []*core.AgentSession
	if all {
		live, err := app.Store.ListSessions(false)
		if err != nil {
			return err
		}
		targets = live
	} else {
		sess, err := app.Manager.ResolveOne(args[0])
		if err != nil {
			return err
		}
		if sess.Status.Terminal() {
			fmt.Fprintf(cmd.ErrOrStderr(), "session %s is already %s\n", sess.ID, sess.Status)
			return nil
		}
		targets = []*core.AgentSession{sess}
	}
	for _, sess := range targets {
		if err := stop(app, sess); err != nil {
			return err
		}
		if !quiet {
			// stop() harvests in place; -1 means killed by signal.
			if sess.ExitCode != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "%s %s  %s  exit %d\n", verb, sess.ID, sess.Name, *sess.ExitCode)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "%s %s  %s\n", verb, sess.ID, sess.Name)
			}
		}
	}
	if all && len(targets) == 0 && !quiet {
		fmt.Fprintln(cmd.OutOrStdout(), "no live sessions")
	}
	return nil
}
