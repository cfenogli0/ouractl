package sync

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/cfenogli0/ouractl/internal/datasets"
	"github.com/cfenogli0/ouractl/internal/store"
)

type fakeFetcher struct {
	gotStart, gotEnd string
	recs             []map[string]any
}

func (f *fakeFetcher) Fetch(_ context.Context, _, start, end string) ([]map[string]any, error) {
	f.gotStart, f.gotEnd = start, end
	return f.recs, nil
}

func newStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSyncStartsAtWatermarkMinusOverlap(t *testing.T) {
	s := newStore(t)
	if err := s.AdvanceWatermark("daily_sleep", "2026-06-10", "2026-06-10T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	f := &fakeFetcher{recs: []map[string]any{{"id": "x", "day": "2026-06-12"}}}
	ds, _ := datasets.Get("daily_sleep")

	opts := Options{Now: "2026-06-13", OverlapDays: 3, Source: "sync", End: "2026-06-13"}
	if err := Run(context.Background(), s, f, []datasets.Dataset{ds}, opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if f.gotStart != "2026-06-07" {
		t.Fatalf("start = %q, want 2026-06-07 (watermark - overlap)", f.gotStart)
	}
	rows, _ := s.QueryRange("daily_sleep", "2026-01-01", "2026-12-31")
	if len(rows) != 1 {
		t.Fatalf("stored %d rows, want 1", len(rows))
	}
	wm, _ := s.Watermark("daily_sleep")
	if wm != "2026-06-12" {
		t.Fatalf("watermark = %q, want 2026-06-12 (latest fetched day)", wm)
	}
}

func TestRunRequiresNowAndEnd(t *testing.T) {
	s := newStore(t)
	ds, _ := datasets.Get("daily_sleep")
	f := &fakeFetcher{}
	if err := Run(context.Background(), s, f, []datasets.Dataset{ds}, Options{End: "2026-06-13"}); err == nil {
		t.Fatal("empty Now must error to avoid malformed fetched_at")
	}
	if err := Run(context.Background(), s, f, []datasets.Dataset{ds}, Options{Now: "2026-06-13"}); err == nil {
		t.Fatal("empty End must error")
	}
}

func TestBackfillUsesExplicitRange(t *testing.T) {
	s := newStore(t)
	f := &fakeFetcher{recs: nil}
	ds, _ := datasets.Get("daily_sleep")
	opts := Options{Start: "2025-01-01", End: "2025-02-01", Now: "2025-02-01", OverlapDays: 3, Source: "backfill"}
	if err := Run(context.Background(), s, f, []datasets.Dataset{ds}, opts); err != nil {
		t.Fatal(err)
	}
	if f.gotStart != "2025-01-01" || f.gotEnd != "2025-02-01" {
		t.Fatalf("backfill range = [%s,%s], want [2025-01-01,2025-02-01]", f.gotStart, f.gotEnd)
	}
}
