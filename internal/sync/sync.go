package sync

import (
	"context"
	"fmt"
	"time"

	"github.com/cfenogli0/ouractl/internal/datasets"
	"github.com/cfenogli0/ouractl/internal/store"
)

// Fetcher is the slice of the oura client the orchestrator needs.
type Fetcher interface {
	Fetch(ctx context.Context, endpoint, start, end string) ([]map[string]any, error)
}

// Options controls one sync/backfill run. Start empty => incremental from
// watermark minus OverlapDays. Start set => explicit backfill range.
type Options struct {
	Start       string // YYYY-MM-DD, "" for incremental
	End         string // YYYY-MM-DD, required
	Now         string // YYYY-MM-DD, used as fetched_at date and run timestamp
	OverlapDays int
	Source      string // "sync" | "backfill"
}

func Run(ctx context.Context, s *store.Store, f Fetcher, sets []datasets.Dataset, opts Options) error {
	// Guard the silent-corruption path: an empty Now would write a malformed
	// "T00:00:00Z" into every record's fetched_at. The CLI always sets it; this
	// catches a misconfigured caller before it pollutes the store.
	if opts.Now == "" {
		return fmt.Errorf("sync: opts.Now (YYYY-MM-DD) is required")
	}
	if opts.End == "" {
		return fmt.Errorf("sync: opts.End (YYYY-MM-DD) is required")
	}
	runAt := opts.Now + "T00:00:00Z"
	for _, ds := range sets {
		start, err := rangeStart(s, ds.Name, opts)
		if err != nil {
			return err
		}
		raws, err := f.Fetch(ctx, ds.Endpoint, start, opts.End)
		if err != nil {
			return fmt.Errorf("fetch %s: %w", ds.Name, err)
		}
		maxDay := ""
		for _, raw := range raws {
			id, day := ds.Key(raw)
			payload, err := ds.Normalize(raw)
			if err != nil {
				return fmt.Errorf("normalize %s/%s: %w", ds.Name, id, err)
			}
			if err := s.Upsert(store.Record{
				Dataset: ds.Name, ID: id, Day: day, Payload: payload,
				FetchedAt: runAt, Source: opts.Source,
			}); err != nil {
				return err
			}
			if day > maxDay {
				maxDay = day
			}
		}
		if maxDay != "" {
			if err := s.AdvanceWatermark(ds.Name, maxDay, runAt); err != nil {
				return err
			}
		}
	}
	return nil
}

func rangeStart(s *store.Store, dataset string, opts Options) (string, error) {
	if opts.Start != "" {
		return opts.Start, nil
	}
	wm, err := s.Watermark(dataset)
	if err != nil {
		return "", err
	}
	if wm == "" {
		// Never synced and no explicit start: pull the overlap window before End.
		return MinusDays(opts.End, opts.OverlapDays)
	}
	return MinusDays(wm, opts.OverlapDays)
}

// MinusDays returns the date n days before day (both YYYY-MM-DD). Exported
// because the CLI backfill command uses it to resolve --days into a start date.
func MinusDays(day string, n int) (string, error) {
	t, err := time.Parse("2006-01-02", day)
	if err != nil {
		return "", fmt.Errorf("parse day %q: %w", day, err)
	}
	return t.AddDate(0, 0, -n).Format("2006-01-02"), nil
}
