package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// version is overwritten at release time via -ldflags.
var version = "dev"

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "octl",
		Short:         "octl pulls Oura Ring data into a local store and exports it",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
	}
	return root
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "octl: "+err.Error())
		os.Exit(1)
	}
}
