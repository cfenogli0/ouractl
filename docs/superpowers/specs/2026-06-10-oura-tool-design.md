# Oura Data Tool: Design Spec

- **Status:** Draft, pending user review
- **Date:** 2026-06-10
- **Author:** Calib (with Claude)
- **Type:** Design / explanation. One job: capture the architecture and the decisions behind it so implementation can proceed from a single source.

> Working name: `oura` (binary), module `github.com/cfenogli0/oura`. The repo slug is not final; pick before public release.

---

## Context

A growing number of Oura "MCP servers" exist (for example the Node server at oura.makemcp.dev). They share two weaknesses: they are live-query proxies with no durable store, and they require a language runtime (Node or Python) to deploy. None of them persist a longitudinal record or backfill history. That gap is the opportunity.

Calib already runs a personal Oura sync (`~/dev/sync-tools/scripts/oura_sync.py`): Python, OAuth2-only, 5 hardcoded metrics, JSON files on disk, vault-coupled output. It already does backfill and persistence, the two things worth keeping. This project extracts that capability into a standalone, configurable, public tool and makes persistence and easy deployment the differentiators.

The existing Python sync also carries a latent bug worth not reproducing: it reads the sleep `score` from the detailed `sleep` endpoint, where that field does not exist in Oura API v2 (the score lives on `daily_sleep`). The dataset normalizer tests below exist to catch exactly this class of mapping error.

## Goals

- A single static Go binary plus one config file. Install and deploy without a runtime.
- Durable, queryable persistence with safe, idempotent backfill.
- User chooses which datasets are collected; the default is a minimal set, not everything.
- Two auth modes: Oura personal access token (default) and OAuth2.
- CLI and exports are the primary interface. The MCP server is an optional add-on.
- A Claude Code skill ships in v1 so the tool is usable conversationally without an MCP server.

## Non-goals

- No built-in scheduler daemon in v1. The OS scheduler (cron, launchd, systemd timer) invokes `oura sync`.
- No web-auth session capture. Oura exposes no supported hook; scraping would be fragile for little gain.
- No `pkg/` public Go library in v1. The binary is the product. A client library can be promoted later, deliberately.
- No vault coupling in the OSS tool. Vault wiring stays in the user's own layer.

## Decisions and rejected alternatives

| Decision | Choice | Rejected, and why |
|---|---|---|
| Language | Go | TypeScript: native MCP ecosystem but needs a runtime at deploy, weakening the single-binary story that is the differentiator. Go + TS split: two artifacts to maintain for one product. |
| Storage | SQLite source of truth, with export commands | JSON files only: weak backfill dedup, history is a rewrite-the-blob file, no real query. SQLite + JSON mirror: two stores to keep in sync for marginal readability gain. |
| Data selection | Endpoint opt-in at collection; field selection at read | Field selection at collection time poisons the stored history, so changing your mind forces a re-backfill from the API. Endpoint-only everywhere is too coarse for shaping MCP output. |
| Project layout | `main.go` at root + `internal/` | `cmd/oura/`: only earns its keep with multiple binaries (we have one), and it makes `go install` path uglier. `golang-standards/project-layout`: not an official standard; cargo-culting its full tree adds empty ceremony. |
| Invocation | On-demand CLI in v1; MCP is the only long-running mode, in v2 | Built-in daemon: adds supervision and lifecycle surface the OS schedulers already solve better. |
| Auth default | Personal access token via `OURA_TOKEN` | OAuth2-only (the current Python approach): forces app registration and a refresh flow on every user, including single-owner deploys that do not need it. OAuth2 stays available as the second mode. |
| SQLite driver | Pure-Go (`modernc.org/sqlite`) | cgo driver (`mattn/go-sqlite3`): faster but breaks the static-binary, cross-compile-clean promise. |

## Architecture

Single binary, cobra-based commands, isolated internal packages.

```
main.go              package main, cobra root wiring
internal/config/     load + merge (flags > env > file > default), validate
internal/auth/       TokenProvider: PAT and OAuth2, encrypted token store
internal/oura/       API client: endpoints, pagination, retry, rate-limit accounting
internal/datasets/   registry: each dataset = {name, endpoint, key(raw)->(id,day), normalize(raw)->payload}
internal/store/      SQLite: migrations, idempotent upsert, range queries, watermarks
internal/sync/       orchestration: range -> fetch -> normalize -> upsert -> advance watermark
internal/export/     readers + json/csv/ndjson formatters, field projection
internal/mcp/        (v2) MCP tools over the store
```

The `datasets` registry is the keystone. Adding a dataset is one registry entry, no schema migration, because the store is generic (see Data model). Endpoint opt-in is simply which registry entries the config enables.

### Commands

