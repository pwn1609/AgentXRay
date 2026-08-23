// Package cli wires up the agentxray command surface (cobra).
package cli

import (
	"github.com/spf13/cobra"
)

// Version is the build version, overridable via -ldflags.
var Version = "0.1.0-dev"

// NewRootCmd builds the root cobra command with all subcommands attached.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "agentxray",
		Short:         "AgentXRay — token & tool-call observability for agents",
		Long:          "AgentXRay ingests agent run telemetry (OTLP) and surfaces token breakdowns and tool-call statistics.",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(
		newServeCmd(),
		newRunsCmd(),
		newConfigCmd(),
	)
	return root
}
