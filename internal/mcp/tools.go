package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cfenogli0/ouractl/internal/datasets"
	"github.com/cfenogli0/ouractl/internal/export"
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
	registerQuery(server, d)
	registerStatus(server, d)
}

func validDay(s string) bool {
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

type queryIn struct {
	Dataset string   `json:"dataset" jsonschema:"dataset name, one of the octl datasets"`
	From    string   `json:"from" jsonschema:"start day inclusive, YYYY-MM-DD"`
	To      string   `json:"to" jsonschema:"end day inclusive, YYYY-MM-DD"`
	Fields  []string `json:"fields,omitempty" jsonschema:"optional top-level fields to keep; omit for full records"`
	Limit   int      `json:"limit,omitempty" jsonschema:"max rows returned, default 200; raise deliberately for big ranges"`
}

func registerQuery(server *sdk.Server, d Deps) {
	desc := "Query the local Oura store (SQLite, synced longitudinal record; not the live API). " +
		"Returns a JSON array, one object per record, same shape as `octl export`. " +
		"Use fields to keep results small. Datasets: " + strings.Join(datasets.All(), ", ") + "."
	sdk.AddTool(server, &sdk.Tool{Name: "query", Description: desc},
		func(ctx context.Context, req *sdk.CallToolRequest, in queryIn) (*sdk.CallToolResult, any, error) {
			if _, ok := datasets.Get(in.Dataset); !ok {
				return toolError("unknown dataset %q; valid: %s", in.Dataset, strings.Join(datasets.All(), ", ")), nil, nil
			}
			for _, day := range []string{in.From, in.To} {
				if !validDay(day) {
					return toolError("bad date %q: want YYYY-MM-DD", day), nil, nil
				}
			}
			limit := in.Limit
			if limit <= 0 {
				limit = defaultRowLimit
			}
			recs, err := d.Store.QueryRange(in.Dataset, in.From, in.To)
			if err != nil {
				return toolError("query store: %v", err), nil, nil
			}
			if len(recs) > limit {
				return toolError("%d rows match %s [%s, %s], over the limit of %d; narrow the range or pass a higher limit",
					len(recs), in.Dataset, in.From, in.To, limit), nil, nil
			}
			if len(recs) == 0 {
				wm, err := d.Store.Watermark(in.Dataset)
				if err != nil {
					return toolError("read watermark: %v", err), nil, nil
				}
				if wm == "" {
					return toolError("dataset %s has never been synced; run the sync tool or `octl sync`", in.Dataset), nil, nil
				}
				return textResult("[]"), nil, nil
			}
			// Second read via export.Export keeps the output byte-identical to
			// `octl export`; rows are local SQLite, the cost is negligible.
			var buf strings.Builder
			opts := export.Options{Dataset: in.Dataset, From: in.From, To: in.To, Format: "json", Fields: in.Fields}
			if err := export.Export(d.Store, opts, &buf); err != nil {
				return toolError("export: %v", err), nil, nil
			}
			return textResult(strings.TrimSpace(buf.String())), nil, nil
		})
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
