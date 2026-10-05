---
id: 0007-second-wave-outbound-mcp-clients
title: "0007 — Second-wave outbound MCP clients: order-management, labor-performance, process-path-management wired but unconsumed"
sidebar_label: "0007 · Second-wave MCP clients"
description: Adding three outbound MCP-client adapters for order-management, labor-performance, and process-path-management as available dependencies in the composition root, without wiring them into any existing use case yet.
---

# ADR 0007: Second-wave outbound MCP clients — order-management, labor-performance, process-path-management wired but unconsumed

## Status

Accepted

**Date:** 2026-09-11

## Context

`warehouse-ops-agent` started as a Customer of five bounded contexts'
published MCP Open Host Services (`wes-work-planning`,
`fulfillment-execution`, `inventory-storage`, `workforce-management`,
`facility-layout`), each reached through a thin, schema-typed client in
`internal/adapters/outbound/mcpclient/` and a matching port interface in
`internal/ports/`. A fleet-wide MCP-server wiring pass has since brought
three more bounded contexts online with their own published, read-only
MCP tool surfaces: `order-management` (`get_order`), `labor-performance`
(`get_associate_scorecard`, `get_task_type_performance`,
`get_labor_standard`), and `process-path-management`
(`get_process_path`, `list_process_paths`).

This repo already has a precedent for exactly this situation:
`InventoryStorageClient` was wired into the composition root
(`cmd/agent/main.go`) as soon as inventory-storage's MCP server existed,
well before the E3 daily brief use case had a reason to call it — see the
`_ = inv` line there, annotated "not used by the E3 daily brief; kept
wired for T2/T3 use cases." The same shape now applies to these three new
upstreams: the agent should be able to reach them as soon as their
servers exist, independent of whether any current use case (`DailyBrief`,
`FlowBalanceAdvisory`, the ADR-0004 reasoner) has a reason to call them
yet.

## Decision

1. Add three outbound MCP-client adapters, mirroring the exact shape of
   the five existing clients (`facility_layout.go` is the reference:
   embed a `*mcpclient.Session`, one exported method per published tool,
   a `var _ ports.XClient = (*X)(nil)` compile-time assertion):
   - `internal/adapters/outbound/mcpclient/order_management.go` —
     `OrderManagement.GetOrder`.
   - `internal/adapters/outbound/mcpclient/labor_performance.go` —
     `LaborPerformance.GetAssociateScorecard`,
     `.GetTaskTypePerformance`, `.GetLaborStandard`.
   - `internal/adapters/outbound/mcpclient/process_path_management.go` —
     `ProcessPathManagement.GetProcessPath`, `.ListProcessPaths`.
2. Add the three corresponding port interfaces and their tool-boundary
   DTOs to `internal/ports/clients_phase2.go`, a new file rather than an
   edit to `clients.go`, mirroring this repo's existing convention of
   splitting port families into their own file
   (`order_lifecycle_clients.go`, `console_reports_clients.go`). The
   order-management MCP port is named `OrderManagementMCPClient`, not
   `OrderManagementClient`, because that name is already taken by the
   REST port in `order_lifecycle_clients.go` — the two are deliberately
   separate port families (MCP tools for LLM-facing use cases vs. plain
   REST for the console-bff), and a shared name across them would blur
   that distinction.
3. Add three `UpstreamConfig` fields to `internal/config.Config`
   (`OrderManagement`, `LaborPerformance`, `ProcessPathManagement`) and
   their `Load()` reads (`ORDER_MANAGEMENT_MCP_ENDPOINT`,
   `LABOR_PERFORMANCE_MCP_ENDPOINT`,
   `PROCESS_PATH_MANAGEMENT_MCP_ENDPOINT`), each defaulting to an empty
   string exactly like the five existing endpoints.
