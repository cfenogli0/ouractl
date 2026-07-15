// Package mcp serves MCP tools (query, status, sync) over the octl store.
// It is a thin adapter: query formatting is export.Export, freshness is
// store.Stats, sync is sync.Run. Client/auth construction stays in the CLI
// and arrives here only as an opaque fetcher factory, so read-only sessions
// never touch auth.
package mcp

import (
	"fmt"

	"github.com/cfenogli0/ouractl/internal/datasets"
	"github.com/cfenogli0/ouractl/internal/store"
	syncpkg "github.com/cfenogli0/ouractl/internal/sync"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const defaultRowLimit = 200

// Deps is everything the server needs. FetcherFactory is called lazily, only
// by the sync tool, so a broken token store cannot take down read-only use.
type Deps struct {
	Store          *store.Store
	FetcherFactory func() (syncpkg.Fetcher, error)
	Enabled        []datasets.Dataset // sync default set (config collection.enabled)
	OverlapDays    int
	Today          func() string // YYYY-MM-DD; injectable for tests
	Version        string
}

// New builds the MCP server with the query, status, and sync tools registered.
func New(d Deps) *sdk.Server {
	server := sdk.NewServer(&sdk.Implementation{Name: "octl", Version: d.Version}, nil)
	registerTools(server, d)
	return server
}

// namesToDatasets resolves dataset names against the registry, erroring on
// unknown names with the valid list so a typo fails loud.
func namesToDatasets(names []string) ([]datasets.Dataset, error) {
	out := make([]datasets.Dataset, 0, len(names))
	for _, n := range names {
		ds, ok := datasets.Get(n)
		if !ok {
			return nil, fmt.Errorf("unknown dataset %q (valid: %v)", n, datasets.All())
		}
		out = append(out, ds)
	}
	return out, nil
}
