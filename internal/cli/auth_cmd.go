package cli

import (
	"context"
	"fmt"

	"github.com/cfenogli0/ouractl/internal/auth"
	"github.com/cfenogli0/ouractl/internal/config"
	"github.com/cfenogli0/ouractl/internal/oura"
	"github.com/spf13/cobra"
)

const ouraBaseURL = "https://api.ouraring.com/v2/usercollection"

func newAuthCmd(g *globalFlags) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
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
				if path, err := g.persistAuthSettings(c); err == nil {
					fmt.Fprintf(cmd.OutOrStdout(), "token OK; account id: %v\nsaved auth mode to %s\n", info["id"], path)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "token OK; account id: %v\n", info["id"])
				}
				return nil
			case "oauth2":
				store, err := buildTokenStore(c)
				if err != nil {
					return err
				}
				if err := guardReauth(store, force); err != nil {
					return err
				}
				toks, err := auth.RunBrowserFlow(context.Background(), auth.FlowConfig{
					ClientID:     c.Auth.ClientID,
					ClientSecret: c.Auth.ClientSecret,
					RedirectURI:  c.Auth.RedirectURI, // empty -> DefaultRedirectURI
					Scope:        c.Auth.Scope,
				})
				if err != nil {
					return fmt.Errorf("oauth2 authorization failed: %w", err)
				}
				if err := store.Save(toks); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "oauth2 authorized; tokens stored via %s backend\n", backendName(c))
				path, err := g.persistAuthSettings(c)
				if err != nil {
					return fmt.Errorf("authorized, but failed to save auth mode to config (re-run with --auth-mode oauth2, or set [auth] mode): %w", err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "saved auth settings to %s (mode, client_id, token_backend)\n", path)
				if c.Auth.ClientSecret != "" {
					fmt.Fprintln(cmd.OutOrStdout(), "note: client_secret is NOT saved to config; set OURA_CLIENT_SECRET in your environment so `octl sync` can refresh the token")
				}
				return nil
			default:
				return fmt.Errorf("unknown auth mode %q", c.Auth.Mode)
			}
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing token store even if it can't be read with the current key")
	return cmd
}

func backendName(c config.Config) string {
	if c.Storage.TokenBackend == "" {
		return "file"
	}
	return c.Storage.TokenBackend
}