| Command | Job |
|---|---|
| `oura auth` | Set up and verify credentials. PAT: validate against `personal_info`. OAuth2: run the localhost-callback flow, store the refresh token, report the live mode. |
| `oura sync` | Incremental pull of enabled datasets since each dataset watermark, minus an overlap window. The cron target. |
| `oura backfill --from D --to D` (or `--days N`) | Idempotent historical pull over a range. Safe to re-run. |
| `oura export --dataset D --from D --to D --format json\|csv\|ndjson [--fields ...]` | Read out of SQLite. Field projection lives here. |
| `oura status` | Auth state, per-dataset last sync, row counts, daily rate-limit budget used. |
| `oura config` | Print the effective merged config and validate it. |
| `oura mcp` | (v2) Serve MCP over the same store. |

### Data flow, one sync run

```
config.enabled[] -> for each dataset:
   determine range (watermark - overlap_days, or backfill flags)
   oura.client.fetch(endpoint, range)        (paginated, retried, rate-limit-aware)
   datasets.normalize(raw) -> payload, datasets.key(raw) -> (id, day)
   store.upsert(dataset, id, day, payload)    (idempotent on (dataset, id))
   store.advanceWatermark(dataset, day)
export / mcp -> store.query(dataset, range) -> project fields -> format
```

## Data model

Keyed on the Oura document `id`, not on `day`, because several datasets are not one-per-day (workouts and sessions are N-per-day, sleep can include a nap period). `day` is retained as an indexed column for range queries.

```sql
CREATE TABLE records (
  dataset    TEXT NOT NULL,
  id         TEXT NOT NULL,   -- Oura document id (or timestamp for intraday datasets)
  day        TEXT NOT NULL,   -- YYYY-MM-DD, for range queries
  payload    TEXT NOT NULL,   -- normalized JSON (SQLite JSON1)
  fetched_at TEXT NOT NULL,   -- RFC3339
  source     TEXT NOT NULL,   -- 'sync' | 'backfill'
  PRIMARY KEY (dataset, id)
);
CREATE INDEX idx_records_dataset_day ON records(dataset, day);

CREATE TABLE sync_state (
  dataset         TEXT PRIMARY KEY,
  last_synced_day TEXT,
  last_run_at     TEXT
);

CREATE TABLE schema_meta (version INTEGER NOT NULL);
```

The upsert is `INSERT ... ON CONFLICT(dataset,id) DO UPDATE`. This is load-bearing because Oura revises scores after the fact: a sleep score can change hours after the night. Incremental sync therefore re-fetches the last `overlap_days` so revisions land as updates rather than duplicates or stale rows. The JSON `payload` column makes "store the full normalized record, project fields at read" a `json_extract` call, which is what keeps the collection-vs-exposure split cheap.

Each dataset registry entry declares `key(raw) -> (id, day)`, so a future intraday dataset (heart rate, which has timestamped samples and no daily document id) keys on its timestamp without a schema change.

## Configuration

TOML, matching the existing `claude-os.toml` convention. Default location `~/.config/oura/config.toml` (XDG). Precedence: **flags > env > file > built-in default**, each value resolved independently.

```toml
[auth]
mode = "token"              # "token" (default) or "oauth2"
# token: prefer OURA_TOKEN env over file
# oauth2: client_id, client_secret, redirect_uri

[storage]
path = "~/.local/share/oura/oura.db"
# encryption_key: prefer OURA_ENCRYPTION_KEY env

[collection]
enabled = ["daily_sleep", "sleep", "daily_readiness", "daily_activity"]  # minimal default, not everything
overlap_days = 3

[logging]
level = "info"             # error|warn|info|debug
```

### Credential resolution

Every credential resolves down the same chain. Flags, env, and file are all accepted; they can be mixed.

| Credential | Flag | Env | Config file |
|---|---|---|---|
| PAT | `--oura-token` | `OURA_TOKEN` | `[auth] token` |
| Client ID | `--client-id` | `OURA_CLIENT_ID` | `[auth] client_id` |
| Client secret | `--client-secret` | `OURA_CLIENT_SECRET` | `[auth] client_secret` |
| Redirect URI | `--redirect-uri` | `OURA_REDIRECT_URI` | `[auth] redirect_uri` |
| Encryption key | `--encryption-key` | `OURA_ENCRYPTION_KEY` | `[storage] encryption_key` |
| Mode | `--auth-mode` | `OURA_AUTH_MODE` | `[auth] mode` |

CLI-arg secrets are the least safe of the three sources: they appear in `ps aux` (other users on the host can read them) and in shell history. The docs state this plainly and recommend env for long-lived deploys and flags only for one-off or CI runs where the value comes from a masked variable. The tool supports all three regardless.

## Auth modes

