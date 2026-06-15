---
name: octl
description: Use when answering questions about the user's Oura Ring data (sleep, readiness, activity trends). Ensures the local data is fresh, then exports from the octl store and reads it. No MCP server required.
---

# octl: query Oura data from the local store

`octl` keeps a local SQLite copy of Oura Ring data. Answer questions by exporting
the relevant range as JSON and reading it. Do not call the Oura API directly.

## Workflow

1. Check freshness: `octl status`. Read the LAST SYNCED column for the dataset you need.
2. If it is older than today, refresh: `octl sync`.
3. Export the range as JSON, then read it:
   `octl export --dataset daily_sleep --from <YYYY-MM-DD> --to <YYYY-MM-DD> --format json`
4. Parse the JSON and answer. For trends, pull a multi-week range and compute over it.

## Datasets

Default-enabled: `daily_sleep` (carries the sleep score), `sleep` (per-period
detail, no score), `daily_readiness`, `daily_activity`. Others (spo2, stress,
workout, session, resilience, and more) are opt-in via config `collection.enabled`.

The sleep score lives on `daily_sleep`, not `sleep`. Pull `daily_sleep` for scores.

## Field projection

For a compact view, project top-level fields: `--fields day,score`. For `csv` you
must pass `--fields` (top-level names) or `--all` (every leaf, dotted paths);
`json` and `ndjson` always carry the full record.

## Boundaries

This skill reads data. It does not write to any vault or external system. If the
user wants results saved, hand the parsed data to their own capture step.
