package cli

import (
	"context"
	"fmt"

	"github.com/cfenogli0/ouractl/internal/auth"
	"github.com/cfenogli0/ouractl/internal/oura"
	"github.com/spf13/cobra"
)

const ouraBaseURL = "https://api.ouraring.com/v2/usercollection"

func newAuthCmd(g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "auth",
		Short: "Set up and verify Oura credentials",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := g.load()
			if err != nil {
				return err
			}
			if err := mustValidate(c); err != nil {
				return err
			}
			switch c.Auth.Mode {
			case "token":
				client := oura.New(auth.NewStaticProvider(c.Auth.Token), ouraBaseURL)
				info, err := client.GetOne(context.Background(), "personal_info")
				if err != nil {
					return fmt.Errorf("token validation failed: %w", err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "token OK; account id: %v\n", info["id"])
				return nil
			case "oauth2":
				return fmt.Errorf("oauth2 interactive flow is not yet wired; for v1 use a personal access token (auth mode token)")
			default:
				return fmt.Errorf("unknown auth mode %q", c.Auth.Mode)
			}
		},
	}
}
