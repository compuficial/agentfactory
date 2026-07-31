package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"agentfactory.sh/af/internal/core"
)

// defineFlags are the definition fields settable from the command line.
type defineFlags struct {
	harness   string
	model     string
	workdir   string
	envKVs    []string
	configKVs []string
	cmdStr    string
	service   bool
}

func newDefineCmd() *cobra.Command {
	var (
		f       defineFlags
		fromRef string
		quiet   bool
		open    bool
	)
	c := &cobra.Command{
		Use:   commandDefine + " <name>",
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

			def, err := loadOrNewDefinition(app, args[0])
			if err != nil {
				return err
			}
			// --from seeds the definition from a live session's config
			// (harness/model/workdir/env/cmd); explicit flags below still
			// win, so you can capture-then-tweak in one command.
			if fromRef != "" {
				sess, resolveErr := app.Manager.ResolveOne(fromRef)
				if resolveErr != nil {
					return resolveErr
				}
				def.SeedFromSession(sess)
			}
			if applyErr := f.apply(cmd, def); applyErr != nil {
				return applyErr
			}
			if validateErr := app.Manager.Harnesses.ValidateDefinition(def); validateErr != nil {
				return validateErr
			}
			if putErr := app.Store.PutDefinition(def); putErr != nil {
				return putErr
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
	c.Flags().StringVar(&f.harness, "harness", "", "harness to launch (required on create, unless --cmd)")
	c.Flags().StringVar(&f.model, "model", "", "model passthrough")
	c.Flags().StringVarP(&f.workdir, "workdir", "C", "", "default working directory")
	c.Flags().StringArrayVarP(&f.envKVs, "env", "e", nil, "environment K=V (repeatable)")
	c.Flags().StringArrayVar(&f.configKVs, "config", nil, "harness-specific config K=V (repeatable)")
	c.Flags().StringVar(&f.cmdStr, "cmd", "", "shorthand: harness=custom with this command")
	c.Flags().StringVar(&fromRef, "from", "", "seed fields from an existing session (name or id)")
	c.Flags().BoolVar(&f.service, "service", false, "definitions launch as service sessions")
	c.Flags().BoolVar(&open, "open", false, "immediately open a session from the definition")
	c.Flags().BoolVarP(&quiet, "quiet", "q", false, "suppress non-essential output")
	return c
}

// loadOrNewDefinition returns the stored definition for name (upsert:
// edits layer onto it) or a fresh one when none exists yet.
func loadOrNewDefinition(app *App, name string) (*core.AgentDefinition, error) {
	existing, err := app.Store.GetDefinition(name)
	if err != nil {
		if core.ExitCode(err) != core.ExitNotFound {
			return nil, err
		}
		return &core.AgentDefinition{Name: name, Env: map[string]string{}, Config: map[string]string{}}, nil
	}
	if existing.Env == nil {
		existing.Env = map[string]string{}
	}
	if existing.Config == nil {
		existing.Config = map[string]string{}
	}
	return existing, nil
}

// apply overlays onto def only the flags that were passed, so an upsert
// leaves unmentioned fields alone.
func (f *defineFlags) apply(cmd *cobra.Command, def *core.AgentDefinition) error {
	if f.cmdStr != "" {
		def.Harness = "custom"
		def.Config["cmd"] = f.cmdStr
	}
	if cmd.Flags().Changed("harness") {
		def.Harness = f.harness
	}
	if cmd.Flags().Changed("model") {
		def.Model = f.model
	}
	if cmd.Flags().Changed("workdir") {
		def.WorkDir = f.workdir
	}
	if cmd.Flags().Changed("service") {
		def.Service = f.service
	}
	for _, kv := range f.envKVs {
		k, v, err := core.ParseKV(kv)
		if err != nil {
			return err
		}
		def.Env[k] = v
	}
	for _, kv := range f.configKVs {
		k, v, err := core.ParseKV(kv)
		if err != nil {
			return err
		}
		def.Config[k] = v
	}
	return nil
}

func newDefsCmd() *cobra.Command {
	var jsonMode bool
	c := &cobra.Command{
		Use:   "defs",
		Short: "List agent definitions",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
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
			w := tabwriter.NewWriter(cmd.OutOrStdout(), tabMinWidth, tabWidth, tabPadding, ' ', 0)
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
