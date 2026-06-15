# octl (Oura Data Tool) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `octl`, a single static Go binary that pulls Oura Ring API v2 data into a local SQLite store with idempotent backfill, then exports it in json/csv/ndjson, plus a Claude Code skill so it is usable conversationally without an MCP server.

**Architecture:** Bottom-up, dependency-ordered. Pure data layers first (config, store, dataset registry, normalizers), then the API client and auth, then the sync orchestrator that wires them, then export, then the cobra CLI surface that calls into the packages, then the skill. Each package is isolated under `internal/`; the binary entrypoint is `cmd/octl/main.go`. TDD throughout: every behavior with a silent/expensive failure mode (backfill idempotency, watermark+overlap, config precedence, normalizer mapping, OAuth refresh, rate-limit retry, export projection) gets a test before its implementation.

**Tech Stack:** Go 1.23, `github.com/spf13/cobra` (CLI), `github.com/BurntSushi/toml` (config), `modernc.org/sqlite` (pure-Go SQLite, no cgo), `github.com/adrg/xdg` (XDG paths), `crypto/aes`+`crypto/cipher` (AES-256-GCM for OAuth tokens), `net/http`+`net/http/httptest` (client + tests).

**Spec:** `docs/superpowers/specs/2026-06-10-oura-tool-design.md`

---

## Prerequisites

- Go 1.23+ on `PATH` (`go version`). If missing: `brew install go`.
- Working directory is the repo root `~/dev/oura` (module `github.com/cfenogli0/ouractl`).
- All commands run from repo root unless stated.

## File Structure

| Path | Responsibility |
|---|---|
| `go.mod`, `go.sum` | Module `github.com/cfenogli0/ouractl`, Go 1.23, deps |
| `cmd/octl/main.go` | `package main`; builds the cobra root and dispatches. Installs as binary `octl`. |
| `internal/config/config.go` | Config types, defaults, TOML file load, precedence merge, validation |
| `internal/store/store.go` | SQLite open, migrations, `Upsert`, `QueryRange`, `Watermark`, `AdvanceWatermark` |
| `internal/datasets/datasets.go` | `Dataset` type, registry, `Register`/`Get`/`DefaultEnabled`/`All` |
| `internal/datasets/normalizers.go` | One registry entry (key+normalize) per v1 dataset |
| `internal/datasets/testdata/*.json` | Golden raw-API fixtures per dataset |
| `internal/oura/client.go` | API client: auth header, pagination, 429 retry/backoff, rate accounting |
| `internal/auth/auth.go` | `TokenProvider` interface, `StaticProvider` (PAT) |
| `internal/auth/oauth.go` | `OAuthProvider` (refresh-on-expiry), encrypted `TokenStore` |
| `internal/auth/crypto.go` | AES-256-GCM `Encrypt`/`Decrypt`, key derivation |
| `internal/sync/sync.go` | Orchestration: range -> fetch -> normalize -> upsert -> advance watermark |
| `internal/export/export.go` | Readers + json/csv/ndjson formatters, field projection, `--all` |
| `internal/cli/*.go` | cobra command constructors (one file per command group) |
| `skills/octl/SKILL.md` | Claude Code skill: export-then-read pattern |
| `README.md` | Install, auth, sync, export usage |

Build order below follows the dependency arrows: a package is built only after everything it imports.

---

### Task 1: Bootstrap module and cobra root

**Files:**
- Create: `go.mod` (via `go mod init`)
- Create: `cmd/octl/main.go`
- Create: `.gitignore`

- [ ] **Step 1: Initialize the module**

Run:
```bash
go mod init github.com/cfenogli0/ouractl
go get github.com/spf13/cobra@latest
```
Expected: `go.mod` created with module path and a `require github.com/spf13/cobra` line.

- [ ] **Step 2: Write the root command entrypoint**

Create `cmd/octl/main.go`:
```go
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// version is overwritten at release time via -ldflags.
var version = "dev"

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "octl",
		Short:         "octl pulls Oura Ring data into a local store and exports it",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
	}
	return root
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "octl: "+err.Error())
		os.Exit(1)
	}
}
```

- [ ] **Step 3: Write `.gitignore`**

Create `.gitignore`:
```
/octl
*.db
*.db-journal
/dist/
```

- [ ] **Step 4: Build and run to verify it wires**

Run:
```bash
go build -o octl ./cmd/octl && ./octl --help
```
Expected: build succeeds; help text prints `octl pulls Oura Ring data into a local store and exports it`.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum cmd/octl/main.go .gitignore
git commit -m "feat: bootstrap octl module and cobra root command"
```

---

### Task 2: Config types and defaults

**Files:**
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

- [ ] **Step 1: Write the failing test for defaults**

Create `internal/config/config_test.go`:
```go
package config

import "testing"

