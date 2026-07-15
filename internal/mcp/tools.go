package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cfenogli0/ouractl/internal/datasets"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// toolError returns a model-visible tool failure. Domain failures always take
// this path; a non-nil Go error from a handler is reserved for SDK/protocol
// plumbing.
func toolError(format string, a ...any) *sdk.CallToolResult {
	return &sdk.CallToolResult{
		IsError: true,
		Content: []sdk.Content{&sdk.TextContent{Text: fmt.Sprintf(format, a...)}},
	}
}

func textResult(s string) *sdk.CallToolResult {
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: s}}}
}

func registerTools(server *sdk.Server, d Deps) {
	registerStatus(server, d)
}

type statusIn struct{}

type statusRow struct {
	Dataset       string `json:"dataset"`
	Rows          int    `json:"rows"`
	LastSyncedDay string `json:"last_synced_day"`
	LastRunAt     string `json:"last_run_at"`
}

func registerStatus(server *sdk.Server, d Deps) {
	desc := "Per-dataset freshness of the local Oura store: row count, last synced day, " +
		"last sync run time. Call this first when data recency matters; if the store is " +
		"stale, say so or run the sync tool. Datasets: " + strings.Join(datasets.All(), ", ") + "."
	sdk.AddTool(server, &sdk.Tool{Name: "status", Description: desc},
		func(ctx context.Context, req *sdk.CallToolRequest, in statusIn) (*sdk.CallToolResult, any, error) {
			stats, err := d.Store.Stats()
			if err != nil {
				return toolError("read store stats: %v", err), nil, nil
			}
			rows := make([]statusRow, 0, len(stats))
			for _, st := range stats {
				rows = append(rows, statusRow{
					Dataset: st.Dataset, Rows: st.Rows,
					LastSyncedDay: st.LastSyncedDay, LastRunAt: st.LastRunAt,
				})
			}
			out, err := json.Marshal(rows)
			if err != nil {
				return toolError("encode status: %v", err), nil, nil
			}
			return textResult(string(out)), nil, nil
		})
}
