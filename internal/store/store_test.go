package store

import (
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenCreatesSchema(t *testing.T) {
	s := newTestStore(t)
	v, err := s.SchemaVersion()
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if v != 1 {
		t.Fatalf("schema version = %d, want 1", v)
	}
}

func TestUpsertIsIdempotentAndUpdates(t *testing.T) {
	s := newTestStore(t)
	rec := Record{Dataset: "daily_sleep", ID: "doc-1", Day: "2026-06-10",
		Payload: []byte(`{"score":70}`), FetchedAt: "2026-06-10T08:00:00Z", Source: "backfill"}

	if err := s.Upsert(rec); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if err := s.Upsert(rec); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	revised := rec
	revised.Payload = []byte(`{"score":82}`)
	revised.Source = "sync"
	if err := s.Upsert(revised); err != nil {
		t.Fatalf("revising upsert: %v", err)
	}

	rows, err := s.QueryRange("daily_sleep", "2026-06-01", "2026-06-30")
	if err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1 (no duplicate)", len(rows))
	}
	if string(rows[0].Payload) != `{"score":82}` {
		t.Fatalf("payload = %s, want revised score 82", rows[0].Payload)
	}
}
