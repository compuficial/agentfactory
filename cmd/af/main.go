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
