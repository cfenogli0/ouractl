package cli

import (
	"fmt"

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"
)

func newConfigCmd(g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "Print the effective merged config and validate it",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := g.load()
			if err != nil {
				return err
			}
			c.Auth.Token = redact(c.Auth.Token)
			c.Auth.ClientSecret = redact(c.Auth.ClientSecret)
			c.Storage.EncryptionKey = redact(c.Storage.EncryptionKey)
			if err := toml.NewEncoder(cmd.OutOrStdout()).Encode(c); err != nil {
				return err
			}
			if err := mustValidate(c); err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "WARNING:", err)
			}
			return nil
		},
	}
}

func redact(s string) string {
	if s == "" {
		return ""
	}
	return "***redacted***"
}