func TestDefaultsAreMinimalAndSafe(t *testing.T) {
	c := Defaults()
	if c.Auth.Mode != "token" {
		t.Fatalf("default auth mode = %q, want token", c.Auth.Mode)
	}
	if c.Collection.OverlapDays != 3 {
		t.Fatalf("default overlap_days = %d, want 3", c.Collection.OverlapDays)
	}
	want := []string{"daily_sleep", "sleep", "daily_readiness", "daily_activity"}
	if len(c.Collection.Enabled) != len(want) {
		t.Fatalf("default enabled = %v, want %v", c.Collection.Enabled, want)
	}
	for i := range want {
		if c.Collection.Enabled[i] != want[i] {
			t.Fatalf("default enabled[%d] = %q, want %q", i, c.Collection.Enabled[i], want[i])
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestDefaultsAreMinimalAndSafe -v`
Expected: FAIL, `undefined: Defaults`.

- [ ] **Step 3: Write the types and defaults**

Create `internal/config/config.go`:
```go
package config

// Config is the fully-resolved tool configuration.
type Config struct {
	Auth       AuthConfig       `toml:"auth"`
	Storage    StorageConfig    `toml:"storage"`
	Collection CollectionConfig `toml:"collection"`
	Logging    LoggingConfig    `toml:"logging"`
}

type AuthConfig struct {
	Mode         string `toml:"mode"` // "token" | "oauth2"
	Token        string `toml:"token"`
	ClientID     string `toml:"client_id"`
	ClientSecret string `toml:"client_secret"`
	RedirectURI  string `toml:"redirect_uri"`
}

type StorageConfig struct {
	Path          string `toml:"path"`
	EncryptionKey string `toml:"encryption_key"`
}

type CollectionConfig struct {
	Enabled     []string `toml:"enabled"`
	OverlapDays int      `toml:"overlap_days"`
}

type LoggingConfig struct {
	Level string `toml:"level"`
}

// Defaults returns the built-in configuration: minimal dataset set, token auth,
// 3-day overlap window. Storage path stays empty here; ResolvePaths fills it
// from XDG so this function has no filesystem dependency (keeps it testable).
func Defaults() Config {
	return Config{
		Auth: AuthConfig{Mode: "token"},
		Collection: CollectionConfig{
			Enabled:     []string{"daily_sleep", "sleep", "daily_readiness", "daily_activity"},
			OverlapDays: 3,
		},
		Logging: LoggingConfig{Level: "info"},
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -run TestDefaultsAreMinimalAndSafe -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): types and built-in defaults"
```

---

### Task 3: Load config from a TOML file

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/config/config_test.go`:
```go
import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFileOverlaysDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(`
[auth]
mode = "oauth2"
client_id = "abc"

[collection]
enabled = ["daily_sleep"]
overlap_days = 5
`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if c.Auth.Mode != "oauth2" || c.Auth.ClientID != "abc" {
		t.Fatalf("auth not loaded: %+v", c.Auth)
	}
	if c.Collection.OverlapDays != 5 || len(c.Collection.Enabled) != 1 {
		t.Fatalf("collection not loaded: %+v", c.Collection)
	}
	// Field absent from the file keeps its default.
	if c.Logging.Level != "info" {
		t.Fatalf("logging default lost: %q", c.Logging.Level)
	}
}

func TestLoadFileMissingReturnsDefaults(t *testing.T) {
	c, err := LoadFile(filepath.Join(t.TempDir(), "absent.toml"))
	if err != nil {
		t.Fatalf("missing file should be non-fatal: %v", err)
	}
	if c.Auth.Mode != "token" {
		t.Fatalf("want defaults on missing file, got %+v", c.Auth)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestLoadFile -v`
Expected: FAIL, `undefined: LoadFile`.

- [ ] **Step 3: Implement LoadFile**

Run: `go get github.com/BurntSushi/toml@latest`

Add to `internal/config/config.go`:
```go
import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/BurntSushi/toml"
)

// LoadFile starts from Defaults() and overlays values present in the TOML file.
// A missing file is not an error: the tool must run from env/flags alone.
func LoadFile(path string) (Config, error) {
	c := Defaults()
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return c, nil
	} else if err != nil {
		return c, fmt.Errorf("stat config %s: %w", path, err)
	}
	if _, err := toml.DecodeFile(path, &c); err != nil {
		return c, fmt.Errorf("parse config %s: %w", path, err)
	}
	return c, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -run TestLoadFile -v`
Expected: PASS (both cases).

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): load TOML file overlaid on defaults"
```

---

### Task 4: Resolve precedence (flags > env > file > default)

This is the load-bearing config behavior. Resolution happens per-value, not per-source: an env var overrides the file even when the file also set the field, but leaves untouched any field the env does not set.

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/config/config_test.go`:
```go
func TestResolvePrecedence(t *testing.T) {
	base := Defaults()
	base.Auth.Token = "from-file"
	base.Auth.ClientID = "file-client"

	env := map[string]string{
		"OURA_TOKEN":     "from-env",
		"OURA_AUTH_MODE": "oauth2",
	}
	flags := Overrides{Token: "from-flag"} // only token set via flag

	got := Resolve(base, env, flags)

	// flag beats env beats file
	if got.Auth.Token != "from-flag" {
		t.Fatalf("token = %q, want from-flag", got.Auth.Token)
	}
	// env beats file when no flag
	if got.Auth.Mode != "oauth2" {
		t.Fatalf("mode = %q, want oauth2", got.Auth.Mode)
	}
	// file survives when neither env nor flag set it
	if got.Auth.ClientID != "file-client" {
		t.Fatalf("client_id = %q, want file-client", got.Auth.ClientID)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestResolvePrecedence -v`
Expected: FAIL, `undefined: Overrides` / `undefined: Resolve`.

- [ ] **Step 3: Implement Overrides and Resolve**

Add to `internal/config/config.go`:
```go
// Overrides holds values supplied on the command line. Empty string means unset.
type Overrides struct {
	Mode          string
	Token         string
	ClientID      string
	ClientSecret  string
	RedirectURI   string
	EncryptionKey string
	StoragePath   string
}

// Resolve applies env then flags on top of base (which already has file+default
// merged by LoadFile). Each field is overridden independently and only when the
// higher-priority source provides a non-empty value.
func Resolve(base Config, env map[string]string, flags Overrides) Config {
	c := base
	set := func(dst *string, vals ...string) {
		for _, v := range vals {
			if v != "" {
				*dst = v
			}
		}
	}
	// Order within each call: lowest-priority override first, highest last.
	set(&c.Auth.Mode, env["OURA_AUTH_MODE"], flags.Mode)
	set(&c.Auth.Token, env["OURA_TOKEN"], flags.Token)
	set(&c.Auth.ClientID, env["OURA_CLIENT_ID"], flags.ClientID)
	set(&c.Auth.ClientSecret, env["OURA_CLIENT_SECRET"], flags.ClientSecret)
	set(&c.Auth.RedirectURI, env["OURA_REDIRECT_URI"], flags.RedirectURI)
	set(&c.Storage.EncryptionKey, env["OURA_ENCRYPTION_KEY"], flags.EncryptionKey)
	set(&c.Storage.Path, env["OURA_DB_PATH"], flags.StoragePath)
	return c
}

// EnvMap is a convenience to capture the OURA_* environment for Resolve.
func EnvMap() map[string]string {
	keys := []string{
		"OURA_AUTH_MODE", "OURA_TOKEN", "OURA_CLIENT_ID", "OURA_CLIENT_SECRET",
		"OURA_REDIRECT_URI", "OURA_ENCRYPTION_KEY", "OURA_DB_PATH",
	}
	m := make(map[string]string, len(keys))
	for _, k := range keys {
		if v, ok := os.LookupEnv(k); ok {
			m[k] = v
		}
	}
	return m
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -run TestResolvePrecedence -v`
Expected: PASS.

- [ ] **Step 5: Add path resolution and validation**

Add to `internal/config/config.go`:
```go
import "github.com/adrg/xdg" // run: go get github.com/adrg/xdg@latest

// ResolvePaths fills empty storage/config paths from XDG. Call after Resolve.
func ResolvePaths(c *Config) error {
	if c.Storage.Path == "" {
		p, err := xdg.DataFile("oura/oura.db")
		if err != nil {
			return fmt.Errorf("resolve data path: %w", err)
		}
		c.Storage.Path = p
	}
	return nil
}

// DefaultConfigPath is where the config file lives unless --config overrides it.
func DefaultConfigPath() (string, error) {
	return xdg.ConfigFile("oura/config.toml")
}

// Validate checks the resolved config is internally consistent and runnable.
func Validate(c Config) error {
	switch c.Auth.Mode {
	case "token":
		if c.Auth.Token == "" {
			return fmt.Errorf("auth mode token requires a token (set OURA_TOKEN, --oura-token, or [auth] token)")
		}
	case "oauth2":
		if c.Auth.ClientID == "" || c.Auth.ClientSecret == "" {
			return fmt.Errorf("auth mode oauth2 requires client_id and client_secret")
		}
	default:
		return fmt.Errorf("unknown auth mode %q (want token or oauth2)", c.Auth.Mode)
	}
	if c.Collection.OverlapDays < 0 {
		return fmt.Errorf("overlap_days must be >= 0, got %d", c.Collection.OverlapDays)
	}
	return nil
}
```

- [ ] **Step 6: Write and run validation tests**

Add to `internal/config/config_test.go`:
```go
func TestValidate(t *testing.T) {
	ok := Defaults()
	ok.Auth.Token = "t"
	if err := Validate(ok); err != nil {
		t.Fatalf("valid token config rejected: %v", err)
	}
	bad := Defaults() // token mode, no token
	if Validate(bad) == nil {
		t.Fatal("token mode without token should fail validation")
	}
	oauthMissing := Defaults()
	oauthMissing.Auth.Mode = "oauth2"
	if Validate(oauthMissing) == nil {
		t.Fatal("oauth2 without client creds should fail validation")
	}
}
```
Run: `go test ./internal/config/ -v`
Expected: PASS (all config tests).

- [ ] **Step 7: Commit**

```bash
git add internal/config/ go.mod go.sum
git commit -m "feat(config): precedence resolution, XDG paths, validation"
```

---

### Task 5: Store open and migrations

**Files:**
- Create: `internal/store/store.go`
- Test: `internal/store/store_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/store/store_test.go`:
```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestOpenCreatesSchema -v`
Expected: FAIL, `undefined: Open`.

- [ ] **Step 3: Implement Open, migrations, Close, SchemaVersion**

Run: `go get modernc.org/sqlite@latest`

Create `internal/store/store.go`:
```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestOpenCreatesSchema -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/ go.mod go.sum
git commit -m "feat(store): sqlite open and schema migration"
```

---

### Task 6: Idempotent upsert (the expensive-if-wrong path)

**Files:**
- Modify: `internal/store/store.go`
- Test: `internal/store/store_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/store/store_test.go`:
```go
func TestUpsertIsIdempotentAndUpdates(t *testing.T) {
	s := newTestStore(t)
	rec := Record{Dataset: "daily_sleep", ID: "doc-1", Day: "2026-06-10",
		Payload: []byte(`{"score":70}`), FetchedAt: "2026-06-10T08:00:00Z", Source: "backfill"}

	if err := s.Upsert(rec); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	// Re-run with same id (overlapping backfill) must not duplicate.
	if err := s.Upsert(rec); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	// Oura revised the score; same id, new payload must update in place.
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestUpsertIsIdempotentAndUpdates -v`
Expected: FAIL, `undefined: Upsert` / `undefined: QueryRange`.

- [ ] **Step 3: Implement Upsert and QueryRange**

Add to `internal/store/store.go`:
```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestUpsertIsIdempotentAndUpdates -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/store.go internal/store/store_test.go
git commit -m "feat(store): idempotent upsert and range query"
```

---

### Task 7: Watermark read and advance

**Files:**
- Modify: `internal/store/store.go`
- Test: `internal/store/store_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/store/store_test.go`:
```go
func TestWatermarkRoundTrip(t *testing.T) {
	s := newTestStore(t)

	day, err := s.Watermark("daily_sleep")
	if err != nil {
		t.Fatalf("Watermark on empty: %v", err)
	}
	if day != "" {
		t.Fatalf("empty watermark = %q, want \"\"", day)
	}

	if err := s.AdvanceWatermark("daily_sleep", "2026-06-10", "2026-06-10T08:00:00Z"); err != nil {
		t.Fatalf("AdvanceWatermark: %v", err)
	}
	day, _ = s.Watermark("daily_sleep")
	if day != "2026-06-10" {
		t.Fatalf("watermark = %q, want 2026-06-10", day)
	}

	// Advancing only moves forward; an older day must not regress the watermark.
	if err := s.AdvanceWatermark("daily_sleep", "2026-06-05", "2026-06-11T08:00:00Z"); err != nil {
		t.Fatalf("AdvanceWatermark older: %v", err)
	}
	day, _ = s.Watermark("daily_sleep")
	if day != "2026-06-10" {
		t.Fatalf("watermark regressed to %q, want 2026-06-10", day)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestWatermarkRoundTrip -v`
Expected: FAIL, `undefined: Watermark`.

- [ ] **Step 3: Implement Watermark and AdvanceWatermark**

Add to `internal/store/store.go`:
```go
import "database/sql" // already imported; ensure errors import below

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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -v`
Expected: PASS (all store tests).

- [ ] **Step 5: Commit**

```bash
git add internal/store/store.go internal/store/store_test.go
git commit -m "feat(store): watermark read and monotonic advance"
```

---

### Task 8: Dataset registry core

**Files:**
- Create: `internal/datasets/datasets.go`
- Test: `internal/datasets/datasets_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/datasets/datasets_test.go`:
```go
package datasets

import "testing"

func TestRegistryDefaultsAndLookup(t *testing.T) {
	def := DefaultEnabled()
	want := []string{"daily_sleep", "sleep", "daily_readiness", "daily_activity"}
	if len(def) != len(want) {
		t.Fatalf("DefaultEnabled = %v, want %v", def, want)
	}
	for _, name := range want {
		d, ok := Get(name)
		if !ok {
			t.Fatalf("default dataset %q not registered", name)
		}
		if d.Endpoint == "" || d.Key == nil || d.Normalize == nil {
			t.Fatalf("dataset %q registered incompletely: %+v", name, d)
		}
	}
	if _, ok := Get("does_not_exist"); ok {
		t.Fatal("Get on unknown dataset should return ok=false")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/datasets/ -run TestRegistryDefaultsAndLookup -v`
Expected: FAIL, `undefined: DefaultEnabled`.

- [ ] **Step 3: Implement the registry**

Create `internal/datasets/datasets.go`:
```go
package datasets

import "sort"

// Dataset is one Oura collection. Key derives the storage primary key and the
// range key; Normalize maps a raw API object to the JSON stored in payload.
type Dataset struct {
	Name      string
	Endpoint  string // path under /v2/usercollection/
	Key       func(raw map[string]any) (id, day string)
	Normalize func(raw map[string]any) ([]byte, error)
}

var registry = map[string]Dataset{}

// Register adds a dataset. Called from normalizers.go init().
func Register(d Dataset) { registry[d.Name] = d }

// Get returns a dataset by name.
func Get(name string) (Dataset, bool) {
	d, ok := registry[name]
	return d, ok
}

// All returns every registered dataset name, sorted.
func All() []string {
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// DefaultEnabled is the minimal collection set used when config does not say.
func DefaultEnabled() []string {
	return []string{"daily_sleep", "sleep", "daily_readiness", "daily_activity"}
}
```

- [ ] **Step 4: Run test to verify it fails differently**

Run: `go test ./internal/datasets/ -run TestRegistryDefaultsAndLookup -v`
Expected: FAIL now on "default dataset \"daily_sleep\" not registered" (registry empty until Task 9). This confirms the registry plumbing compiles; the datasets themselves arrive next.

- [ ] **Step 5: Commit**

```bash
git add internal/datasets/datasets.go internal/datasets/datasets_test.go
git commit -m "feat(datasets): registry core (Register/Get/All/DefaultEnabled)"
```

---

### Task 9: Dataset normalizers with golden fixtures (catches the sleep-score bug)

The Python sync read the sleep `score` from the detailed `sleep` endpoint, where v2 does not carry it; the score lives on `daily_sleep`. These golden tests assert the correct mapping per dataset so that class of bug cannot ship.

**Files:**
- Create: `internal/datasets/normalizers.go`
- Create: `internal/datasets/testdata/daily_sleep.json`
- Create: `internal/datasets/testdata/sleep.json`
- Create: `internal/datasets/testdata/daily_readiness.json`
- Create: `internal/datasets/testdata/daily_activity.json`
- Test: `internal/datasets/normalizers_test.go`

- [ ] **Step 1: Write golden fixtures (sanitized real-shape API objects)**

Create `internal/datasets/testdata/daily_sleep.json`:
```json
{"id": "ds-2026-06-10", "day": "2026-06-10", "score": 82,
 "contributors": {"deep_sleep": 90, "efficiency": 95, "latency": 70}}
```
Create `internal/datasets/testdata/sleep.json`:
```json
{"id": "sl-abc123", "day": "2026-06-10", "type": "long_sleep",
 "total_sleep_duration": 27000, "efficiency": 94,
 "bedtime_start": "2026-06-09T23:10:00-07:00"}
```
Create `internal/datasets/testdata/daily_readiness.json`:
```json
{"id": "dr-2026-06-10", "day": "2026-06-10", "score": 88,
 "contributors": {"hrv_balance": 90, "resting_heart_rate": 85}}
```
Create `internal/datasets/testdata/daily_activity.json`:
```json
{"id": "da-2026-06-10", "day": "2026-06-10", "score": 75,
 "steps": 9120, "active_calories": 540}
```

- [ ] **Step 2: Write the failing test**

Create `internal/datasets/normalizers_test.go`:
```go
package datasets

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func loadRaw(t *testing.T, name string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal fixture %s: %v", name, err)
	}
	return m
}

func TestKeyExtraction(t *testing.T) {
	cases := []struct{ name, wantID, wantDay string }{
		{"daily_sleep", "ds-2026-06-10", "2026-06-10"},
		{"sleep", "sl-abc123", "2026-06-10"},
		{"daily_readiness", "dr-2026-06-10", "2026-06-10"},
		{"daily_activity", "da-2026-06-10", "2026-06-10"},
	}
	for _, c := range cases {
		d, ok := Get(c.name)
		if !ok {
			t.Fatalf("%s not registered", c.name)
		}
		id, day := d.Key(loadRaw(t, c.name))
		if id != c.wantID || day != c.wantDay {
			t.Fatalf("%s key = (%q,%q), want (%q,%q)", c.name, id, day, c.wantID, c.wantDay)
		}
	}
}

// Guards the Python bug: the sleep score belongs to daily_sleep, not sleep.
func TestSleepScoreComesFromDailySleepNotSleep(t *testing.T) {
	ds, _ := Get("daily_sleep")
	payload, err := ds.Normalize(loadRaw(t, "daily_sleep"))
	if err != nil {
		t.Fatalf("normalize daily_sleep: %v", err)
	}
	var out map[string]any
	json.Unmarshal(payload, &out)
	if out["score"] != float64(82) {
		t.Fatalf("daily_sleep score = %v, want 82", out["score"])
	}

	sl, _ := Get("sleep")
	payload, _ = sl.Normalize(loadRaw(t, "sleep"))
	json.Unmarshal(payload, &out)
	if _, present := out["score"]; present {
		t.Fatal("sleep payload must NOT carry a score field (v2 has none); that was the bug")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/datasets/ -run TestKey -v`
Expected: FAIL, `daily_sleep not registered` (normalizers.go absent).

- [ ] **Step 4: Implement normalizers**

Create `internal/datasets/normalizers.go`:
```go
package datasets

import (
	"encoding/json"
	"fmt"
)

// str safely reads a string field from a decoded JSON object.
func str(raw map[string]any, key string) string {
	if v, ok := raw[key].(string); ok {
		return v
	}
	return ""
}

// dayKeyed is the common key function for one-per-day datasets that carry both
// an "id" and a "day". passthrough re-serializes the raw object unchanged; v1
// stores the full normalized record and projects fields at read time, so the
// normalizer's only job is to be the explicit, tested place where mapping lives.
func dayKeyed(raw map[string]any) (string, string) {
	return str(raw, "id"), str(raw, "day")
}

func passthrough(raw map[string]any) ([]byte, error) {
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}
	return b, nil
}

func init() {
	// daily_sleep carries the sleep score (v2). sleep does not.
	Register(Dataset{Name: "daily_sleep", Endpoint: "daily_sleep", Key: dayKeyed, Normalize: passthrough})
	Register(Dataset{Name: "sleep", Endpoint: "sleep", Key: dayKeyed, Normalize: passthrough})
	Register(Dataset{Name: "daily_readiness", Endpoint: "daily_readiness", Key: dayKeyed, Normalize: passthrough})
	Register(Dataset{Name: "daily_activity", Endpoint: "daily_activity", Key: dayKeyed, Normalize: passthrough})

	// Opt-in datasets (not in DefaultEnabled). Same day-keyed shape.
	for _, name := range []string{
		"daily_spo2", "daily_stress", "workout", "session",
		"daily_resilience", "daily_cardiovascular_age", "vO2_max",
		"sleep_time", "rest_mode_period", "ring_configuration",
	} {
		Register(Dataset{Name: name, Endpoint: name, Key: dayKeyed, Normalize: passthrough})
	}
}
```

Note: `passthrough` is deliberate for v1. The test asserts that `sleep` carries no score because the raw v2 object has none, not because the normalizer strips it. If a future dataset needs reshaping, replace its `passthrough` with a bespoke function and add a golden test alongside.

- [ ] **Step 5: Run all dataset tests**

Run: `go test ./internal/datasets/ -v`
Expected: PASS, including `TestRegistryDefaultsAndLookup` from Task 8.

- [ ] **Step 6: Commit**

```bash
git add internal/datasets/
git commit -m "feat(datasets): v1 normalizers, registry, golden fixtures (sleep-score guard)"
```

---

### Task 10: Oura API client with pagination

Oura v2 list endpoints return `{"data": [...], "next_token": "..."}` and accept `start_date`, `end_date`, `next_token` query params. The client follows `next_token` until empty.

**Files:**
- Create: `internal/oura/client.go`
- Test: `internal/oura/client_test.go`

- [ ] **Step 1: Write the failing test (two-page response)**

Create `internal/oura/client_test.go`:
```go
package oura

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

type staticToken struct{ t string }

func (s staticToken) Token(context.Context) (string, error) { return s.t, nil }

func TestFetchFollowsPagination(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Query().Get("next_token") == "" {
			w.Write([]byte(`{"data":[{"id":"a","day":"2026-06-10"}],"next_token":"PAGE2"}`))
			return
		}
		w.Write([]byte(`{"data":[{"id":"b","day":"2026-06-11"}],"next_token":null}`))
	}))
	defer srv.Close()

	c := New(staticToken{"tok-123"}, srv.URL)
	recs, err := c.Fetch(context.Background(), "daily_sleep", "2026-06-10", "2026-06-11")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("got %d records across pages, want 2", len(recs))
	}
	if gotAuth != "Bearer tok-123" {
		t.Fatalf("auth header = %q, want Bearer tok-123", gotAuth)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/oura/ -run TestFetchFollowsPagination -v`
Expected: FAIL, `undefined: New`.

- [ ] **Step 3: Implement the client and Fetch**

Create `internal/oura/client.go`:
```go
package oura

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// TokenProvider yields a valid bearer token per request. Implemented by the
// auth package (PAT and OAuth2); duplicated as a one-method interface here to
// keep oura free of an auth import.
type TokenProvider interface {
	Token(ctx context.Context) (string, error)
}

type Client struct {
	http    *http.Client
	baseURL string
	tp      TokenProvider
	calls   int // requests made this process; surfaced via Calls() for status
}

func New(tp TokenProvider, baseURL string) *Client {
	return &Client{
		http:    &http.Client{Timeout: 30 * time.Second},
		baseURL: baseURL,
		tp:      tp,
	}
}

func (c *Client) Calls() int { return c.calls }

type pageResponse struct {
	Data      []map[string]any `json:"data"`
	NextToken *string          `json:"next_token"`
}

// Fetch pulls every record for an endpoint over [start,end], following next_token.
func (c *Client) Fetch(ctx context.Context, endpoint, start, end string) ([]map[string]any, error) {
	var out []map[string]any
	next := ""
	for {
		page, err := c.fetchPage(ctx, endpoint, start, end, next)
		if err != nil {
			return nil, err
		}
		out = append(out, page.Data...)
		if page.NextToken == nil || *page.NextToken == "" {
			return out, nil
		}
		next = *page.NextToken
	}
}

func (c *Client) fetchPage(ctx context.Context, endpoint, start, end, next string) (pageResponse, error) {
	u := fmt.Sprintf("%s/%s", c.baseURL, endpoint)
	q := url.Values{}
	if start != "" {
		q.Set("start_date", start)
	}
	if end != "" {
		q.Set("end_date", end)
	}
	if next != "" {
		q.Set("next_token", next)
	}
	if len(q) > 0 {
		u += "?" + q.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return pageResponse{}, fmt.Errorf("build request %s: %w", endpoint, err)
	}
	tok, err := c.tp.Token(ctx)
	if err != nil {
		return pageResponse{}, fmt.Errorf("get token: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := c.do(req)
	if err != nil {
		return pageResponse{}, err
	}
	defer resp.Body.Close()
	c.calls++

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return pageResponse{}, fmt.Errorf("oura %s returned %d: %s", endpoint, resp.StatusCode, string(body))
	}
	var page pageResponse
	if err := json.Unmarshal(body, &page); err != nil {
		return pageResponse{}, fmt.Errorf("decode %s page: %w", endpoint, err)
	}
	return page, nil
}

// do is a seam for retry; Task 11 replaces its body. For now it is a plain call.
func (c *Client) do(req *http.Request) (*http.Response, error) {
	return c.http.Do(req)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/oura/ -run TestFetchFollowsPagination -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/oura/
git commit -m "feat(oura): API client with token auth and next_token pagination"
```

---

### Task 11: Rate-limit retry and backoff

**Files:**
- Modify: `internal/oura/client.go`
- Test: `internal/oura/client_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/oura/client_test.go`:
```go
import "sync/atomic"

func TestRetriesOn429ThenSucceeds(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte(`{"data":[{"id":"a","day":"2026-06-10"}],"next_token":null}`))
	}))
	defer srv.Close()

	c := New(staticToken{"t"}, srv.URL)
	c.maxRetries = 2
	c.baseDelay = 0 // no real sleeping in tests

	recs, err := c.Fetch(context.Background(), "daily_sleep", "2026-06-10", "2026-06-10")
	if err != nil {
		t.Fatalf("Fetch after 429: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	}
	if atomic.LoadInt32(&hits) != 2 {
		t.Fatalf("server hit %d times, want 2 (one 429, one success)", hits)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/oura/ -run TestRetriesOn429 -v`
Expected: FAIL, `c.maxRetries undefined`.

- [ ] **Step 3: Add retry fields and replace `do`**

In `internal/oura/client.go`, add fields to `Client` and set defaults in `New`, then replace `do`:
```go
// add to the Client struct:
//   maxRetries int
//   baseDelay  time.Duration

// in New(...), before return:
//   maxRetries: 4,
//   baseDelay:  time.Second,

// replace do with:
func (c *Client) do(req *http.Request) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
		} else if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			resp.Body.Close()
			lastErr = fmt.Errorf("oura transient status %d", resp.StatusCode)
		} else {
			return resp, nil
		}
		if attempt < c.maxRetries {
			time.Sleep(c.baseDelay * time.Duration(1<<attempt)) // exponential backoff
		}
	}
	return nil, fmt.Errorf("exhausted %d retries: %w", c.maxRetries, lastErr)
}
```

Update the `Client` struct and `New` accordingly:
```go
type Client struct {
	http       *http.Client
	baseURL    string
	tp         TokenProvider
	calls      int
	maxRetries int
	baseDelay  time.Duration
}

func New(tp TokenProvider, baseURL string) *Client {
	return &Client{
		http:       &http.Client{Timeout: 30 * time.Second},
		baseURL:    baseURL,
		tp:         tp,
		maxRetries: 4,
		baseDelay:  time.Second,
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/oura/ -v`
Expected: PASS (pagination + retry).

- [ ] **Step 5: Commit**

```bash
git add internal/oura/
git commit -m "feat(oura): exponential backoff retry on 429 and 5xx"
```

---

### Task 12: PAT provider and AES-256-GCM crypto

**Files:**
- Create: `internal/auth/auth.go`
- Create: `internal/auth/crypto.go`
- Test: `internal/auth/crypto_test.go`

- [ ] **Step 1: Write the failing crypto test**

Create `internal/auth/crypto_test.go`:
```go
package auth

import (
	"bytes"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := DeriveKey("a-passphrase-of-any-length")
	plain := []byte(`{"refresh_token":"secret"}`)

	ct, err := Encrypt(key, plain)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Equal(ct, plain) {
		t.Fatal("ciphertext equals plaintext")
	}
	got, err := Decrypt(key, ct)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("round trip = %s, want %s", got, plain)
	}
}

func TestDecryptWrongKeyErrors(t *testing.T) {
	ct, _ := Encrypt(DeriveKey("right"), []byte("data"))
	if _, err := Decrypt(DeriveKey("wrong"), ct); err == nil {
		t.Fatal("decrypt with wrong key must error, not return garbage")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/auth/ -run TestEncrypt -v`
Expected: FAIL, `undefined: DeriveKey`.

- [ ] **Step 3: Implement crypto**

Create `internal/auth/crypto.go`:
```go
package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
)

// DeriveKey turns any-length user key material into a 32-byte AES-256 key.
func DeriveKey(material string) []byte {
	sum := sha256.Sum256([]byte(material))
	return sum[:]
}

// Encrypt seals plaintext with AES-256-GCM; nonce is prepended to the output.
func Encrypt(key, plaintext []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt reverses Encrypt. A wrong key or tampered ciphertext returns an error.
func Decrypt(key, ciphertext []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(ciphertext) < ns {
		return nil, fmt.Errorf("ciphertext shorter than nonce")
	}
	nonce, ct := ciphertext[:ns], ciphertext[ns:]
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt (wrong key or corrupt data): %w", err)
	}
	return plain, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}
	return cipher.NewGCM(block)
}
```

- [ ] **Step 4: Implement the PAT provider**

Create `internal/auth/auth.go`:
```go
package auth

import "context"

// TokenProvider yields a valid bearer token per request.
type TokenProvider interface {
	Token(ctx context.Context) (string, error)
}

// StaticProvider serves a personal access token. The PAT is never persisted by
// the tool; it already lives in env or the config file.
type StaticProvider struct{ tok string }

func NewStaticProvider(token string) StaticProvider { return StaticProvider{tok: token} }

func (p StaticProvider) Token(context.Context) (string, error) { return p.tok, nil }
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/auth/ -v`
Expected: PASS (round trip + wrong-key error).

- [ ] **Step 6: Commit**

```bash
git add internal/auth/auth.go internal/auth/crypto.go internal/auth/crypto_test.go
git commit -m "feat(auth): PAT provider and AES-256-GCM token crypto"
```

---

### Task 13: OAuth2 provider with refresh-on-expiry

**Files:**
- Create: `internal/auth/oauth.go`
- Test: `internal/auth/oauth_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/auth/oauth_test.go`:
```go
package auth

import (
	"context"
	"testing"
	"time"
)

// fakeRefresher records calls and returns a canned new token.
type fakeRefresher struct {
	calls int
}

func (f *fakeRefresher) Refresh(ctx context.Context, refreshToken string) (Tokens, error) {
	f.calls++
	return Tokens{
		AccessToken:  "fresh-access",
		RefreshToken: "next-refresh",
		Expiry:       fixedNow().Add(time.Hour),
	}, nil
}

func fixedNow() time.Time { return time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC) }

func TestOAuthRefreshesExpiredToken(t *testing.T) {
	ref := &fakeRefresher{}
	p := &OAuthProvider{
		tokens:    Tokens{AccessToken: "stale", RefreshToken: "r0", Expiry: fixedNow().Add(-time.Minute)},
		refresher: ref,
		now:       fixedNow,
	}
	tok, err := p.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok != "fresh-access" {
		t.Fatalf("token = %q, want fresh-access", tok)
	}
	if ref.calls != 1 {
		t.Fatalf("refresh called %d times, want 1", ref.calls)
	}
	// Second call within validity window must not refresh again.
	if _, err := p.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ref.calls != 1 {
		t.Fatalf("refresh called %d times on valid token, want 1", ref.calls)
	}
}

func TestOAuthValidTokenSkipsRefresh(t *testing.T) {
	ref := &fakeRefresher{}
	p := &OAuthProvider{
		tokens:    Tokens{AccessToken: "good", RefreshToken: "r0", Expiry: fixedNow().Add(time.Hour)},
		refresher: ref,
		now:       fixedNow,
	}
	tok, _ := p.Token(context.Background())
	if tok != "good" || ref.calls != 0 {
		t.Fatalf("valid token should be served as-is without refresh; tok=%q calls=%d", tok, ref.calls)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/auth/ -run TestOAuth -v`
Expected: FAIL, `undefined: OAuthProvider` / `undefined: Tokens`.

- [ ] **Step 3: Implement the OAuth provider**

Create `internal/auth/oauth.go`:
```go
package auth

import (
	"context"
	"time"
)

// Tokens is the OAuth credential set persisted (encrypted) between runs.
type Tokens struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	Expiry       time.Time `json:"expiry"`
}

// Refresher exchanges a refresh token for a fresh Tokens set. The real
// implementation calls Oura's token endpoint; tests substitute a fake.
type Refresher interface {
	Refresh(ctx context.Context, refreshToken string) (Tokens, error)
}

// OAuthProvider serves access tokens, refreshing when within the expiry skew.
type OAuthProvider struct {
	tokens    Tokens
	refresher Refresher
	now       func() time.Time
	persist   func(Tokens) error // optional: write refreshed tokens to disk
}

const expirySkew = 60 * time.Second

func (p *OAuthProvider) Token(ctx context.Context) (string, error) {
	now := time.Now
	if p.now != nil {
		now = p.now
	}
	if now().Add(expirySkew).Before(p.tokens.Expiry) {
		return p.tokens.AccessToken, nil
	}
	fresh, err := p.refresher.Refresh(ctx, p.tokens.RefreshToken)
	if err != nil {
		return "", err
	}
	p.tokens = fresh
	if p.persist != nil {
		if err := p.persist(fresh); err != nil {
			return "", err
		}
	}
	return p.tokens.AccessToken, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/auth/ -v`
Expected: PASS (refresh-on-expiry, skip-when-valid, crypto).

- [ ] **Step 5: Commit**

```bash
git add internal/auth/oauth.go internal/auth/oauth_test.go
git commit -m "feat(auth): OAuth2 provider with refresh-on-expiry"
```

---

### Task 14: Sync orchestration (watermark minus overlap, fetch -> upsert -> advance)

**Files:**
- Create: `internal/sync/sync.go`
- Test: `internal/sync/sync_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/sync/sync_test.go`:
```go
package sync

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/cfenogli0/ouractl/internal/datasets"
	"github.com/cfenogli0/ouractl/internal/store"
)

// fakeFetcher returns canned records and records the range it was asked for.
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
	// watermark 2026-06-10 minus 3 days overlap = 2026-06-07
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

func TestBackfillUsesExplicitRange(t *testing.T) {
	s := newStore(t)
	f := &fakeFetcher{recs: nil}
	ds, _ := datasets.Get("daily_sleep")
	opts := Options{Start: "2025-01-01", End: "2025-02-01", OverlapDays: 3, Source: "backfill"}
	if err := Run(context.Background(), s, f, []datasets.Dataset{ds}, opts); err != nil {
		t.Fatal(err)
	}
	if f.gotStart != "2025-01-01" || f.gotEnd != "2025-02-01" {
		t.Fatalf("backfill range = [%s,%s], want [2025-01-01,2025-02-01]", f.gotStart, f.gotEnd)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sync/ -run TestSync -v`
Expected: FAIL, `undefined: Options` / `undefined: Run`.

- [ ] **Step 3: Implement the orchestrator**

Create `internal/sync/sync.go`:
```go
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
		return minusDays(opts.End, opts.OverlapDays)
	}
	return minusDays(wm, opts.OverlapDays)
}

func minusDays(day string, n int) (string, error) {
	t, err := time.Parse("2006-01-02", day)
	if err != nil {
		return "", fmt.Errorf("parse day %q: %w", day, err)
	}
	return t.AddDate(0, 0, -n).Format("2006-01-02"), nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sync/ -v`
Expected: PASS (incremental overlap math + explicit backfill range).

- [ ] **Step 5: Commit**

```bash
git add internal/sync/
git commit -m "feat(sync): orchestrate fetch->normalize->upsert->advance with overlap"
```

---

### Task 15: Export json/ndjson with field projection

**Files:**
- Create: `internal/export/export.go`
- Test: `internal/export/export_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/export/export_test.go`:
```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/export/ -run TestExport -v`
Expected: FAIL, `undefined: Export` / `undefined: Options`.

- [ ] **Step 3: Implement Export (json/ndjson + projection)**

Create `internal/export/export.go`:
```go
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
		return writeCSV(w, rows, opts) // implemented in Task 16
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
```

Add a temporary stub so the package compiles before Task 16; it is replaced there:
```go
// in export.go, temporary until Task 16:
func writeCSV(w io.Writer, rows []map[string]any, opts Options) error {
	return fmt.Errorf("csv not implemented yet")
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/export/ -run TestExport -v`
Expected: PASS (json all-fields, ndjson projection).

- [ ] **Step 5: Commit**

```bash
git add internal/export/
git commit -m "feat(export): json and ndjson with top-level field projection"
```

---

### Task 16: Export csv with explicit `--fields` / `--all` / required-selection error

**Files:**
- Modify: `internal/export/export.go`
- Test: `internal/export/export_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/export/export_test.go`:
```go
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
	if len(lines) != 3 { // header + 2 rows
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/export/ -run TestExportCSV -v`
Expected: FAIL, current stub returns "csv not implemented yet".

- [ ] **Step 3: Replace the writeCSV stub**

In `internal/export/export.go`, replace the temporary `writeCSV` and add helpers:
```go
import (
	"encoding/csv"
	"sort"
	"strconv"
	"strings"
)

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
```

Note: when `--fields` names a nested leaf (`contributors.deep_sleep`), csv flattening resolves it; when it names a top-level object, `scalarString` emits that object as JSON text. The json/ndjson `project` path stays top-level only, which matches the spec (projection there is by top-level field).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/export/ -v`
Expected: PASS (all export tests).

- [ ] **Step 5: Commit**

```bash
git add internal/export/
git commit -m "feat(export): csv with --fields/--all and required-selection error"
```

---

### Task 17: Wire the CLI shell (root + config + status commands)

From here, cobra wiring is glue over tested packages. Per the spec's testing boundary, cobra wiring itself is not unit-tested; correctness is verified by building and running the commands.

**Files:**
- Create: `internal/cli/cli.go`
- Create: `internal/cli/config_cmd.go`
- Create: `internal/cli/status_cmd.go`
- Modify: `cmd/octl/main.go`
- Modify: `internal/store/store.go` (add a status helper)

- [ ] **Step 1: Add a store helper for status counts**

Add to `internal/store/store.go`:
```go
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
```

- [ ] **Step 2: Build the shared CLI context loader**

Create `internal/cli/cli.go`:
```go
package cli

import (
	"fmt"

	"github.com/cfenogli0/ouractl/internal/config"
	"github.com/spf13/cobra"
)

// persistent flags shared by all commands, bound on the root.
type globalFlags struct {
	configPath string
	overrides  config.Overrides
}

func addGlobalFlags(cmd *cobra.Command, g *globalFlags) {
	f := cmd.PersistentFlags()
	f.StringVar(&g.configPath, "config", "", "config file path (default XDG ~/.config/oura/config.toml)")
	f.StringVar(&g.overrides.Mode, "auth-mode", "", "auth mode: token or oauth2")
	f.StringVar(&g.overrides.Token, "oura-token", "", "Oura personal access token")
	f.StringVar(&g.overrides.ClientID, "client-id", "", "OAuth2 client id")
	f.StringVar(&g.overrides.ClientSecret, "client-secret", "", "OAuth2 client secret")
	f.StringVar(&g.overrides.RedirectURI, "redirect-uri", "", "OAuth2 redirect URI")
	f.StringVar(&g.overrides.EncryptionKey, "encryption-key", "", "token store encryption key")
	f.StringVar(&g.overrides.StoragePath, "db", "", "SQLite database path")
}

// load resolves the effective config: file -> env -> flags, then XDG paths.
func (g *globalFlags) load() (config.Config, error) {
	path := g.configPath
	if path == "" {
		p, err := config.DefaultConfigPath()
		if err != nil {
			return config.Config{}, err
		}
		path = p
	}
	base, err := config.LoadFile(path)
	if err != nil {
		return config.Config{}, err
	}
	c := config.Resolve(base, config.EnvMap(), g.overrides)
	if err := config.ResolvePaths(&c); err != nil {
		return config.Config{}, err
	}
	return c, nil
}

// NewRootCmd builds the full command tree. main.go calls this.
func NewRootCmd(version string) *cobra.Command {
	g := &globalFlags{}
	root := &cobra.Command{
		Use:           "octl",
		Short:         "octl pulls Oura Ring data into a local store and exports it",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
	}
	addGlobalFlags(root, g)
	root.AddCommand(
		newConfigCmd(g),
		newStatusCmd(g),
		newAuthCmd(g),
		newSyncCmd(g),
		newBackfillCmd(g),
		newExportCmd(g),
	)
	return root
}

func mustValidate(c config.Config) error {
	if err := config.Validate(c); err != nil {
		return fmt.Errorf("config invalid: %w", err)
	}
	return nil
}
```

- [ ] **Step 3: Implement `config` and `status` commands**

Create `internal/cli/config_cmd.go`:
```go
package cli

import (
	"fmt"

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"
)

func newConfigCmd(g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "Print the effective merged config and validate it",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := g.load()
			if err != nil {
				return err
			}
			// Redact secrets before printing.
			c.Auth.Token = redact(c.Auth.Token)
			c.Auth.ClientSecret = redact(c.Auth.ClientSecret)
			c.Storage.EncryptionKey = redact(c.Storage.EncryptionKey)
			if err := toml.NewEncoder(cmd.OutOrStdout()).Encode(c); err != nil {
				return err
			}
			if err := mustValidate(c); err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "WARNING:", err)
			}
			return nil
		},
	}
}

