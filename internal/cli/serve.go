package cli

import (
	"github.com/spf13/cobra"
)

func newServeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the OTLP receiver and persist runs",
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO(phase-3): start OTLP gRPC/HTTP receiver and ingest loop.
			cmd.Println("serve: not yet implemented (phase 3)")
			return nil
		},
	}
	cmd.Flags().String("grpc", ":4317", "OTLP gRPC listen address")
	cmd.Flags().String("http", ":4318", "OTLP HTTP listen address")
	cmd.Flags().String("db", "agentxray.db", "SQLite database file path")
	return cmd
}
