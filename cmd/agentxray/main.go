// Command agentxray is the entrypoint for the AgentXRay CLI.
package main

import (
	"fmt"
	"os"

	"github.com/pwn1609/AgentXRay/internal/cli"
)

func main() {
	if err := cli.NewRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