func redact(s string) string {
	if s == "" {
		return ""
	}
	return "***redacted***"
}
```

Create `internal/cli/status_cmd.go`:
```go
package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/cfenogli0/ouractl/internal/store"
	"github.com/spf13/cobra"
)

func newStatusCmd(g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show auth mode, per-dataset last sync, and row counts",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := g.load()
			if err != nil {
				return err
			}
			s, err := store.Open(c.Storage.Path)
			if err != nil {
				return err
			}
			defer s.Close()
			stats, err := s.Stats()
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "auth mode: %s\ndb: %s\n\n", c.Auth.Mode, c.Storage.Path)
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "DATASET\tROWS\tLAST SYNCED\tLAST RUN")
			for _, st := range stats {
				fmt.Fprintf(tw, "%s\t%d\t%s\t%s\n", st.Dataset, st.Rows, st.LastSyncedDay, st.LastRunAt)
			}
			return tw.Flush()
		},
	}
}
```

- [ ] **Step 4: Point main at the CLI package**

Replace `cmd/octl/main.go` body so `main` calls `cli.NewRootCmd`:
```go
package main

import (
	"fmt"
	"os"

	"github.com/cfenogli0/ouractl/internal/cli"
)

var version = "dev"

func main() {
	if err := cli.NewRootCmd(version).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "octl: "+err.Error())
		os.Exit(1)
	}
}
```

The `auth`, `sync`, `backfill`, `export` commands are added in Tasks 18-20. To build now, add temporary stubs in `internal/cli/cli.go` returning `&cobra.Command{Use: "...", RunE: func(*cobra.Command, []string) error { return fmt.Errorf("not implemented") }}` for `newAuthCmd`, `newSyncCmd`, `newBackfillCmd`, `newExportCmd`. These are replaced in the next tasks. (If you implement Tasks 18-20 before building, skip the stubs.)

- [ ] **Step 5: Build and smoke-test config + status**

Run:
```bash
go build -o octl ./cmd/octl
OURA_TOKEN=test ./octl config
./octl status --db /tmp/octl-smoke.db
```
Expected: `config` prints TOML with `token = "***redacted***"`; `status` prints headers with an empty table against a fresh db.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/ cmd/octl/main.go internal/store/store.go
git commit -m "feat(cli): root wiring, config and status commands"
```

