package cli

import (
	"github.com/pwn1609/AgentXRay/internal/config"
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
		Short: "Validate the config file (ports, paths, tokenizer)",
		RunE: func(cmd *cobra.Command, args []string) error {
			path, _ := cmd.Flags().GetString("config")
			cfg, err := config.Load(path)
			if err != nil {
				return err
			}
			if err := cfg.Validate(); err != nil {
				return err
			}
			cmd.Printf("ok: %s is valid\n", path)
			cmd.Printf("  otlp.grpc=%s otlp.http=%s db.path=%s tokenizer.encoding=%s capture_content=%v\n",
				cfg.OTLP.GRPC, cfg.OTLP.HTTP, cfg.DB.Path, cfg.Tokenizer.Encoding, cfg.Ingest.CaptureContent)
			return nil
		},
	}
	cmd.Flags().String("config", "config/agentxray.example.yaml", "path to config file")
	return cmd
}
