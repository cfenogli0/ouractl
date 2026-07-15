package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cfenogli0/ouractl/internal/store"
	syncpkg "github.com/cfenogli0/ouractl/internal/sync"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// seedStore opens a temp store with two daily_sleep rows on known days.
func seedStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	for _, day := range []string{"2026-07-01", "2026-07-02"} {
		payload := []byte(`{"day":"` + day + `","score":80}`)
		if err := s.Upsert(store.Record{
			Dataset: "daily_sleep", ID: "id-" + day, Day: day,
			Payload: payload, FetchedAt: "2026-07-03T00:00:00Z", Source: "sync",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AdvanceWatermark("daily_sleep", "2026-07-02", "2026-07-03T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	return s
}

// errFactory is a fetcher factory that must not be called; it fails the test if it is.
func errFactory(t *testing.T) func() (syncpkg.Fetcher, error) {
	return func() (syncpkg.Fetcher, error) {
		t.Error("fetcher factory called by a read-only tool")
		return nil, errors.New("no auth")
	}
}

func testDeps(t *testing.T, s *store.Store) Deps {
	enabled, err := namesToDatasets([]string{"daily_sleep"})
	if err != nil {
		t.Fatal(err)
	}
	return Deps{
		Store:          s,
		FetcherFactory: errFactory(t),
		Enabled:        enabled,
		OverlapDays:    2,
		Today:          func() string { return "2026-07-03" },
		Version:        "test",
	}
}

func newTestSession(t *testing.T, d Deps) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	ct, st := sdk.NewInMemoryTransports()
	server := New(d)
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "v0"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func callText(t *testing.T, cs *sdk.ClientSession, tool string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s: %v", tool, err)
	}
	if len(res.Content) == 0 {
		return "", res.IsError
	}
	tc, ok := res.Content[0].(*sdk.TextContent)
	if !ok {
		t.Fatalf("content is %T, want TextContent", res.Content[0])
	}
	return tc.Text, res.IsError
}

func TestStatusTool(t *testing.T) {
	cs := newTestSession(t, testDeps(t, seedStore(t)))
	text, isErr := callText(t, cs, "status", nil)
	if isErr {
		t.Fatalf("status errored: %s", text)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(text), &rows); err != nil {
		t.Fatalf("status output not JSON: %v\n%s", err, text)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 dataset row, got %d", len(rows))
	}
	r := rows[0]
	if r["dataset"] != "daily_sleep" || r["rows"] != float64(2) || r["last_synced_day"] != "2026-07-02" {
		t.Errorf("unexpected status row: %v", r)
	}
	if !strings.Contains(text, "last_run_at") {
		t.Errorf("status row missing last_run_at: %s", text)
	}
}

func TestReadToolsNeverTouchAuth(t *testing.T) {
	// testDeps wires errFactory, which t.Error()s if called. Read-only tools
	// must not trip it.
	cs := newTestSession(t, testDeps(t, seedStore(t)))
	if _, isErr := callText(t, cs, "status", nil); isErr {
		t.Fatal("status errored")
	}
	if _, isErr := callText(t, cs, "query", map[string]any{
		"dataset": "daily_sleep", "from": "2026-07-01", "to": "2026-07-02",
	}); isErr {
		t.Fatal("query errored")
	}
}

func TestQueryTool(t *testing.T) {
	cs := newTestSession(t, testDeps(t, seedStore(t)))
	text, isErr := callText(t, cs, "query", map[string]any{
		"dataset": "daily_sleep", "from": "2026-07-01", "to": "2026-07-02",
	})
	if isErr {
		t.Fatalf("query errored: %s", text)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(text), &rows); err != nil {
		t.Fatalf("query output not JSON: %v\n%s", err, text)
	}
	if len(rows) != 2 || rows[0]["score"] != float64(80) {
		t.Errorf("unexpected rows: %v", rows)
	}
}

func TestQueryFieldProjection(t *testing.T) {
	cs := newTestSession(t, testDeps(t, seedStore(t)))
	text, _ := callText(t, cs, "query", map[string]any{
		"dataset": "daily_sleep", "from": "2026-07-01", "to": "2026-07-02",
		"fields": []string{"day"},
	})
	var rows []map[string]any
	if err := json.Unmarshal([]byte(text), &rows); err != nil {
		t.Fatal(err)
	}
	if _, hasScore := rows[0]["score"]; hasScore {
		t.Errorf("projection leaked score: %v", rows[0])
	}
	if rows[0]["day"] != "2026-07-01" {
		t.Errorf("projection dropped day: %v", rows[0])
	}
}

func TestQueryRowCap(t *testing.T) {
	s := seedStore(t)
	cs := newTestSession(t, testDeps(t, s))
	// limit 2 passes (exactly at cap), limit 1 errors and names the count.
	if text, isErr := callText(t, cs, "query", map[string]any{
		"dataset": "daily_sleep", "from": "2026-07-01", "to": "2026-07-02", "limit": 2,
	}); isErr {
		t.Fatalf("at-cap query errored: %s", text)
	}
	text, isErr := callText(t, cs, "query", map[string]any{
		"dataset": "daily_sleep", "from": "2026-07-01", "to": "2026-07-02", "limit": 1,
	})
	if !isErr {
		t.Fatal("over-cap query did not error")
	}
	if !strings.Contains(text, "2 rows") || !strings.Contains(text, "limit") {
		t.Errorf("cap error should name count and limit: %s", text)
	}
}

func TestQueryErrors(t *testing.T) {
	cs := newTestSession(t, testDeps(t, seedStore(t)))

	text, isErr := callText(t, cs, "query", map[string]any{
		"dataset": "nope", "from": "2026-07-01", "to": "2026-07-02",
	})
	if !isErr || !strings.Contains(text, "daily_sleep") {
		t.Errorf("unknown dataset should error listing valid names: err=%v %s", isErr, text)
	}

	text, isErr = callText(t, cs, "query", map[string]any{
		"dataset": "daily_sleep", "from": "07/01/2026", "to": "2026-07-02",
	})
	if !isErr || !strings.Contains(text, "YYYY-MM-DD") {
		t.Errorf("bad date should error with format hint: err=%v %s", isErr, text)
	}

	// daily_readiness is registered but never synced in the seed store.
	text, isErr = callText(t, cs, "query", map[string]any{
		"dataset": "daily_readiness", "from": "2026-07-01", "to": "2026-07-02",
	})
	if !isErr || !strings.Contains(text, "never") {
		t.Errorf("never-synced dataset should say so and point at sync: err=%v %s", isErr, text)
	}

	// Known dataset, synced, but empty range: legit empty result, not an error.
	text, isErr = callText(t, cs, "query", map[string]any{
		"dataset": "daily_sleep", "from": "2020-01-01", "to": "2020-01-02",
	})
	if isErr {
		t.Errorf("empty range errored: %s", text)
	}
	if strings.TrimSpace(text) != "[]" {
		t.Errorf("empty range should return [], got %q", text)
	}
}
