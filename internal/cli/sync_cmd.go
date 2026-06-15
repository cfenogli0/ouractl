package cli

import (
	"context"
	"fmt"

	"github.com/cfenogli0/ouractl/internal/auth"
	"github.com/cfenogli0/ouractl/internal/oura"
	"github.com/cfenogli0/ouractl/internal/store"
	syncpkg "github.com/cfenogli0/ouractl/internal/sync"
	"github.com/spf13/cobra"
)

type configForClient struct {
	mode, token string
}

func buildClient(c configForClient) (*oura.Client, error) {
	if c.mode != "token" {
		return nil, fmt.Errorf("only token auth is wired for sync in v1; run `octl auth` for status")
	}
	return oura.New(auth.NewStaticProvider(c.token), ouraBaseURL), nil
}

func newSyncCmd(g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Incrementally pull enabled datasets since each watermark",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := g.load()
			if err != nil {
				return err
			}
			if err := mustValidate(c); err != nil {
				return err
			}
			sets, err := enabledDatasets(c.Collection.Enabled)
			if err != nil {
				return err
			}
			client, err := buildClient(configForClient{c.Auth.Mode, c.Auth.Token})
			if err != nil {
				return err
			}
			s, err := store.Open(c.Storage.Path)
			if err != nil {
				return err
			}
			defer s.Close()
			now := today()
			opts := syncpkg.Options{End: now, Now: now, OverlapDays: c.Collection.OverlapDays, Source: "sync"}
			if err := syncpkg.Run(context.Background(), s, client, sets, opts); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "sync complete: %d datasets through %s\n", len(sets), now)
			return nil
		},
	}
}

func newBackfillCmd(g *globalFlags) *cobra.Command {
	var from, to string
	var days int
	cmd := &cobra.Command{
		Use:   "backfill",
		Short: "Idempotent historical pull over a date range",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := g.load()
			if err != nil {
				return err
			}
			if err := mustValidate(c); err != nil {
				return err
			}
			end := to
			if end == "" {
				end = today()
			}
			start := from
			if start == "" {
				if days <= 0 {
					return fmt.Errorf("backfill needs --from or --days")
				}
				var err error
				start, err = syncpkg.MinusDays(end, days)
				if err != nil {
					return err
				}
			}
			sets, err := enabledDatasets(c.Collection.Enabled)
			if err != nil {
				return err
			}
			client, err := buildClient(configForClient{c.Auth.Mode, c.Auth.Token})
			if err != nil {
				return err
			}
			s, err := store.Open(c.Storage.Path)
			if err != nil {
				return err
			}
			defer s.Close()
			opts := syncpkg.Options{Start: start, End: end, Now: today(),
				OverlapDays: c.Collection.OverlapDays, Source: "backfill"}
			if err := syncpkg.Run(context.Background(), s, client, sets, opts); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "backfill complete: %s to %s, %d datasets\n", start, end, len(sets))
			return nil
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "start date YYYY-MM-DD")
	cmd.Flags().StringVar(&to, "to", "", "end date YYYY-MM-DD (default today)")
	cmd.Flags().IntVar(&days, "days", 0, "backfill the last N days (alternative to --from)")
	return cmd
}
