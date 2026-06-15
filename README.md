# octl

A single Go binary that pulls Oura Ring API v2 data into a local SQLite store
with idempotent backfill, then exports it as json, csv, or ndjson. The repo is
`ouractl`; the installed command is `octl`.

## Why this exists

Other Oura tools are live-query proxies: no durable store, no history, and they
need a language runtime to deploy. `octl` persists a longitudinal record and
backfills history, shipping as one static binary plus one config file. Persistence
and easy deployment are the point.

## Install

    go install github.com/cfenogli0/ouractl/cmd/octl@latest

## Auth

Two modes. Personal access token (default):

    export OURA_TOKEN=...   # from https://cloud.ouraring.com/personal-access-tokens
    octl auth               # validates against personal_info

OAuth2 client credentials resolve the same way but the interactive browser flow
is a fast-follow; v1 runs on a personal access token. Every credential resolves
flags > env > file, mixable per value:

| Credential | Flag | Env | Config |
|---|---|---|---|
| token | `--oura-token` | `OURA_TOKEN` | `[auth] token` |
| client id | `--client-id` | `OURA_CLIENT_ID` | `[auth] client_id` |
| client secret | `--client-secret` | `OURA_CLIENT_SECRET` | `[auth] client_secret` |
| encryption key | `--encryption-key` | `OURA_ENCRYPTION_KEY` | `[storage] encryption_key` |

CLI-arg secrets appear in `ps aux` and shell history. Prefer env for long-lived
deploys; use flags only for one-off or CI runs where the value comes from a
masked variable.

## Use

    octl sync                                              # incremental pull, the cron target
    octl backfill --from 2025-01-01 --to 2025-12-31        # or --days 90
    octl export --dataset daily_sleep --from 2026-06-01 --to 2026-06-30 --format json
    octl export --dataset daily_sleep --from 2026-06-01 --to 2026-06-30 --format csv --fields day,score
    octl status                                            # per-dataset last sync + row counts
    octl config                                            # effective merged config (secrets redacted)

`sync` re-fetches the last `overlap_days` (default 3) so Oura's after-the-fact
score revisions land as updates, not duplicates. `backfill` is safe to re-run:
records upsert on (dataset, id).

For `csv`, field selection is explicit: `--fields` names top-level fields, `--all`
dumps every leaf as dotted paths (`contributors.deep_sleep`). Omitting both is an
error that lists the available fields. `json` and `ndjson` always carry the full
record.

## Config

TOML at `~/.config/oura/config.toml` (XDG). Datasets are opt-in: the default set
is `daily_sleep`, `sleep`, `daily_readiness`, `daily_activity`. Enable more under
`[collection] enabled`.

    [auth]
    mode = "token"

    [collection]
    enabled = ["daily_sleep", "sleep", "daily_readiness", "daily_activity"]
    overlap_days = 3

## Scope

v1 ships the CLI and a Claude Code skill (`skills/octl/`) so you can query the
data conversationally without an MCP server. The MCP server, the OAuth2 browser
flow, and the intraday heart-rate dataset are fast-follow.
