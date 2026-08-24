package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/pwn1609/AgentXRay/internal/model"
	"github.com/pwn1609/AgentXRay/internal/store"
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

func openStoreFromFlags(cmd *cobra.Command) (*store.Store, error) {
	dbPath, _ := cmd.Flags().GetString("db")
	return store.Open(dbPath)
}

func newRunsListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List recent runs with total tokens and status",
		RunE: func(cmd *cobra.Command, args []string) error {
			limit, _ := cmd.Flags().GetInt("limit")
			st, err := openStoreFromFlags(cmd)
			if err != nil {
				return err
			}
			defer st.Close()

			runs, err := st.ListRuns(context.Background(), limit)
			if err != nil {
				return err
			}
			renderRunsList(cmd.OutOrStdout(), runs)
			return nil
		},
	}
	cmd.Flags().Int("limit", 50, "maximum number of runs to show")
	return cmd
}

func newRunsShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <run-id>",
		Short: "Show the full token & tool breakdown for one run",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openStoreFromFlags(cmd)
			if err != nil {
				return err
			}
			defer st.Close()

			run, err := resolveRun(context.Background(), st, args[0])
			if err != nil {
				return err
			}
			renderRunShow(cmd.OutOrStdout(), run)
			return nil
		},
	}
}

// resolveRun looks up a run by exact id, falling back to a unique id prefix.
func resolveRun(ctx context.Context, st *store.Store, idOrPrefix string) (*model.Run, error) {
	if run, err := st.GetRun(ctx, idOrPrefix); err != nil {
		return nil, err
	} else if run != nil {
		return run, nil
	}

	runs, err := st.ListRuns(ctx, 1000)
	if err != nil {
		return nil, err
	}
	var matches []string
	for _, r := range runs {
		if strings.HasPrefix(r.ID, idOrPrefix) {
			matches = append(matches, r.ID)
		}
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("no run found matching %q", idOrPrefix)
	case 1:
		return st.GetRun(ctx, matches[0])
	default:
		return nil, fmt.Errorf("ambiguous run id %q matches %d runs; use more characters", idOrPrefix, len(matches))
	}
}
