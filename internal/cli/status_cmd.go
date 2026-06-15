package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/cfenogli0/ouractl/internal/store"
	"github.com/spf13/cobra"
)

func newStatusCmd(g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show auth mode, per-dataset last sync, and row counts",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := g.load()
			if err != nil {
				return err
			}
			s, err := store.Open(c.Storage.Path)
			if err != nil {
				return err
			}
			defer s.Close()
			stats, err := s.Stats()
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "auth mode: %s\ndb: %s\n\n", c.Auth.Mode, c.Storage.Path)
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "DATASET\tROWS\tLAST SYNCED\tLAST RUN")
			for _, st := range stats {
				fmt.Fprintf(tw, "%s\t%d\t%s\t%s\n", st.Dataset, st.Rows, st.LastSyncedDay, st.LastRunAt)
			}
			return tw.Flush()
		},
	}
}
