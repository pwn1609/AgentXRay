package cli

import (
	"github.com/spf13/cobra"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Configuration helpers",
	}
	cmd.AddCommand(newConfigValidateCmd())
	return cmd
}

func newConfigValidateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate the config file (ports, thresholds, paths)",
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO(phase-5): load and validate YAML config.
			cmd.Println("config validate: not yet implemented (phase 5)")
			return nil
		},
	}
	cmd.Flags().String("config", "agentxray.yaml", "path to config file")
	return cmd
}
