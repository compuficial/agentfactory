package cli

import (
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newSendCmd() *cobra.Command {
	var (
		noEnter bool
		delay   time.Duration
	)
	// send takes <session> plus at least one word of text.
	const sendMinArgs = 2
	c := &cobra.Command{
		Use:   "send <session> <text...>",
		Short: "Inject input into a session without attaching",
		Args:  minArgs(sendMinArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, sess, err := resolveSession(cmd, args[0])
			if err != nil {
				return err
			}
			defer app.Close()
			if cmd.Flags().Changed("delay") {
				app.Backend.SendDelay = delay
			}
			text := strings.Join(args[1:], " ")
			return app.Manager.Send(sess, text, !noEnter)
		},
	}
	c.Flags().BoolVar(&noEnter, "no-enter", false, "do not send a trailing Enter")
	c.Flags().DurationVar(&delay, "delay", 0, "gap between literal text and Enter (default: send_delay)")
	return c
}
