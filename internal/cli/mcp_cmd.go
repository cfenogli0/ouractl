package cli

import (
	"github.com/cfenogli0/ouractl/internal/mcp"
	"github.com/cfenogli0/ouractl/internal/store"
	syncpkg "github.com/cfenogli0/ouractl/internal/sync"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

func newMcpCmd(g *globalFlags, version string) *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Serve MCP over stdio (query, status, sync tools over the local store)",
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
			s, err := store.Open(c.Storage.Path)
			if err != nil {
				return err
			}
			defer s.Close()
			server := mcp.New(mcp.Deps{
				Store: s,
				// Lazy: only the sync tool pays for auth; a broken token
				// store must not take down read-only sessions.
				FetcherFactory: func() (syncpkg.Fetcher, error) { return buildClient(c) },
				Enabled:        sets,
				OverlapDays:    c.Collection.OverlapDays,
				Today:          today,
				Version:        version,
			})
			return server.Run(cmd.Context(), &sdk.StdioTransport{})
		},
	}
}