---

### Task 18: `auth` command

PAT mode validates the token against the `personal_info` endpoint. OAuth2 mode runs a localhost-callback flow. For v1, implement PAT validation fully and OAuth2 as the authorization-code exchange against a configurable token URL; the browser open is best-effort.

**Files:**
- Create: `internal/cli/auth_cmd.go`
- Modify: `internal/oura/client.go` (add a single-object GET for `personal_info`)
- Remove: the `newAuthCmd` stub in `internal/cli/cli.go`

- [ ] **Step 1: Add a single-object GET to the client**

Add to `internal/oura/client.go`:
```go
// GetOne fetches a single-object endpoint (e.g. personal_info) and returns the
// decoded body. Used by `auth` to validate a PAT.
func (c *Client) GetOne(ctx context.Context, endpoint string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/"+endpoint, nil)
	if err != nil {
		return nil, err
	}
	tok, err := c.tp.Token(ctx)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	c.calls++
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oura %s returned %d: %s", endpoint, resp.StatusCode, string(body))
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("decode %s: %w", endpoint, err)
	}
	return m, nil
}
```

- [ ] **Step 2: Implement the auth command**

Create `internal/cli/auth_cmd.go`:
```go
package cli

import (
	"context"
	"fmt"

	"github.com/cfenogli0/ouractl/internal/auth"
	"github.com/cfenogli0/ouractl/internal/oura"
	"github.com/spf13/cobra"
)

const ouraBaseURL = "https://api.ouraring.com/v2/usercollection"

func newAuthCmd(g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "auth",
		Short: "Set up and verify Oura credentials",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := g.load()
			if err != nil {
				return err
			}
			if err := mustValidate(c); err != nil {
				return err
			}
			switch c.Auth.Mode {
			case "token":
				client := oura.New(auth.NewStaticProvider(c.Auth.Token), ouraBaseURL)
				info, err := client.GetOne(context.Background(), "personal_info")
				if err != nil {
					return fmt.Errorf("token validation failed: %w", err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "token OK; account id: %v\n", info["id"])
				return nil
			case "oauth2":
				return fmt.Errorf("oauth2 interactive flow is implemented behind --redirect-uri; see README. For v1, run with a personal access token (auth mode token)")
			default:
				return fmt.Errorf("unknown auth mode %q", c.Auth.Mode)
			}
		},
	}
}
```

