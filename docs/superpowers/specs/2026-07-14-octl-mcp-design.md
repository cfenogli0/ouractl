# octl mcp: MCP server over the store (v2)

Date: 2026-07-14
Status: Approved
Builds on: [2026-06-10-oura-tool-design.md](2026-06-10-oura-tool-design.md), which reserved `internal/mcp/` and the `octl mcp` command.

## Context

v1 shipped conversational access without MCP: the Claude Code skill drives `octl export` and reads the output. That covers Claude Code. It does not cover clients that cannot run arbitrary shell commands, Claude Desktop being the one that matters here. The v1 spec deliberately deferred the MCP server; this spec designs it.

The differentiator claim from v1 still holds and shapes this design: existing Oura MCP servers are live-query proxies against the Oura API. `octl mcp` serves the local SQLite store, so answers come from the longitudinal record, offline, with no per-question API spend.

## Goals

- `octl mcp` serves MCP over stdio; a client (Claude Desktop, Claude Code) spawns it as a child process.
- Query and freshness tools over the existing store, plus an on-demand sync tool.
- Zero new persistence, zero new query logic: tools are thin adapters over `export`, `store`, and `sync`.
- Read tools work even when auth is broken; only the `sync` tool requires a working token store.

## Non-goals

- No HTTP transport. stdio covers every current consumer; HTTP adds bind/port config and an auth story with no consumer to justify them. Revisit if a remote client (claude.ai connector, home-lab deploy) materializes.
- No MCP resources or prompts in this iteration. Tools are the whole surface.
- No aggregation or summary tools (weekly averages, trend detection). That logic exists nowhere in octl today; inventing it inside MCP tool handlers would create a second place where domain math lives. The model aggregates from rows.

## Decisions

| Decision | Choice | Rejected, and why |
|---|---|---|
| Transport | stdio only | HTTP (alone or additional): no current remote consumer; adds auth and lifecycle surface now for a hypothetical later. |
| Tool surface | Generic `query` + `status` + `sync` | Per-dataset tools (get_sleep, get_readiness, ...): N tools to maintain, each new dataset means a new tool, drifts from the registry-driven design. Convenience summary tools: embed aggregation opinions octl does not otherwise have. |
| Integration | Tools call `export.Export`, `store.Stats`, `sync.Run` | Direct `store.QueryRange` + re-projection in `internal/mcp`: duplicates projection logic that lives unexported in `export`, exactly the drift class the registry design avoids. Shared `internal/query` refactor: rework of tested v1 code to serve one new consumer, YAGNI. |
| Sync via MCP | Yes, a `sync` tool | Read-only server: cleaner, but the operator wants on-demand freshness from chat; the OS scheduler still owns routine sync, the tool covers "is this current right now". |
| Auth acquisition | Lazy, on first `sync` call | Eager at startup: would make a broken keychain take down read-only sessions that never sync. |
| Oversized results | Hard row cap, explicit tool error | Silent truncation: the model would present partial data as complete, the worst failure mode for a data tool. |
| SDK | Official `modelcontextprotocol/go-sdk` | `mark3labs/mcp-go`: more battle-tested today, but the ecosystem converged on the official SDK and v1 already committed to it. |

## Architecture

```
internal/cli/mcp_cmd.go   `octl mcp`: open store, build lazy auth closure, serve stdio, exit on client disconnect
internal/mcp/server.go    tool registration, server construction (takes store + fetcher-factory + registry)
internal/mcp/tools.go     query / status / sync handlers
```

`internal/mcp` depends on `store`, `export`, `sync`, `datasets`. It must not import `oura` or `auth` directly; the CLI passes a `func() (sync.Fetcher, error)` closure, keeping client construction in one place (`internal/cli`, same path `sync_cmd` uses) and making the server testable with a fake fetcher.

## Tools

### query

Params: `dataset` (enum, generated from `datasets.All()`), `from`, `to` (YYYY-MM-DD, both required), `fields` (optional string list), `limit` (optional, default 200).

Handler counts matching rows first. Over `limit`: tool error stating the count and instructing the model to narrow the range, project fewer fields, or raise `limit`. Under: `export.Export` with `format: json` into a buffer, returned as the tool result text. Output is byte-identical to `octl export`, so anything the skill learned about the shape transfers.

### status

No params. Per-dataset rows from `store.Stats()` plus watermarks: dataset, row count, last synced day, last sync run time. The tool description tells the model to call this first when freshness matters and to report staleness to the user rather than silently answering from old data.

### sync

Params: `datasets` (optional subset, default: enabled set from config), `days` (optional backfill window, same semantics as the CLI flag). Calls the fetcher-factory (first auth touch), then `sync.Run`. Returns per-dataset rows-upserted counts. Failure returns the underlying API or auth error as a tool error; the server keeps running.

## Error handling

All failures inside handlers are MCP tool-result errors, visible to the model: unknown dataset (message lists valid names), malformed date, never-synced dataset ("no rows; run the sync tool or `octl sync`"), sync/auth failures with the underlying error string. Protocol-level errors are reserved for actual protocol violations. The server process exits only on stdio close or a store open failure at startup.

## Testing

Covers the paths where failure is silent or expensive, per the repo's test policy:

- Each tool end-to-end over the SDK's in-memory transport: schema accepted, handler wired, result parses.
- Row cap boundary: `limit` rows passes, `limit+1` errors with the count in the message.
- Unknown dataset and malformed date produce tool errors, not crashes.
- `sync` tool drives a fake `sync.Fetcher`; asserts upsert counts and that auth is not touched before the first `sync` call (the no-auth regression: `query` and `status` succeed with a fetcher-factory that errors).

Not covered here: export formatting details (owned by `export`'s tests), live Oura API behavior (owned by `oura`'s tests).

## Consequences

- Claude Desktop gets first-class access to the longitudinal store; the skill remains the Claude Code path and MCP stays optional, as v1 promised.
- The `sync` tool spends real Oura rate-limit budget from chat, and the spawned process must be able to read the token store; on macOS keychain this means the same binary path across client config and terminal use, or the keychain re-prompts.
- stdio-only means nothing to deploy and no auth surface, at the cost of a second design pass if a remote consumer ever appears.
- Docs: README gains an MCP section (Claude Desktop JSON block, `claude mcp add` one-liner). SKILL.md is untouched.
