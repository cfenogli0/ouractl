package export

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

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

func writeCSV(w io.Writer, rows []map[string]any, opts Options) error {
	var cols []string
	switch {
	case len(opts.Fields) > 0:
		cols = opts.Fields
	case opts.All:
		cols = allLeafColumns(rows)
	default:
		return fmt.Errorf("csv requires --fields or --all; available top-level fields: %s",
			strings.Join(topLevelFields(rows), ", "))
	}

	cw := csv.NewWriter(w)
	if err := cw.Write(cols); err != nil {
		return err
	}
	for _, m := range rows {
		flat := flatten(m, "")
		rec := make([]string, len(cols))
		for i, c := range cols {
			rec[i] = scalarString(flat[c])
		}
		if err := cw.Write(rec); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// flatten turns nested maps into dotted leaf paths: contributors.deep_sleep.
// Arrays are emitted as their JSON encoding (kept whole, never spread to columns).
func flatten(m map[string]any, prefix string) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		if child, ok := v.(map[string]any); ok {
			for ck, cv := range flatten(child, key) {
				out[ck] = cv
			}
			continue
		}
		out[key] = v
	}
	return out
}

func allLeafColumns(rows []map[string]any) []string {
	seen := map[string]struct{}{}
	for _, m := range rows {
		for k := range flatten(m, "") {
			seen[k] = struct{}{}
		}
	}
	cols := make([]string, 0, len(seen))
	for k := range seen {
		cols = append(cols, k)
	}
	sort.Strings(cols)
	return cols
}

func topLevelFields(rows []map[string]any) []string {
	seen := map[string]struct{}{}
	for _, m := range rows {
		for k := range m {
			seen[k] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func scalarString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		b, _ := json.Marshal(t) // arrays/objects kept as JSON text
		return string(b)
	}
}