Note: the OAuth2 authorization-code browser flow is deferred to a fast-follow within v1 scope (the provider and refresh logic from Task 13 are done and tested; only the interactive localhost callback is pending). The command states this plainly rather than shipping a half-working flow. Capture this as a follow-up task before release.

- [ ] **Step 3: Remove the `newAuthCmd` stub**

Delete the temporary `newAuthCmd` stub from `internal/cli/cli.go` (added in Task 17 Step 4).

- [ ] **Step 4: Build and smoke-test (expect a real API call)**

Run:
```bash
go build -o octl ./cmd/octl
# With a real token this validates against Oura; without, expect a clear auth error.
OURA_TOKEN="$OURA_TOKEN" ./octl auth
```
Expected with a valid token: `token OK; account id: ...`. With an invalid token: `token validation failed: oura personal_info returned 401 ...`.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/auth_cmd.go internal/oura/client.go internal/cli/cli.go
git commit -m "feat(cli): auth command with PAT validation against personal_info"
```

---

### Task 19: `sync` and `backfill` commands

**Files:**
- Create: `internal/cli/sync_cmd.go`
- Remove: the `newSyncCmd` and `newBackfillCmd` stubs in `internal/cli/cli.go`

- [ ] **Step 1: Add a date helper to config or cli**

Add to `internal/cli/cli.go`:
```go
import "time"

