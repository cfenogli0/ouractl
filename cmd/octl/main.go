package main

import (
	"fmt"
	"os"

	"github.com/cfenogli0/ouractl/internal/cli"
)

var version = "dev"

func main() {
	if err := cli.NewRootCmd(version).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "octl: "+err.Error())
		os.Exit(1)
	}
}
