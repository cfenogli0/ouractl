package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cfenogli0/ouractl/internal/datasets"
	"github.com/cfenogli0/ouractl/internal/export"
	"github.com/cfenogli0/ouractl/internal/store"
	syncpkg "github.com/cfenogli0/ouractl/internal/sync"
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
	registerSync(server, d)
}

type syncIn struct {
	Datasets []string `json:"datasets,omitempty" jsonschema:"datasets to sync; omit for the configured enabled set"`
	Days     int      `json:"days,omitempty" jsonschema:"backfill the last N days instead of incremental-from-watermark"`
}

type syncRow struct {
	Dataset       string `json:"dataset"`
	RowsAdded     int    `json:"rows_added"`
	TotalRows     int    `json:"total_rows"`
	LastSyncedDay string `json:"last_synced_day"`
}

func statsByDataset(s *store.Store) (map[string]store.DatasetStat, error) {
	stats, err := s.Stats()
	if err != nil {
		return nil, err
	}
	m := make(map[string]store.DatasetStat, len(stats))
	for _, st := range stats {
		m[st.Dataset] = st
	}
	return m, nil
}

func registerSync(server *sdk.Server, d Deps) {
	desc := "Fetch fresh data from the Oura API into the local store. Spends real API " +
		"rate-limit budget; use when status shows stale data, not before every query. " +
		"Returns per-dataset rows_added (new rows this run), total_rows, last_synced_day."
	sdk.AddTool(server, &sdk.Tool{Name: "sync", Description: desc},
		func(ctx context.Context, req *sdk.CallToolRequest, in syncIn) (*sdk.CallToolResult, any, error) {
			sets := d.Enabled
			if len(in.Datasets) > 0 {
				var err error
				sets, err = namesToDatasets(in.Datasets)
				if err != nil {
					return toolError("%v", err), nil, nil
				}
			}
			before, err := statsByDataset(d.Store)
			if err != nil {
				return toolError("read store stats: %v", err), nil, nil
			}
			fetcher, err := d.FetcherFactory()
			if err != nil {
				return toolError("auth: %v", err), nil, nil
			}
			now := d.Today()
			opts := syncpkg.Options{End: now, Now: now, OverlapDays: d.OverlapDays, Source: "sync"}
			if in.Days > 0 {
				start, err := syncpkg.MinusDays(now, in.Days)
				if err != nil {
					return toolError("resolve days: %v", err), nil, nil
				}
				opts.Start = start
				opts.Source = "backfill"
			}
			if err := syncpkg.Run(ctx, d.Store, fetcher, sets, opts); err != nil {
				return toolError("sync: %v", err), nil, nil
			}
			after, err := statsByDataset(d.Store)
			if err != nil {
				return toolError("read store stats after sync: %v", err), nil, nil
			}
			// rows_added is a row-count diff, not an upsert count: overlap
			// re-upserts don't change the count, new rows do; that is the
			// "what did this sync bring in" number.
			rows := make([]syncRow, 0, len(sets))
			for _, ds := range sets {
				a := after[ds.Name]
				rows = append(rows, syncRow{
					Dataset:       ds.Name,
					RowsAdded:     a.Rows - before[ds.Name].Rows,
					TotalRows:     a.Rows,
					LastSyncedDay: a.LastSyncedDay,
				})
			}
			out, err := json.Marshal(rows)
			if err != nil {
				return toolError("encode sync result: %v", err), nil, nil
			}
			return textResult(string(out)), nil, nil
		})
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