A `TokenProvider` interface yields a valid bearer per request.

| Mode | Setup | Refresh | At rest |
|---|---|---|---|
| `token` (PAT, default) | `OURA_TOKEN` | none | not stored; already in env or file |
| `oauth2` | `oura auth` runs a localhost-callback flow and opens the browser | auto on 401 or expiry | access + refresh encrypted with AES-256-GCM, key from `OURA_ENCRYPTION_KEY` |

Only OAuth2 persists a token (the refresh token), and only that is encrypted at rest. The PAT is not copied into storage; it already lives in env or the config file, so a stored duplicate would only widen exposure. Token files are written `0600`.

## Dataset registry

The single extension point. A registry entry:

```
{
  name:      "daily_sleep",
  endpoint:  "daily_sleep",
  key:       func(raw) (id string, day string),   // derive primary key + range key
  normalize: func(raw) (payload []byte),           // map raw API object to stored JSON
}
```

v1 default-enabled set: `daily_sleep`, `sleep`, `daily_readiness`, `daily_activity`. Available but opt-in: `daily_spo2`, `daily_stress`, `workout`, `session`, `daily_resilience`, `daily_cardiovascular_age`, `vO2_max`, `personal_info`, `sleep_time`, `rest_mode_period`, `ring_configuration`. Intraday `heart_rate` is deferred to a later version because its timestamp-keyed shape needs a distinct key strategy; the registry already accommodates it.

## Export

`export` reads from SQLite and writes one of three formats: `json` (array of records), `csv` (flattened, selected fields), `ndjson` (one record per line, for streaming into other tools). `--fields` projects via `json_extract`; `--from`/`--to` bound the range. Export never calls the API; it is a pure read over the store.

## Claude Code skill (v1)

Ships at `skills/oura/SKILL.md`. It teaches Claude Code to run `oura status`/`oura sync` to ensure freshness, then `oura export --dataset … --format json` and read the result to answer questions about trends. It is vault-agnostic: it describes the export-then-read pattern and leaves any vault write to the user's own layer. This skill is the concrete proof that conversational access does not require the MCP server.

## MCP server (v2, designed-for not built)

`internal/mcp/` will expose query tools (sleep, readiness, activity, workouts, range queries) over the same store, using the official Go MCP SDK (`modelcontextprotocol/go-sdk`). It reuses `store` and `export` projection; it adds no new persistence. Kept out of v1 to ship a tight, tested core first. The architecture reserves the package and the `oura mcp` command so adding it is additive.

## Testing

Per the project owner's bar: cover the paths whose failure is silent, irreversible, or expensive. Not breadth for its own sake.

100% coverage on:

- **Backfill idempotency.** An overlapping re-run produces no duplicate rows and updates revised values. This is the expensive-if-wrong path; double-counted history is the whole reason backfill is scary.
- **OAuth2 refresh.** An expired access token triggers a refresh; a failed refresh errors loudly rather than serving empty or stale data.
- **Watermark and overlap.** Incremental sync advances the watermark correctly and absorbs revised scores from the overlap window.
- **Config precedence.** flags > env > file > default, including the `OURA_TOKEN` override.
- **Dataset normalizers.** Golden fixtures per dataset assert the raw-to-normalized mapping. This is the test that would have caught the wrong-endpoint sleep-score bug in the Python sync.
- **Rate-limit handling.** A 429 triggers backoff and retry; daily budget accounting does not exceed the cap.
- **Export projection.** Field and date selection return exactly the requested shape.

Infra: `httptest` Oura mock with sanitized real-payload fixtures; pure-Go SQLite runs store tests on a temp database with no external dependency. Not tested: cobra wiring, log formatting, trivial accessors.

## Security

- Secrets default to env; the config file may hold them but the docs do not encourage it; CLI-arg secrets are documented as least-safe.
- OAuth2 tokens encrypted at rest (AES-256-GCM); token files `0600`.
- The encryption key is never written into the config file by tooling.
- No telemetry. The tool talks to the Oura API and the local store, nothing else.

## v1 scope boundary

In: core CLI (`auth`, `sync`, `backfill`, `export`, `status`, `config`), both auth modes, config system with full precedence, endpoint-opt-in collection, SQLite persistence with idempotent upsert, backfill, exports, the dataset registry with the default and opt-in sets above, and one Claude Code skill.

Out (fast-follow, separate plans): the MCP server, intraday heart-rate dataset, any scheduler/`--watch` mode, a published Go client library.

## Open questions

- Final repo and binary name (`oura` is a working placeholder; consider collision and discoverability).
- Whether `export --format csv` flattens nested JSON automatically or requires explicit `--fields`. Leaning explicit `--fields` for CSV to avoid surprising column sets.
