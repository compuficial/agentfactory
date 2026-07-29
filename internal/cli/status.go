package cli

import (
	"fmt"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"agentfactory.sh/af/internal/core"
)

// tabwriter geometry shared by every column-aligned table the CLI prints.
const (
	tabMinWidth = 2
	tabWidth    = 4
	tabPadding  = 2
)

func newStatusCmd() *cobra.Command {
	var (
		all      bool
		jsonMode bool
	)
	c := &cobra.Command{
		Use:   "status [session]",
		Short: "List sessions or show one in detail",
		Args:  maxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := newApp(cmd)
			if err != nil {
				return err
			}
			defer app.Close()
			if len(args) == 1 {
				sess, resolveErr := app.Manager.ResolveOne(args[0])
				if resolveErr != nil {
					return resolveErr
				}
				if jsonMode {
					return writeJSON(cmd, sess.JSON())
				}
				return printSessionDetail(cmd, sess)
			}
			sessions, err := app.Store.ListSessions(all)
			if err != nil {
				return err
			}
			if jsonMode {
				out := make([]core.SessionJSON, 0, len(sessions))
				for _, s := range sessions {
					out = append(out, s.JSON())
				}
				return writeJSON(cmd, out)
			}
			return printSessionTable(cmd, sessions)
		},
	}
	c.Flags().BoolVar(&all, "all", false, "include exited/failed history")
	c.Flags().BoolVar(&jsonMode, "json", false, "machine-readable output")
	return c
}

func printSessionTable(cmd *cobra.Command, sessions []*core.AgentSession) error {
	w := tabwriter.NewWriter(cmd.OutOrStdout(), tabMinWidth, tabWidth, tabPadding, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tHARNESS\tMODEL\tSTATUS\tUPTIME\tLAST-ACTIVE\tWORKDIR")
	for _, s := range sessions {
		model := s.Model
		if model == "" {
			model = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			s.ID, s.Name, s.Harness, model, core.StatusLabel(s), core.Uptime(s), core.Ago(s.LastActive), core.TildePath(s.WorkDir))
	}
	return w.Flush()
}

func printSessionDetail(cmd *cobra.Command, s *core.AgentSession) error {
	out := cmd.OutOrStdout()
	w := tabwriter.NewWriter(out, tabMinWidth, tabWidth, tabPadding, ' ', 0)
	pair := func(k, v string) { fmt.Fprintf(w, "%s\t%s\n", k, v) }
	pair("ID", s.ID)
	pair("Name", s.Name)
	if s.Definition != "" {
		pair("Definition", s.Definition)
	}
	pair("Harness", s.Harness)
	if s.Model != "" {
		pair("Model", s.Model)
	}
	pair("Status", core.StatusLabel(s))
	pair("Command", s.Command)
	pair("WorkDir", core.TildePath(s.WorkDir))
	pair("PID", fmt.Sprintf("%d (pgid %d)", s.PID, s.PGID))
	pair("Service", strconv.FormatBool(s.Service))
	pair("Log", s.LogPath)
	pair("Started", s.StartedAt.Local().Format(time.RFC3339)+" ("+core.Uptime(s)+")")
	pair("LastActive", core.Ago(s.LastActive))
	if s.EndedAt != nil {
		pair("Ended", s.EndedAt.Local().Format(time.RFC3339))
	}
	for k, v := range s.Metadata {
		pair("Meta."+k, v)
	}
	return w.Flush()
}
