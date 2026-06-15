package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newAuthCmd(g *globalFlags) *cobra.Command {
	return &cobra.Command{Use: "auth", RunE: func(*cobra.Command, []string) error { return fmt.Errorf("not implemented") }}
}
