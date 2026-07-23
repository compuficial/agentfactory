package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"agentfactory.sh/af/internal/core"
)

func newPeekCmd() *cobra.Command {
	var (
		lines    int
		jsonMode bool
	)
	c := &cobra.Command{
		Use:   "peek <session>",
		Short: "Print the session's current rendered screen",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, sess, err := resolveSession(cmd, args[0])
			if err != nil {
				return err
			}
			defer app.Close()
			if sess.Status.Terminal() {
				return core.Errf(core.ExitRuntime, "session %s is %s; no screen to capture", sess.ID, sess.Status)
			}
			screen, err := app.Backend.CapturePane(sess.ID, lines)
			if err != nil {
				return err
			}
			if jsonMode {
				return writeJSON(cmd, map[string]string{"screen": screen})
			}
			fmt.Fprint(cmd.OutOrStdout(), screen)
			return nil
		},
	}
	c.Flags().IntVar(&lines, "lines", 0, "trailing lines to keep (default: full visible screen)")
	c.Flags().BoolVar(&jsonMode, "json", false, "machine-readable output")
	return c
}
