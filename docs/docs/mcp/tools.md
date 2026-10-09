---
id: tools
title: MCP tools
sidebar_label: MCP tools
description: Every tool on warehouse-ops-agent's own MCP server, with its input fields, read-only annotation, registration condition and the upstream calls behind it.
---

# MCP tools

warehouse-ops-agent runs its own MCP server so an agentic host can read
its recommendations the same way it reads a bounded context's facts. The
server is mounted at `/mcp` on the same router and port as the REST routes
(`AGENT_ADDR`, default `:8095`). It uses Streamable HTTP in stateless mode
(`internal/adapters/inbound/mcp/server.go`) and needs no authentication
([ADR 0006](../adr/0006-fleet-wide-auth-removal.md)). There is no separate
`cmd/mcp` binary.

The server identifies itself as `warehouse-ops-agent-mcp` version `1.0.0`.
Every tool call runs inside an OTel span named `mcp.tool <name>` with the
attribute `mcp.tool.name` (`addTool` in `tools.go`).

## Tool list

The table below comes from a live `tools/list` call against a binary built
from this branch, with the three optional upstreams configured. "Required"
is the `required` array of the published input schema: the go-sdk marks
every field without `omitempty` as required.

| Tool | Read-only | Registered when | Required inputs | Optional inputs | Same answer as |
|---|---|---|---|---|---|
| `get_daily_brief` | yes | always | none | none | `GET /daily-brief` (without `generatedAt`) |
| `list_open_exceptions` | yes | always | none | `severity` | the `openExceptions` of `GET /daily-brief` |
| `get_flow_balance_exception` | yes | always (the use case is always wired) | `buildingId`, `shiftId`, `pathId` | none | `GET /flow-balance/{pathId}` |
| `explain_travel_factor` | yes | always | `pathId`, `fromLocationCode`, `toLocationCode` | none | `GET /explain-travel-factor` |
| `detect_stranded_reservation` | yes | always | `taskType`, `sku`, `minUsableThreshold` | `withinSeconds`, `reservationId`, `binId` | no REST route |
| `find_master_data_gaps` | yes | `PRODUCT_MASTER_MCP_ENDPOINT` is set | none | `kind`, `cursor` | `GET /master-data-gaps` |
| `get_inbound_outlook` | yes | `INBOUND_RECEIVING_MCP_ENDPOINT` is set | none | none | `GET /inbound-outlook` |
| `triage_stuck_transfers` | yes | `NETWORK_INVENTORY_PLANNING_MCP_ENDPOINT` is set | `olderThanMinutes` | `state`, `limit` | `GET /transfer-watch/stuck` |
| `get_transfer_status` | yes | `NETWORK_INVENTORY_PLANNING_MCP_ENDPOINT` is set | `transferId` | none | `GET /transfer-watch/transfers/{id}` |
| `explain_network_imbalance` | yes | `NETWORK_INVENTORY_PLANNING_MCP_ENDPOINT` is set | none | none | `GET /transfer-watch/imbalance` |

All ten carry `ReadOnlyHint: true`. The agent has no write tool.
`internal/architecture/zerowrite/zerowrite_test.go`
(`TestNoMutatingToolAnnotationInMCPServer`) fails the build if a tool is
registered without the read-only hint. See the
[governance note](./governance-note.md) for why.

`explain_travel_factor` requires `pathId` in its schema although the use
case only logs it. A host must send it, and an empty string is accepted.

## Inputs and behaviour

### `get_daily_brief`

No input. Returns `sites` and `openExceptions` with the same shape as
[`GET /daily-brief`](../api/http-routes.md). It never fails: an unavailable
upstream shows up in a path's `unavailable` list.

### `list_open_exceptions`

| Field | Type | Meaning |
|---|---|---|
| `severity` | string, optional | Minimum severity: `info`, `warning` or `critical`. Omit it to get every exception. |

Returns `count` and `exceptions[]`. Any other `severity` value is a tool
error, `invalid severity "<value>": must be info, warning, or critical`
(`errors.go`). It is never replaced by a default.

### `get_flow_balance_exception`

| Field | Type | Meaning |
|---|---|---|
| `buildingId` | string | building of the path, for the workforce-management staffing lookup |
| `shiftId` | string | shift to check staffing against |
| `pathId` | string | wes-work-planning path id |

Returns `pathId`, `recommendedAction` (`assign_labor`, `release_next_work`
or `hold`), `proposedHeads`, `rationale`, `partial`, `missingSignals`,
`evidence[]` (`source`, `detail`), `utilization` and `source`
(`deterministic`, `llm` or `fallback`). It fails only when
wes-work-planning returns an unknown rebalance action. The upstream calls
are the ones listed under `GET /flow-balance/{pathId}` on the
[HTTP routes](../api/http-routes.md) page.

### `explain_travel_factor`