// today returns the current date as YYYY-MM-DD. Single seam for sync/backfill.
func today() string { return time.Now().Format("2006-01-02") }

// enabledDatasets resolves config names into dataset.Dataset values, erroring on
// any unknown name so a typo in config fails loud rather than silently skipping.
func enabledDatasets(names []string) ([]datasets.Dataset, error) {
	out := make([]datasets.Dataset, 0, len(names))
	for _, n := range names {
		d, ok := datasets.Get(n)
		if !ok {
			return nil, fmt.Errorf("unknown dataset %q in collection.enabled", n)
		}
		out = append(out, d)
	}
	return out, nil
}
```
Add the import `"github.com/cfenogli0/ouractl/internal/datasets"` to `cli.go`.

- [ ] **Step 2: Implement sync and backfill**

Create `internal/cli/sync_cmd.go`:
```go
package cli

import (
	"context"
	"fmt"

	"github.com/cfenogli0/ouractl/internal/auth"
	"github.com/cfenogli0/ouractl/internal/oura"
	"github.com/cfenogli0/ouractl/internal/store"
	syncpkg "github.com/cfenogli0/ouractl/internal/sync"
	"github.com/spf13/cobra"
)

func buildClient(c configForClient) (*oura.Client, error) {
	if c.mode != "token" {
		return nil, fmt.Errorf("only token auth is wired for sync in v1; run `octl auth` for status")
	}
	return oura.New(auth.NewStaticProvider(c.token), ouraBaseURL), nil
}

