package cli

import (
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/pwn1609/AgentXRay/internal/ingest"
	"github.com/pwn1609/AgentXRay/internal/store"
	"github.com/pwn1609/AgentXRay/internal/tokenize"
	"github.com/spf13/cobra"
)

func newServeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the OTLP receiver and persist runs",
		Long:  "Starts the OTLP trace receiver (gRPC + HTTP) and ingests agent telemetry into the local database.",
		RunE: func(cmd *cobra.Command, args []string) error {
			grpcAddr, _ := cmd.Flags().GetString("grpc")
			httpAddr, _ := cmd.Flags().GetString("http")
			dbPath, _ := cmd.Flags().GetString("db")
			encoding, _ := cmd.Flags().GetString("encoding")
			capture, _ := cmd.Flags().GetBool("capture-content")

			log := slog.New(slog.NewTextHandler(cmd.OutOrStdout(), &slog.HandlerOptions{Level: slog.LevelInfo}))

			st, err := store.Open(dbPath)
			if err != nil {
				return err
			}
			defer st.Close()

			counter, err := tokenize.NewCounter(encoding)
			if err != nil {
				return err
			}

			proc := ingest.NewProcessor(st, counter, capture, log)
			srv := ingest.NewServer(grpcAddr, httpAddr, proc, log)

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			log.Info("agentxray serve starting", "db", dbPath)
			if err := srv.ListenAndServe(ctx); err != nil {
				return err
			}
			log.Info("agentxray serve stopped")
			return nil
		},
	}
	cmd.Flags().String("grpc", ":4317", "OTLP gRPC listen address")
	cmd.Flags().String("http", ":4318", "OTLP HTTP listen address")
	cmd.Flags().String("db", "agentxray.db", "SQLite database file path")
	cmd.Flags().String("encoding", tokenize.DefaultEncoding, "tokenizer encoding for category splitting")
	cmd.Flags().Bool("capture-content", true, "store tool-call arguments and results")
	return cmd
}
