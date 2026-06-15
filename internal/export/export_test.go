package export

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cfenogli0/ouractl/internal/store"
)

func seed(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "e.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	recs := []store.Record{
		{Dataset: "daily_sleep", ID: "1", Day: "2026-06-10", Payload: []byte(`{"day":"2026-06-10","score":80,"contributors":{"deep_sleep":90}}`), FetchedAt: "x", Source: "sync"},
		{Dataset: "daily_sleep", ID: "2", Day: "2026-06-11", Payload: []byte(`{"day":"2026-06-11","score":85,"contributors":{"deep_sleep":88}}`), FetchedAt: "x", Source: "sync"},
	}
	for _, r := range recs {
		if err := s.Upsert(r); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func TestExportJSONAllFields(t *testing.T) {
	s := seed(t)
	var buf bytes.Buffer
	err := Export(s, Options{Dataset: "daily_sleep", From: "2026-06-01", To: "2026-06-30", Format: "json", All: true}, &buf)
	if err != nil {
		t.Fatalf("Export json: %v", err)
	}
	if !strings.Contains(buf.String(), `"score":80`) || !strings.Contains(buf.String(), `"score":85`) {
		t.Fatalf("json missing records: %s", buf.String())
	}
}

func TestExportNDJSONProjectsFields(t *testing.T) {
	s := seed(t)
	var buf bytes.Buffer
	err := Export(s, Options{Dataset: "daily_sleep", From: "2026-06-01", To: "2026-06-30",
		Format: "ndjson", Fields: []string{"day", "score"}}, &buf)
	if err != nil {
		t.Fatalf("Export ndjson: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("ndjson lines = %d, want 2", len(lines))
	}
	if strings.Contains(lines[0], "contributors") {
		t.Fatalf("projection leaked unselected field: %s", lines[0])
	}
	if !strings.Contains(lines[0], `"score":80`) {
		t.Fatalf("projected line missing score: %s", lines[0])
	}
}

func TestExportCSVRequiresFieldSelection(t *testing.T) {
	s := seed(t)
	var buf bytes.Buffer
	err := Export(s, Options{Dataset: "daily_sleep", From: "2026-06-01", To: "2026-06-30", Format: "csv"}, &buf)
	if err == nil {
		t.Fatal("csv without --fields or --all must error")
	}
	if !strings.Contains(err.Error(), "score") {
		t.Fatalf("error should list available top-level fields, got: %v", err)
	}
}

func TestExportCSVWithFields(t *testing.T) {
	s := seed(t)
	var buf bytes.Buffer
	err := Export(s, Options{Dataset: "daily_sleep", From: "2026-06-01", To: "2026-06-30",
		Format: "csv", Fields: []string{"day", "score"}}, &buf)
	if err != nil {
		t.Fatalf("csv with fields: %v", err)
	}
	out := strings.TrimSpace(buf.String())
	lines := strings.Split(out, "\n")
	if lines[0] != "day,score" {
		t.Fatalf("header = %q, want day,score", lines[0])
	}
	if len(lines) != 3 {
		t.Fatalf("csv lines = %d, want 3", len(lines))
	}
	if !strings.Contains(out, "2026-06-10,80") {
		t.Fatalf("csv missing expected row, got:\n%s", out)
	}
}

func TestExportCSVAllFlattens(t *testing.T) {
	s := seed(t)
	var buf bytes.Buffer
	err := Export(s, Options{Dataset: "daily_sleep", From: "2026-06-01", To: "2026-06-30",
		Format: "csv", All: true}, &buf)
	if err != nil {
		t.Fatalf("csv --all: %v", err)
	}
	if !strings.Contains(buf.String(), "contributors.deep_sleep") {
		t.Fatalf("--all should flatten nested leaves, got:\n%s", buf.String())
	}
}
