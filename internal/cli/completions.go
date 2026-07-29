package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"agentfactory.sh/af/internal/core"
)

// wireCompletions attaches dynamic shell completion: session IDs/names
// for <session> args, definition names for af open / rm-def, and
// harness names for --harness. Completion runs as its own af
// invocation (`af __complete ...`), so it reads the store directly and
// skips reconciliation to stay instant.
func wireCompletions(root *cobra.Command) {
	liveSessions := completeSessions(false)
	allSessions := completeSessions(true)
	noComp := cobra.NoFileCompletions

	perCommand := map[string]func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective){
		"attach": liveSessions,
		"kill":   liveSessions,
		"close":  liveSessions,
		"send":   liveSessions,
		"peek":   liveSessions,
		"status": allSessions,
		"logs":   allSessions,
		"open":   completeDefinitions,
		"rm-def": completeDefinitions,
		"rm":     completeTerminalSessions,
		"define": noComp,
		"signal": func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) == 1 {
				return []string{string(core.StatusAwaitingInput)}, cobra.ShellCompDirectiveNoFileComp
			}
			return liveSessions(cmd, args, toComplete)
		},
	}
	for _, c := range root.Commands() {
		if fn, ok := perCommand[c.Name()]; ok {
			c.ValidArgsFunction = fn
		}
		if c.Flags().Lookup("harness") != nil {
			_ = c.RegisterFlagCompletionFunc("harness", completeHarnesses)
		}
	}
}

func completeSessions(all bool) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		app, err := newStoreApp(cmd)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		defer app.Close()
		sessions, err := app.Store.ListSessions(all)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var out []string
		for _, s := range sessions {
			detail := fmt.Sprintf("%s · %s · %s", s.ID, s.Harness, core.StatusLabel(s))
			out = append(out, s.Name+"\t"+detail, s.ID+"\t"+s.Name)
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}

// completeTerminalSessions suggests exited/failed sessions (af rm takes
// several; names already on the line are dropped).
func completeTerminalSessions(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	app, err := newStoreApp(cmd)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	defer app.Close()
	sessions, err := app.Store.ListSessions(true)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	typed := map[string]bool{}
	for _, a := range args {
		typed[a] = true
	}
	var out []string
	for _, s := range sessions {
		if !s.Status.Terminal() || typed[s.Name] || typed[s.ID] {
			continue
		}
		detail := fmt.Sprintf("%s · %s · %s", s.ID, s.Harness, core.StatusLabel(s))
		out = append(out, s.Name+"\t"+detail, s.ID+"\t"+s.Name)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// completeDefinitions suggests definition names; af open takes several,
// so names already on the line are dropped from the suggestions.
func completeDefinitions(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	app, err := newStoreApp(cmd)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	defer app.Close()
	defs, err := app.Store.ListDefinitions()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	typed := map[string]bool{}
	for _, a := range args {
		typed[a] = true
	}
	var out []string
	for _, d := range defs {
		if typed[d.Name] {
			continue
		}
		detail := d.Harness
		if d.Model != "" {
			detail += " · " + d.Model
		}
		out = append(out, d.Name+"\t"+detail)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

func completeHarnesses(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	cfg, err := loadConfig(cmd)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return harnessSet(cfg).Names(), cobra.ShellCompDirectiveNoFileComp
}
