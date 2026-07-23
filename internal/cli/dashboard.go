package cli

import (
	"github.com/spf13/cobra"

	"agentfactory.sh/af/internal/tui"
)

func newDashboardCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "dashboard",
		Short: "Launch the live TUI dashboard",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := refuseInsideSession("dashboard"); err != nil {
				return err
			}
			app, err := newApp(cmd)
			if err != nil {
				return err
			}
			defer app.Close()
			return tui.Run(tui.Deps{
				Config:  app.Config,
				Store:   app.Store,
				Backend: app.Backend,
				Manager: app.Manager,
				NewRoot: NewRoot,
			})
		},
	}
}