| Field | Type | Meaning |
|---|---|---|
| `pathId` | string | path under investigation, only logged |
| `fromLocationCode` | string | seven-segment facility-layout code, e.g. `WH1-STOR-AMB-A07-01-01-A` |
| `toLocationCode` | string | seven-segment facility-layout code |

Calls facility-layout `estimate_travel_distance` and returns `metresM`,
`estimated`, `kind` (`travel_significant` above 60 m, else
`travel_negligible`) and `rationale`. A missing code, or a facility-layout
validation rejection, is a tool error whose text starts with
`invalid input:`. The tool never guesses a location code
([ADR 0009](../adr/0009-explain-travel-factor.md)).

### `detect_stranded_reservation`

| Field | Type | Meaning |
|---|---|---|
| `taskType` | string | `PICK`, `PACK` or `SLAM`; anything else is a tool error (`policy: unknown task type`) |
| `withinSeconds` | integer, optional | window passed to fulfillment-execution `diagnose_stuck_tasks`; 0 means already-expired leases only |
| `sku` | string | SKU suspected of being stranded |
| `minUsableThreshold` | integer | usable quantity at or below which a shortfall counts as correlated |
| `reservationId` | string, optional | reservation the tool may recommend revoking |
| `binId` | string, optional | bin holding that reservation, used for the blast radius |

Upstream calls: fulfillment-execution `diagnose_stuck_tasks`,
inventory-storage `check_availability` (when `sku` is set) and
inventory-storage `get_bin_occupancy` (only when both `reservationId` and
`binId` are set).

Returns `detected`, `action` (`revoke_reservation` or `hold`),
`reservationId`, `rationale`, `evidence[]` (`tool`, `summary`) and
`blastRadius` (`sku`, `binId`, `reservationId`, `quantityFreed`,
`binLines[]`). `revoke_reservation` is recommended only when expired
leases of `taskType` exist, usable stock is at or below the threshold, a
`reservationId` was given and the bin occupancy was read. Every other path
returns `hold`. The tool never calls inventory-storage's
`revoke_reservation` itself.

### `find_master_data_gaps`

| Field | Type | Meaning |
|---|---|---|
| `kind` | string, optional | `unclassified` or `dimension-discrepancy`; omit for both |
| `cursor` | string, optional | `nextCursor` of an earlier incomplete scan |

Same report as `GET /master-data-gaps`: at most 5,000 products per call,
`complete: false` plus `nextCursor` when the bound is hit. An unknown
`kind` is a tool error. A product-master failure is a tool error too: the
report has no partial mode.

### `get_inbound_outlook`

No input. Same four sections as `GET /inbound-outlook`, each with
`omitted`, `complete`, `count` and `items`. It is a tool error only when
every attempted section failed.

### `triage_stuck_transfers`

| Field | Type | Meaning |
|---|---|---|
| `olderThanMinutes` | integer | a transfer is stuck when its state has not changed for longer than this; it must be positive |
| `state` | string, optional | one non-terminal state: `DRAFT`, `PROPOSED`, `APPROVED`, `ALLOCATING`, `ALLOCATED`, `PICKED`, `IN_TRANSIT`, `ARRIVED` |
| `limit` | integer, optional | page size, 0 to 200; 0 uses the network-inventory-planning default |

Calls network-inventory-planning `find_stuck_transfers`. Returns `total`,
`transfers[]` (each with `triage`: `cause`, `summary`, `nextCheck`) and
`causeCount[]`.

### `get_transfer_status`

| Field | Type | Meaning |
|---|---|---|
| `transferId` | string | inter-warehouse transfer id |

Calls network-inventory-planning `get_transfer`. Returns the transfer, its
`audit[]` trail and its `triage`. An unknown id is a tool error whose text
starts with `transfer not found:`.

### `explain_network_imbalance`

No input. Calls network-inventory-planning `simulate_transfer_options` and
returns `advisory`, `asOf`, `imbalanced`, `sites[]` (`balance` is `short`
or `covered`), `summary` and `nextCheck`. It is a tool error while
network-inventory-planning's read models are not fresh. The tool never
proposes a quantity or a route.

## Tool errors

A tool handler's Go error becomes an MCP result with `isError: true` and
the error text as content (`addTool` returns it to the go-sdk). The text is
the use case's own message, for example
`invalid input: find stuck transfers: olderThanMinutes must be positive`.
This server does not prefix its errors with a kebab-case slug, unlike the
upstream servers that follow the convention in
[ADR 0018](../adr/0018-mcp-tool-error-slug-classification.md).

## Calling the server

The server is stateless, so `initialize` returns no `Mcp-Session-Id` and
every request stands alone:

```bash
curl -s http://localhost:8095/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'
```

The answer comes back as one `event: message` server-sent event whose
`data:` line holds the JSON-RPC response.

## The upstream tools this agent calls

The outbound side (`internal/adapters/outbound/mcpclient`) is listed per
upstream on [Integration](../ecosystem/integration.md). The LLM reasoner
can call only the tools on `LLM_TOOL_ALLOWLIST`
([Configuration](../operations/configuration.md)).