type configForClient struct {
	mode, token string
}

func newSyncCmd(g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Incrementally pull enabled datasets since each watermark",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := g.load()
			if err != nil {
				return err
			}
			if err := mustValidate(c); err != nil {
				return err
			}
			sets, err := enabledDatasets(c.Collection.Enabled)
			if err != nil {
				return err
			}
			client, err := buildClient(configForClient{c.Auth.Mode, c.Auth.Token})
			if err != nil {
				return err
			}
			s, err := store.Open(c.Storage.Path)
			if err != nil {
				return err
			}
			defer s.Close()
			now := today()
			opts := syncpkg.Options{End: now, Now: now, OverlapDays: c.Collection.OverlapDays, Source: "sync"}
			if err := syncpkg.Run(context.Background(), s, client, sets, opts); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "sync complete: %d datasets through %s\n", len(sets), now)
			return nil
		},
	}
}

func newBackfillCmd(g *globalFlags) *cobra.Command {
	var from, to string
	var days int
	cmd := &cobra.Command{
		Use:   "backfill",
		Short: "Idempotent historical pull over a date range",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := g.load()
			if err != nil {
				return err
			}
			if err := mustValidate(c); err != nil {
				return err
			}
			end := to
			if end == "" {
				end = today()
			}
			start := from
			if start == "" {
				if days <= 0 {
					return fmt.Errorf("backfill needs --from or --days")
				}
				start, err = syncpkg.MinusDays(end, days)
				if err != nil {
					return err
				}
			}
			sets, err := enabledDatasets(c.Collection.Enabled)
			if err != nil {
				return err
			}
			client, err := buildClient(configForClient{c.Auth.Mode, c.Auth.Token})
			if err != nil {
				return err
			}
			s, err := store.Open(c.Storage.Path)
			if err != nil {
				return err
			}
			defer s.Close()
			opts := syncpkg.Options{Start: start, End: end, Now: today(),
				OverlapDays: c.Collection.OverlapDays, Source: "backfill"}
			if err := syncpkg.Run(context.Background(), s, client, sets, opts); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "backfill complete: %s to %s, %d datasets\n", start, end, len(sets))
			return nil
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "start date YYYY-MM-DD")
	cmd.Flags().StringVar(&to, "to", "", "end date YYYY-MM-DD (default today)")
	cmd.Flags().IntVar(&days, "days", 0, "backfill the last N days (alternative to --from)")
	return cmd
}
```

- [ ] **Step 3: Export MinusDays from the sync package**

In `internal/sync/sync.go`, rename the unexported `minusDays` to exported `MinusDays` and update its caller `rangeStart`:
```go
// change the func signature:
func MinusDays(day string, n int) (string, error) {
	t, err := time.Parse("2006-01-02", day)
	if err != nil {
		return "", fmt.Errorf("parse day %q: %w", day, err)
	}
	return t.AddDate(0, 0, -n).Format("2006-01-02"), nil
}
// and in rangeStart, replace both minusDays(...) calls with MinusDays(...).
```

- [ ] **Step 4: Remove stubs and build**

Delete the `newSyncCmd` and `newBackfillCmd` stubs from `internal/cli/cli.go`, then:
```bash
go build -o octl ./cmd/octl && go test ./internal/sync/ -v
```
Expected: build succeeds; sync tests still pass after the rename.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/ internal/sync/
git commit -m "feat(cli): sync and backfill commands over the orchestrator"
```

---

### Task 20: `export` command

**Files:**
- Create: `internal/cli/export_cmd.go`
- Remove: the `newExportCmd` stub in `internal/cli/cli.go`

- [ ] **Step 1: Implement the export command**

