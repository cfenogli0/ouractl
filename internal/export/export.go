package export

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/cfenogli0/ouractl/internal/store"
)

type Options struct {
	Dataset string
	From    string
	To      string
	Format  string   // "json" | "ndjson" | "csv"
	Fields  []string // projection; empty + All=false is required for csv
	All     bool     // csv: dump every leaf
}

func Export(s *store.Store, opts Options, w io.Writer) error {
	recs, err := s.QueryRange(opts.Dataset, opts.From, opts.To)
	if err != nil {
		return err
	}
	rows := make([]map[string]any, 0, len(recs))
	for _, r := range recs {
		var m map[string]any
		if err := json.Unmarshal(r.Payload, &m); err != nil {
			return fmt.Errorf("decode stored payload %s/%s: %w", r.Dataset, r.ID, err)
		}
		if len(opts.Fields) > 0 {
			m = project(m, opts.Fields)
		}
		rows = append(rows, m)
	}

	switch opts.Format {
	case "json":
		enc := json.NewEncoder(w)
		return enc.Encode(rows)
	case "ndjson":
		enc := json.NewEncoder(w)
		for _, m := range rows {
			if err := enc.Encode(m); err != nil {
				return err
			}
		}
		return nil
	case "csv":
		return writeCSV(w, rows, opts)
	default:
		return fmt.Errorf("unknown format %q (want json, ndjson, or csv)", opts.Format)
	}
}

// project keeps only the named top-level fields.
func project(m map[string]any, fields []string) map[string]any {
	out := make(map[string]any, len(fields))
	for _, f := range fields {
		if v, ok := m[f]; ok {
			out[f] = v
		}
	}
	return out
}

// writeCSV is implemented in Task 16; this temporary stub keeps the package
// compiling until then.
func writeCSV(w io.Writer, rows []map[string]any, opts Options) error {
	return fmt.Errorf("csv not implemented yet")
}
