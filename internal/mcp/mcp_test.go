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
}
