package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newSyncCmd(g *globalFlags) *cobra.Command {
	return &cobra.Command{Use: "sync", RunE: func(*cobra.Command, []string) error { return fmt.Errorf("not implemented") }}
}

func newBackfillCmd(g *globalFlags) *cobra.Command {
	return &cobra.Command{Use: "backfill", RunE: func(*cobra.Command, []string) error { return fmt.Errorf("not implemented") }}
}
