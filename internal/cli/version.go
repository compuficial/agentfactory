package cli

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version, commit, and Go version",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "af %s (commit %s, %s)\n", Version, Commit, runtime.Version())
			return nil
		},
	}
}
