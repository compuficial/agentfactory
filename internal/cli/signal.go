package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"agentfactory.sh/af/internal/core"
)

func newSignalCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "signal <session> <state>",
		Short: "Adapter entry point: harnesses report their state to af",
		Long: `Adapter entry point (T2 detection). A harness-side hook calls
af signal "$AF_SESSION_ID" awaiting-input when it blocks on the user;
an agent calls af signal "$AF_SESSION_ID" done when its task is
complete. The next observed output clears the state automatically.`,
		Args: exactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			state := core.Status(args[1])
			if !state.Sticky() {
				return core.Errf(core.ExitUsage, "unknown state %q (valid: %s, %s)",
					args[1], core.StatusAwaitingInput, core.StatusDone)
			}
			app, sess, err := resolveSession(cmd, args[0])
			if err != nil {
				return err
			}
			defer app.Close()
			if sess.Status.Terminal() {
				fmt.Fprintf(cmd.ErrOrStderr(), "session %s is %s; signal ignored\n", sess.ID, sess.Status)
				return nil
			}
			return app.Manager.Signal(sess, state)
		},
	}
}
