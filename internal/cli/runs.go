package cli

import (
	"github.com/spf13/cobra"
)

func newRunsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runs",
		Short: "Inspect ingested agent runs",
	}
	cmd.PersistentFlags().String("db", "agentxray.db", "SQLite database file path")
	cmd.AddCommand(newRunsListCmd(), newRunsShowCmd())
	return cmd
}

func newRunsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List recent runs with total tokens and status",
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO(phase-5): query store and render table.
			cmd.Println("runs list: not yet implemented (phase 5)")
			return nil
		},
	}
}

func newRunsShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <run-id>",
		Short: "Show the full breakdown for one run",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO(phase-5): query store and render breakdown.
			cmd.Printf("runs show %s: not yet implemented (phase 5)\n", args[0])
			return nil
		},
	}
}
