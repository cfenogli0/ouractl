package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newExportCmd(g *globalFlags) *cobra.Command {
	return &cobra.Command{Use: "export", RunE: func(*cobra.Command, []string) error { return fmt.Errorf("not implemented") }}
}
