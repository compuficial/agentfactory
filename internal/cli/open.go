package cli

import (
	"github.com/spf13/cobra"

	"agentfactory.sh/af/internal/core"
)

func newOpenCmd() *cobra.Command {
	var (
		name    string
		harness string
		model   string
		workdir string
		envKVs  []string
		cmdStr  string
		service bool
		quiet   bool
	)
	c := &cobra.Command{
		Use:   "open [definition...]",
		Short: "Start one or more sessions from definitions (or ad-hoc)",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 && (name != "" || cmdStr != "") {
				return core.Errf(core.ExitUsage, "--name/--cmd cannot apply to multiple definitions")
			}
			app, err := newApp(cmd)
			if err != nil {
				return err
			}
			defer app.Close()
			env := map[string]string{}
			for _, kv := range envKVs {
				k, v, err := core.ParseKV(kv)
				if err != nil {
					return err
				}
				env[k] = v
			}
			base := core.OpenRequest{
				Name:    name,
				Harness: harness,
				Model:   model,
				WorkDir: workdir,
				Env:     env,
				Cmd:     cmdStr,
				Service: service,
			}
			defs := args
			if len(defs) == 0 {
				defs = []string{""} // fully ad-hoc
			}
			for _, def := range defs {
				req := base
				req.Definition = def
				sess, err := app.Manager.Open(req)
				if err != nil {
					return err // sessions opened so far stay up
				}
				printOpened(cmd, sess, quiet)
			}
			return nil
		},
	}
	c.Flags().StringVar(&name, "name", "", "session name (default: definition or harness name)")
	c.Flags().StringVar(&harness, "harness", "", "harness to launch (required if ad-hoc, unless --cmd)")
	c.Flags().StringVar(&model, "model", "", "model passthrough override")
	c.Flags().StringVarP(&workdir, "workdir", "C", "", "working directory (must exist)")
	c.Flags().StringArrayVarP(&envKVs, "env", "e", nil, "environment override K=V (repeatable)")
	c.Flags().StringVar(&cmdStr, "cmd", "", "shorthand: harness=custom with this command")
	c.Flags().BoolVar(&service, "service", false, "mark as a service session (infra, not an agent)")
	c.Flags().BoolVarP(&quiet, "quiet", "q", false, "print only the session ID")
	return c
}
