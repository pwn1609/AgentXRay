package cli

import (
	"fmt"
	"os"

	"github.com/pwn1609/AgentXRay/internal/ingest"
	"github.com/spf13/cobra"
)

func newReplayCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "replay <file.json>...",
		Short: "Replay OTLP/JSON trace fixtures into a running receiver",
		Long: "Reads one or more OTLP/JSON ExportTraceServiceRequest files and posts them " +
			"to a running `agentxray serve` receiver. Useful for seeding demo data and " +
			"reproducing runs from saved fixtures.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			endpoint, _ := cmd.Flags().GetString("endpoint")
			for _, path := range args {
				data, err := os.ReadFile(path)
				if err != nil {
					return fmt.Errorf("read %s: %w", path, err)
				}
				req, err := ingest.DecodeTracesJSON(data)
				if err != nil {
					return fmt.Errorf("%s: %w", path, err)
				}
				if err := ingest.PostTraces(cmd.Context(), endpoint, req); err != nil {
					return fmt.Errorf("%s: %w", path, err)
				}
				cmd.Printf("replayed %s -> %s\n", path, endpoint)
			}
			return nil
		},
	}
	cmd.Flags().String("endpoint", "http://localhost:4318", "OTLP/HTTP endpoint of a running receiver")
	return cmd
}
