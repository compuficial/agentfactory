package cli

import (
	"github.com/spf13/cobra"

	"agentfactory.sh/af/internal/core"
)

func newAttachCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "attach <session>",
		Short: "Attach to a session with full fidelity (execs into tmux attach)",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := refuseInsideSession("attach"); err != nil {
				return err
			}
			app, sess, err := resolveSession(cmd, args[0])
			if err != nil {
				return err
			}
			if sess.Status.Terminal() {
				app.Close()
				return core.Errf(core.ExitRuntime, "session %s is %s; nothing to attach to", sess.ID, sess.Status)
			}
			app.Close() // release the DB before exec replaces the process
			return app.Backend.Attach(sess.ID)
		},
	}
}
