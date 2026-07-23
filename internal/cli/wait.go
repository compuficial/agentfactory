package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"agentfactory.sh/af/internal/core"
)

func newWaitCmd() *cobra.Command {
	var (
		forCSV   string
		timeout  time.Duration
		interval time.Duration
		jsonOut  bool
		quiet    bool
	)
	cmd := &cobra.Command{
		Use:   "wait <session>",
		Short: "Block until a session reaches a target status",
		Long: `Poll (one reconciliation pass per tick, like the dashboard) until the
session reaches one of the --for statuses. The default target set means
"the agent stopped working". Exit codes: 0 target reached, 1 session
ended in a terminal status outside --for, 5 timeout.`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			targets := map[core.Status]bool{}
			for _, name := range strings.Split(forCSV, ",") {
				status, err := core.ParseStatus(strings.TrimSpace(name))
				if err != nil {
					return err
				}
				targets[status] = true
			}
			app, sess, err := resolveSession(cmd, args[0])
			if err != nil {
				return err
			}
			defer app.Close()
			sess, outcome, err := app.Manager.Wait(sess.ID, targets, timeout, interval)
			if err != nil {
				return err
			}
			switch {
			case quiet:
			case jsonOut:
				if err := writeJSON(cmd, sess.JSON()); err != nil {
					return err
				}
			default:
				fmt.Fprintln(cmd.OutOrStdout(), sess.Status)
			}
			switch outcome {
			case core.WaitTerminal:
				return core.Errf(core.ExitRuntime, "session %s ended %s before reaching %s", sess.ID, sess.Status, forCSV)
			case core.WaitTimeout:
				return core.Errf(core.ExitTimeout, "timed out after %s waiting for %s (status %s)", timeout, forCSV, sess.Status)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&forCSV, "for", "idle,awaiting-input,done", "comma-separated target statuses")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "give up after this long (0 = wait forever)")
	cmd.Flags().DurationVar(&interval, "interval", time.Second, "poll interval (floor 50ms)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print the final session as JSON")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "print nothing on stdout")
	return cmd
}
