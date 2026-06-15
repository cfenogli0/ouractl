package cli

import (
	"fmt"

	"github.com/cfenogli0/ouractl/internal/export"
	"github.com/cfenogli0/ouractl/internal/store"
	"github.com/spf13/cobra"
)

func newExportCmd(g *globalFlags) *cobra.Command {
	var dataset, from, to, format string
	var fields []string
	var all bool
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Read records out of the store as json, ndjson, or csv",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if dataset == "" {
				return fmt.Errorf("--dataset is required")
			}
			if from == "" || to == "" {
				return fmt.Errorf("--from and --to are required (YYYY-MM-DD)")
			}
			c, err := g.load()
			if err != nil {
				return err
			}
			s, err := store.Open(c.Storage.Path)
			if err != nil {
				return err
			}
			defer s.Close()
			return export.Export(s, export.Options{
				Dataset: dataset, From: from, To: to, Format: format, Fields: fields, All: all,
			}, cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&dataset, "dataset", "", "dataset name (e.g. daily_sleep)")
	cmd.Flags().StringVar(&from, "from", "", "start date YYYY-MM-DD")
	cmd.Flags().StringVar(&to, "to", "", "end date YYYY-MM-DD")
	cmd.Flags().StringVar(&format, "format", "json", "output format: json, ndjson, csv")
	cmd.Flags().StringSliceVar(&fields, "fields", nil, "comma-separated top-level field names to project (csv: top-level only; use --all for nested leaves)")
	cmd.Flags().BoolVar(&all, "all", false, "csv: dump every leaf field (dotted paths for nested)")
	return cmd
}
