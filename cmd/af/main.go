// Command af is the AgentFactory CLI: main() only builds the cobra
// root (internal/cli) and maps errors to the exit-code contract.
package main

import (
	"fmt"
	"os"

	"agentfactory.sh/af/internal/cli"
	"agentfactory.sh/af/internal/core"
)

func main() {
	root := cli.NewRoot()
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "af:", err)
		os.Exit(core.ExitCode(err))
	}
}