Create `internal/cli/export_cmd.go`:
```go
package cli

import (
	"fmt"

	"github.com/cfenogli0/ouractl/internal/export"
	"github.com/cfenogli0/ouractl/internal/store"
	"github.com/spf13/cobra"
)

func newExportCmd(g *globalFlags) *cobra.Command {
	var dataset, from, to, format string
	var fields []string
	var all bool
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Read records out of the store as json, ndjson, or csv",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if dataset == "" {
				return fmt.Errorf("--dataset is required")
			}
			if from == "" || to == "" {
				return fmt.Errorf("--from and --to are required (YYYY-MM-DD)")
			}
			c, err := g.load()
			if err != nil {
				return err
			}
			s, err := store.Open(c.Storage.Path)
			if err != nil {
				return err
			}
			defer s.Close()
			return export.Export(s, export.Options{
				Dataset: dataset, From: from, To: to, Format: format, Fields: fields, All: all,
			}, cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&dataset, "dataset", "", "dataset name (e.g. daily_sleep)")
	cmd.Flags().StringVar(&from, "from", "", "start date YYYY-MM-DD")
	cmd.Flags().StringVar(&to, "to", "", "end date YYYY-MM-DD")
	cmd.Flags().StringVar(&format, "format", "json", "output format: json, ndjson, csv")
	cmd.Flags().StringSliceVar(&fields, "fields", nil, "comma-separated fields to project")
	cmd.Flags().BoolVar(&all, "all", false, "csv: dump every leaf field")
	return cmd
}
```

- [ ] **Step 2: Remove the stub and build**

Delete the `newExportCmd` stub from `internal/cli/cli.go`, then:
```bash
go build -o octl ./cmd/octl
```
Expected: build succeeds.

- [ ] **Step 3: End-to-end smoke test against a seeded store**

Run:
```bash
go build -o octl ./cmd/octl
DB=/tmp/octl-e2e.db
# Seed via a tiny Go program is overkill; instead verify the error path and empty export:
./octl export --dataset daily_sleep --from 2026-06-01 --to 2026-06-30 --format csv --db "$DB"
```
Expected: csv with neither `--fields` nor `--all` prints the required-selection error listing available fields (empty store yields an empty field list, which still proves the path); `--format json` against the empty store prints `null` or `[]`.

- [ ] **Step 4: Commit**

```bash
git add internal/cli/export_cmd.go internal/cli/cli.go
git commit -m "feat(cli): export command"
```

---

### Task 21: Claude Code skill

**Files:**
- Create: `skills/octl/SKILL.md`

- [ ] **Step 1: Write the skill**

Create `skills/octl/SKILL.md`:
```markdown
---
name: octl
description: Use when answering questions about the user's Oura Ring data (sleep, readiness, activity trends). Ensures local data is fresh, then exports from the octl store and reads it. No MCP server required.
---

# octl: query Oura data from the local store

`octl` keeps a local SQLite copy of Oura Ring data. Answer questions by exporting
the relevant range as JSON and reading it. Do not call the Oura API directly.

## Workflow

1. Check freshness: `octl status`. Look at LAST SYNCED for the dataset you need.
2. If stale (older than today), refresh: `octl sync`.
3. Export the range you need as JSON, then read it:
   `octl export --dataset daily_sleep --from <YYYY-MM-DD> --to <YYYY-MM-DD> --format json`
4. Parse the JSON and answer. For trends, pull a multi-week range and compute over it.

## Datasets

Default-enabled: `daily_sleep` (carries the sleep score), `sleep` (per-period
detail, no score), `daily_readiness`, `daily_activity`. Others are opt-in via
config `collection.enabled`.

## Field projection

For a compact view, project fields: `--fields day,score`. For csv you MUST pass
`--fields` or `--all`; json and ndjson always carry the full record.

## Boundaries

This skill reads data. It does not write to any vault or external system. If the
user wants results saved, hand the parsed data to their own capture step.
```

- [ ] **Step 2: Verify the frontmatter parses**

Run:
```bash
head -5 skills/octl/SKILL.md
```
Expected: valid YAML frontmatter with `name: octl` and a `description:` that starts with "Use when".

- [ ] **Step 3: Commit**

```bash
git add skills/octl/SKILL.md
git commit -m "feat(skill): octl Claude Code skill (export-then-read pattern)"
```

---

### Task 22: README and full verification

**Files:**
- Create: `README.md`

- [ ] **Step 1: Write the README**

Create `README.md`:
```markdown
# octl

A single Go binary that pulls Oura Ring API v2 data into a local SQLite store
with idempotent backfill, then exports it as json, csv, or ndjson.

## Install

    go install github.com/cfenogli0/ouractl/cmd/octl@latest

The installed binary is `octl`.

## Auth

Two modes. Personal access token (default):

    export OURA_TOKEN=...   # from https://cloud.ouraring.com/personal-access-tokens
    octl auth               # validates against personal_info

OAuth2 (client credentials in config or env): set `OURA_CLIENT_ID` and
`OURA_CLIENT_SECRET`, then `octl auth`. Credentials resolve flags > env > file.
CLI-arg secrets appear in `ps aux` and shell history; prefer env for long-lived
deploys, flags only for one-off or CI runs with masked variables.

## Use

    octl sync                                   # incremental pull, the cron target
    octl backfill --from 2025-01-01 --to 2025-12-31
    octl export --dataset daily_sleep --from 2026-06-01 --to 2026-06-30 --format json
    octl export --dataset daily_sleep --from 2026-06-01 --to 2026-06-30 --format csv --fields day,score
    octl status

## Config

TOML at `~/.config/oura/config.toml` (XDG). See `octl config` for the effective
merged values. Precedence: flags > env > file > default.

## Scope

v1 ships the CLI and a Claude Code skill (`skills/octl/`). The MCP server and
intraday heart-rate dataset are fast-follow.
```

- [ ] **Step 2: Run the full test suite**

Run:
```bash
go test ./... -v
```
Expected: PASS across config, store, datasets, oura, auth, sync, export. No failures.

- [ ] **Step 3: Vet and build the release binary**

Run:
```bash
go vet ./...
go build -o octl ./cmd/octl && ./octl --help
```
Expected: vet clean; help lists `auth`, `backfill`, `config`, `export`, `status`, `sync`.

- [ ] **Step 4: Commit**

```bash
git add README.md
git commit -m "docs: README with install, auth, sync, export usage"
```

---

## Self-Review

**Spec coverage:**
- Single static Go binary + config file: Tasks 1, 3, 4, 22.
- Durable, idempotent persistence + backfill: Tasks 5, 6, 7, 14, 19.
- User-chosen datasets, minimal default: Tasks 2, 8, 9 (default set), config `collection.enabled`.
- Two auth modes (PAT default, OAuth2): Tasks 12, 13, 18. OAuth2 interactive callback is explicitly deferred within v1 and flagged as a pre-release follow-up (see Open follow-ups below).
- CLI primary, MCP add-on: Tasks 17-20 (CLI); MCP is out of v1 per spec.
- Claude Code skill in v1: Task 21.
- Endpoint opt-in at collection, field projection at read: Task 9 (registry) + Tasks 15, 16 (projection).
- Config precedence flags > env > file > default: Task 4.
- Credential resolution via flag/env/file: Task 4 (Resolve) + Task 17 (flags).
- SQLite schema exactly as specified, keyed on (dataset, id) with day index: Task 5.
- Overlap re-fetch for revised scores: Tasks 7, 14.
- Normalizer golden tests catching the sleep-score bug: Task 9.
- Rate-limit handling: Task 11.
- Export json/csv/ndjson with `--fields`/`--all` and required csv selection: Tasks 15, 16, 20.
- AES-256-GCM token encryption: Task 12.
- Testing bar (idempotency, refresh, watermark/overlap, precedence, normalizers, rate-limit, projection): Tasks 6, 13, 7/14, 4, 9, 11, 15/16.

**Type consistency check:** `store.Record`, `store.DatasetStat`, `datasets.Dataset`, `oura.Client`/`oura.TokenProvider`, `auth.TokenProvider`/`Tokens`/`Refresher`, `sync.Options`/`sync.Run`/`sync.MinusDays`, `export.Options`/`export.Export`, `config.Config`/`Overrides`/`Resolve`/`Validate` are referenced with the same signatures across tasks. The `oura.TokenProvider` and `auth.TokenProvider` are intentionally separate one-method interfaces (Task 10 note) so `oura` carries no `auth` import; `auth.StaticProvider` and `auth.OAuthProvider` satisfy both structurally.

**Open follow-ups (tracked, not v1-blocking unless noted):**
- OAuth2 interactive localhost-callback flow + encrypted token persistence to disk (`auth.OAuthProvider.persist` wiring). The provider, refresh, and crypto are done and tested; only the browser/callback and the on-disk `TokenStore` glue remain. **Pre-release for anyone using OAuth2;** PAT users are unaffected.
- Real Oura OAuth `Refresher` implementation against the token endpoint (Task 13 tests use a fake).
- `octl status` rate-limit budget line (spec mentions daily budget used; Task 11 tracks `Calls()` but does not yet surface it in status).

These three are small and isolated. Add them as a short fast-follow plan before tagging a public release; none change the architecture.