4. Construct the three clients in `cmd/agent/main.go` next to the
   existing five, assign each to a typed port variable (`om`, `lp`,
   `ppm`), and mark them wired-but-unconsumed with `_ = om` / `_ = lp` /
   `_ = ppm`, annotated the same way as the existing `_ = inv` /
   `_ = telem` lines. None of the three is wired into `DailyBrief`,
   `FlowBalanceAdvisory`, or any other existing use case struct — this is
   additive outbound-adapter wiring only.
5. Full unit test coverage for the three new client files
   (`order_management_test.go`, `labor_performance_test.go`,
   `process_path_management_test.go`), each spinning up a real
   Streamable-HTTP MCP test server via `httptest.NewServer` +
   `mcp.NewStreamableHTTPHandler` (the same pattern
   `tool_invoker_test.go` already uses) rather than hitting a real
   network or an upstream repo's binary.

## Consequences

**Positive** — a future use case (a T2/T3-style order-lifecycle
correlation, a labor-coaching alert, a process-path-aware routing
decision, or a new tool on the ADR-0004 reasoner's allow-list) can start
calling `order-management`, `labor-performance`, or
`process-path-management` by consuming the already-wired `om`/`lp`/`ppm`
variables and adding it to the relevant use case struct — no new adapter,
port, or config plumbing required at that point. The composition root's
startup log already reports each upstream's configured-endpoint boolean
in the same style as the five existing ones would, once that log line is
extended (left for the use case that actually needs it, to keep this
change's diff minimal).

**Negative / accepted** — three more Go types and three more env vars
exist in the codebase with no current caller, exactly the same tradeoff
the `InventoryStorageClient` precedent already accepted. Anyone auditing
"what does this agent actually use today" needs to check the use case
structs, not just the composition root, to see which upstreams are truly
load-bearing versus reachable-but-idle. `go vet`/`staticcheck`-style dead
code warnings are suppressed by the explicit `_ = om` pattern, matching
the existing precedent rather than introducing a new one.

**No behavior change today** — `DailyBrief`'s correlation logic, the
`internal/domain/policy` layer, and the ADR-0004 reasoner's tool
allow-list are all untouched by this change. The daily brief's HTTP and
MCP responses are byte-for-byte identical before and after.

## Alternatives considered

- **Wait until a real use case needs one of these upstreams before adding
  any adapter code.** Rejected for consistency with the
  `InventoryStorageClient` precedent this repo already established: the
  cost of wiring an adapter ahead of its first consumer is low (one file,
  one port, one config field, one `_ =` line) and doing it as each
  upstream's MCP server lands keeps the outbound-adapter inventory
  synchronized with the fleet's actual capabilities, rather than having
  every future use-case PR also need to invent its adapter from scratch.
- **Fold the three new port interfaces into the existing
  `internal/ports/clients.go`.** Rejected in favor of a new
  `clients_phase2.go` file: this repo already splits port families by
  file when they were added in a distinct wave (order-lifecycle,
  console-reports), and doing the same here keeps this addition's diff
  isolated and easy to review without touching the original five-context
  file at all.

## Addendum (2026-09-26): labor-performance is no longer unconsumed

This ADR's title and Context/Consequences prose above describe all three
clients (`order-management`, `labor-performance`, `process-path-management`)
as "wired but unconsumed" as of this ADR's original acceptance. That is
now stale for **labor-performance** specifically: [ADR 0008](./0008-labor-utilization-advisory-correlation.md)
wired the `lp` client built here into `FlowBalanceAdvisory` (see
`internal/application/usecases/flow_balance_advisory.go`'s `LP` field and
`gatherUtilizationSignal`), so `get_flow_balance_exception` has consumed
it since ADR 0008 landed. `order-management` and `process-path-management`
remain genuinely unconsumed today (`_ = om` / `_ = ppm` in
`cmd/agent/main.go`, zero non-test references elsewhere) — this addendum
does not change that.

Per this fleet's convention, the Context/Decision/Consequences sections
above are left as an accurate historical record of the decision as made;
this addendum is appended rather than editing that prose in place.
