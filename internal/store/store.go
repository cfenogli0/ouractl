package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // registers the pure-Go "sqlite" driver
)

const schemaVersion = 1

type Store struct {
	db *sql.DB
}

// Record is one stored Oura document.
type Record struct {
	Dataset   string
	ID        string
	Day       string
	Payload   []byte
	FetchedAt string // RFC3339
	Source    string // "sync" | "backfill"
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}
	db.SetMaxOpenConns(1) // sqlite single-writer; avoids "database is locked"
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	const ddl = `
CREATE TABLE IF NOT EXISTS records (
  dataset    TEXT NOT NULL,
  id         TEXT NOT NULL,
  day        TEXT NOT NULL,
  payload    TEXT NOT NULL,
  fetched_at TEXT NOT NULL,
  source     TEXT NOT NULL,
  PRIMARY KEY (dataset, id)
);
CREATE INDEX IF NOT EXISTS idx_records_dataset_day ON records(dataset, day);
CREATE TABLE IF NOT EXISTS sync_state (
  dataset         TEXT PRIMARY KEY,
  last_synced_day TEXT,
  last_run_at     TEXT
);
CREATE TABLE IF NOT EXISTS schema_meta (version INTEGER NOT NULL);
`
	if _, err := s.db.Exec(ddl); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_meta`).Scan(&n); err != nil {
		return fmt.Errorf("read schema_meta: %w", err)
	}
	if n == 0 {
		if _, err := s.db.Exec(`INSERT INTO schema_meta(version) VALUES (?)`, schemaVersion); err != nil {
			return fmt.Errorf("seed schema_meta: %w", err)
		}
	}
	return nil
}

func (s *Store) SchemaVersion() (int, error) {
	var v int
	err := s.db.QueryRow(`SELECT version FROM schema_meta LIMIT 1`).Scan(&v)
	return v, err
}

// Upsert inserts or, on (dataset,id) conflict, overwrites the record. This is
// what makes overlapping backfill safe and lets Oura's after-the-fact score
// revisions land as updates rather than duplicates.
func (s *Store) Upsert(r Record) error {
	const q = `
INSERT INTO records (dataset, id, day, payload, fetched_at, source)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(dataset, id) DO UPDATE SET
  day = excluded.day,
  payload = excluded.payload,
  fetched_at = excluded.fetched_at,
  source = excluded.source`
	if _, err := s.db.Exec(q, r.Dataset, r.ID, r.Day, string(r.Payload), r.FetchedAt, r.Source); err != nil {
		return fmt.Errorf("upsert %s/%s: %w", r.Dataset, r.ID, err)
	}
	return nil
}

// QueryRange returns records for a dataset with day in [from, to], ordered by day, id.
func (s *Store) QueryRange(dataset, from, to string) ([]Record, error) {
	const q = `
SELECT dataset, id, day, payload, fetched_at, source
FROM records
WHERE dataset = ? AND day >= ? AND day <= ?
ORDER BY day, id`
	rows, err := s.db.Query(q, dataset, from, to)
	if err != nil {
		return nil, fmt.Errorf("query %s [%s,%s]: %w", dataset, from, to, err)
	}
	defer rows.Close()

	var out []Record
	for rows.Next() {
		var r Record
		var payload string
		if err := rows.Scan(&r.Dataset, &r.ID, &r.Day, &payload, &r.FetchedAt, &r.Source); err != nil {
			return nil, fmt.Errorf("scan record: %w", err)
		}
		r.Payload = []byte(payload)
		out = append(out, r)
	}
	return out, rows.Err()
}

// Watermark returns the last synced day for a dataset, or "" if never synced.
func (s *Store) Watermark(dataset string) (string, error) {
	var day sql.NullString
	err := s.db.QueryRow(`SELECT last_synced_day FROM sync_state WHERE dataset = ?`, dataset).Scan(&day)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read watermark %s: %w", dataset, err)
	}
	return day.String, nil
}

// DatasetStat is one row of `octl status`.
type DatasetStat struct {
	Dataset       string
	Rows          int
	LastSyncedDay string
	LastRunAt     string
}

// Stats returns per-dataset row counts joined with sync_state.
func (s *Store) Stats() ([]DatasetStat, error) {
	const q = `
SELECT r.dataset, COUNT(*) AS rows,
       COALESCE(ss.last_synced_day, ''), COALESCE(ss.last_run_at, '')
FROM records r
LEFT JOIN sync_state ss ON ss.dataset = r.dataset
GROUP BY r.dataset
ORDER BY r.dataset`
	rows, err := s.db.Query(q)
	if err != nil {
		return nil, fmt.Errorf("stats: %w", err)
	}
	defer rows.Close()
	var out []DatasetStat
	for rows.Next() {
		var st DatasetStat
		if err := rows.Scan(&st.Dataset, &st.Rows, &st.LastSyncedDay, &st.LastRunAt); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// AdvanceWatermark moves last_synced_day forward to day if day is later than the
// stored value (lexicographic compare works for YYYY-MM-DD). last_run_at is
// always updated so status reflects the attempt.
func (s *Store) AdvanceWatermark(dataset, day, runAt string) error {
	const q = `
INSERT INTO sync_state (dataset, last_synced_day, last_run_at)
VALUES (?, ?, ?)
ON CONFLICT(dataset) DO UPDATE SET
  last_synced_day = MAX(sync_state.last_synced_day, excluded.last_synced_day),
  last_run_at = excluded.last_run_at`
	if _, err := s.db.Exec(q, dataset, day, runAt); err != nil {
		return fmt.Errorf("advance watermark %s: %w", dataset, err)
	}
	return nil
}
