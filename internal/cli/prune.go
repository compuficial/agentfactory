package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newRmCmd() *cobra.Command {
	var quiet bool
	c := &cobra.Command{
		Use:   "rm <session>...",
		Short: "Remove finished sessions from history (deletes their logs too)",
		Args:  minArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := newApp(cmd)
			if err != nil {
				return err
			}
			defer app.Close()
			for _, ref := range args {
				sess, err := app.Manager.ResolveOne(ref)
				if err != nil {
					return err
				}
				if err := app.Manager.Remove(sess); err != nil {
					return err
				}
				if !quiet {
					fmt.Fprintf(cmd.OutOrStdout(), "removed %s  %s\n", sess.ID, sess.Name)
				}
			}
			return nil
		},
	}
	c.Flags().BoolVarP(&quiet, "quiet", "q", false, "suppress non-essential output")
	return c
}

func newPruneCmd() *cobra.Command {
	var quiet bool
	c := &cobra.Command{
		Use:   "prune",
		Short: "Remove all exited/failed sessions from history",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			app, err := newApp(cmd)
			if err != nil {
				return err
			}
			defer app.Close()
			sessions, err := app.Store.ListSessions(true)
			if err != nil {
				return err
			}
			pruned := 0
			for _, sess := range sessions {
				if !sess.Status.Terminal() {
					continue
				}
				if err := app.Manager.Remove(sess); err != nil {
					return err
				}
				pruned++
			}
			if !quiet {
				fmt.Fprintf(cmd.OutOrStdout(), "pruned %d session(s)\n", pruned)
			}
			return nil
		},
	}
	c.Flags().BoolVarP(&quiet, "quiet", "q", false, "suppress non-essential output")
	return c
}
