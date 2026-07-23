package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"agentfactory.sh/af/internal/core"
)

func newDefineCmd() *cobra.Command {
	var (
		harness   string
		model     string
		workdir   string
		envKVs    []string
		configKVs []string
		cmdStr    string
		service   bool
		quiet     bool
		open      bool
	)
	c := &cobra.Command{
		Use:   "define <name>",
		Short: "Create or update (upsert) a reusable agent definition",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// --open touches the backend, so it needs the full app
			// (tmux check + reconciliation); plain define does not.
			newDefineApp := newStoreApp
			if open {
				newDefineApp = newApp
			}
			app, err := newDefineApp(cmd)
			if err != nil {
				return err
			}
			defer app.Close()

			// Upsert: start from the existing definition, apply only the
			// flags that were passed.
			def := &core.AgentDefinition{Name: args[0], Env: map[string]string{}, Config: map[string]string{}}
			if existing, err := app.Store.GetDefinition(args[0]); err == nil {
				def = existing
				if def.Env == nil {
					def.Env = map[string]string{}
				}
				if def.Config == nil {
					def.Config = map[string]string{}
				}
			} else if core.ExitCode(err) != core.ExitNotFound {
				return err
			}
			if cmdStr != "" {
				def.Harness = "custom"
				def.Config["cmd"] = cmdStr
			}
			if cmd.Flags().Changed("harness") {
				def.Harness = harness
			}
			if cmd.Flags().Changed("model") {
				def.Model = model
			}
			if cmd.Flags().Changed("workdir") {
				def.WorkDir = workdir
			}
			if cmd.Flags().Changed("service") {
				def.Service = service
			}
			for _, kv := range envKVs {
				k, v, err := core.ParseKV(kv)
				if err != nil {
					return err
				}
				def.Env[k] = v
			}
			for _, kv := range configKVs {
				k, v, err := core.ParseKV(kv)
				if err != nil {
					return err
				}
				def.Config[k] = v
			}
			if err := app.Manager.Harnesses.ValidateDefinition(def); err != nil {
				return err
			}
			if err := app.Store.PutDefinition(def); err != nil {
				return err
			}
			if !quiet {
				fmt.Fprintf(cmd.OutOrStdout(), "defined %s (harness %s)\n", def.Name, def.Harness)
			}
			if !open {
				return nil
			}
			sess, err := app.Manager.Open(core.OpenRequest{Definition: def.Name})
			if err != nil {
				return err
			}
			printOpened(cmd, sess, quiet)
			return nil
		},
	}
	c.Flags().StringVar(&harness, "harness", "", "harness to launch (required on create, unless --cmd)")
	c.Flags().StringVar(&model, "model", "", "model passthrough")
	c.Flags().StringVarP(&workdir, "workdir", "C", "", "default working directory")
	c.Flags().StringArrayVarP(&envKVs, "env", "e", nil, "environment K=V (repeatable)")
	c.Flags().StringArrayVar(&configKVs, "config", nil, "harness-specific config K=V (repeatable)")
	c.Flags().StringVar(&cmdStr, "cmd", "", "shorthand: harness=custom with this command")
	c.Flags().BoolVar(&service, "service", false, "definitions launch as service sessions")
	c.Flags().BoolVar(&open, "open", false, "immediately open a session from the definition")
	c.Flags().BoolVarP(&quiet, "quiet", "q", false, "suppress non-essential output")
	return c
}

func newDefsCmd() *cobra.Command {
	var jsonMode bool
	c := &cobra.Command{
		Use:   "defs",
		Short: "List agent definitions",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := newStoreApp(cmd)
			if err != nil {
				return err
			}
			defer app.Close()
			defs, err := app.Store.ListDefinitions()
			if err != nil {
				return err
			}
			if jsonMode {
				out := make([]core.DefinitionJSON, 0, len(defs))
				for _, d := range defs {
					out = append(out, d.JSON())
				}
				return writeJSON(cmd, out)
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tHARNESS\tMODEL\tWORKDIR\tSERVICE")
			for _, d := range defs {
				model, workdir := d.Model, d.WorkDir
				if model == "" {
					model = "-"
				}
				if workdir == "" {
					workdir = "-"
				} else {
					workdir = core.TildePath(workdir)
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%v\n", d.Name, d.Harness, model, workdir, d.Service)
			}
			return w.Flush()
		},
	}
	c.Flags().BoolVar(&jsonMode, "json", false, "machine-readable output")
	return c
}

func newRmDefCmd() *cobra.Command {
	var quiet bool
	c := &cobra.Command{
		Use:   "rm-def <name>",
		Short: "Delete a definition (running sessions are unaffected)",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := newStoreApp(cmd)
			if err != nil {
				return err
			}
			defer app.Close()
			if err := app.Store.DeleteDefinition(args[0]); err != nil {
				return err
			}
			if !quiet {
				fmt.Fprintf(cmd.OutOrStdout(), "deleted %s\n", args[0])
			}
			return nil
		},
	}
	c.Flags().BoolVarP(&quiet, "quiet", "q", false, "suppress non-essential output")
	return c
}
