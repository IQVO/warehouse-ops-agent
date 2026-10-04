---
id: api-surface
title: API surface
sidebar_label: API Surface
description: The REST endpoint and MCP tools warehouse-ops-agent exposes. No OpenAPI spec yet — documented here in prose.
---

# API surface

`warehouse-ops-agent` has no `apis/openapi.yaml` — its surface is small
enough that it is documented here in prose rather than generated. Every
route is a `GET`, and neither surface is authenticated (see
[ADR 0006](./adr/0006-fleet-wide-auth-removal.md)). This page is kept current
by hand; if it drifts from `internal/adapters/inbound/`, the code is
authoritative.

## REST (`internal/adapters/inbound/http`)

| Method & path | What it returns |
|---|---|
| `GET /healthz` | `{"status": "ok"}` |
| `GET /daily-brief` | The full synthesized `DailyBrief`: every monitored site's paths with backlog/staffing/queue/stuck-task facts, plus ranked `openExceptions`. When `WAREHOUSE_PLANNING_MCP_ENDPOINT` is set, each path also carries an optional `capacityOutlook` ([ADR 0013](./adr/0013-warehouse-planning-mcp-client-and-capacity-outlook.md)): `normalizedRate` (ORDER/hour), `bottleneckStep`, `bindingConstraint`, per-step `steps`, planning's `warnings`, over `[windowStart, windowEnd)` (`CAPACITY_OUTLOOK_HORIZON`, default 8h) — or only `omittedReason` when it could not be produced (path has no `planningPathId`, planning unreachable, no covering capacity window, …). The key is absent when planning is not configured; the section never fails the brief and never changes `openExceptions`. |
| `GET /flow-balance/{pathId}?buildingId=&shiftId=` | The E1 `FlowBalanceException` correlation for one path; `buildingId`/`shiftId` scope the workforce-management staffing-gap lookup. Optionally arbitrated by the ADR-0004 LLM reasoner (`LLM_MODE`) and enriched with the ADR-0008 labor-utilization correlation. 400 on a use-case error; 503 if the use case isn't wired. |
| `GET /explain-travel-factor?pathId=&fromLocationCode=&toLocationCode=` | Calls facility-layout's `estimate_travel_distance` for the two REQUIRED, caller-supplied location codes and classifies the result (`travel_significant`/`travel_negligible`) against the ADR-0009 threshold. 400 if either location code is missing; 503 if the use case isn't wired. This agent never infers the two location codes itself — see [ADR 0009](./adr/0009-explain-travel-factor.md). |
| `GET /console/orders/{id}/lifecycle` | The **console-bff** read model (see [ADR 0002](./adr/0002-micro-frontend-console-architecture.md)): fans out to order-management, inventory-storage, wes-work-planning, and fulfillment-execution and stitches one order's cross-service lifecycle for `warehouse-console`'s Order Lifecycle screen. Each stage degrades independently — one context being unreachable never 500s the whole response. |
| `GET /console/reports/wms?from=&to=` | The **console-bff** WMS dashboard ([ADR 0003](./adr/0003-console-bff-report-dashboards.md)): three sections — `order-funnel` (order-management `/reports/funnel`), `inventory-flow-accuracy` (inventory-storage `/reports/flow-accuracy`), `catalog-growth` (facility-layout `/reports/catalog-growth`) — each read from that context's separate `*-reports` binary together with its `/freshness` lag. `from`/`to` are optional RFC3339 timestamps (default: trailing 24 h); 400 if either is malformed or `to` is not after `from`. Each section degrades independently (`available: false` + `error`). |
| `GET /console/reports/wes?from=&to=` | The **console-bff** WES dashboard: `planning-throughput` (wes-work-planning `/reports/throughput`), `fulfillment-throughput` (fulfillment-execution `/reports/throughput`), `labor-management` (workforce-management `/reports/labor`), `labor-performance` (labor-performance `/reports/performance`). Same window, validation and per-section degradation as the WMS dashboard. |
| `GET /runtime-signals` | Per-service runtime health for the eight backend contexts (override with `RUNTIME_SIGNALS_SERVICES`) over a 10-minute window: Istio 5xx error rate (`istio_requests_total`) and p99 latency (`istio_request_duration_milliseconds_bucket`) from Prometheus, plus error/fatal log-line counts from Loki (`{namespace="warehouse-systems"}`). Classified by `policy.ClassifyErrorRate` (warning ≥ 1%, critical ≥ 5%) and `policy.ClassifyLatencyP99` (warning ≥ 1000 ms, critical ≥ 3000 ms); any recent error log lifts a service to at least `warning`. A failing Prometheus or Loki query, or an unset `LOKI_URL`, is listed in `unavailableSources` (`prometheus`, `loki`) instead of failing the request; an unset `PROMETHEUS_URL` uses a no-op stub reader, so its metrics read as zero rather than unavailable. 503 if the use case isn't wired. |

## MCP (`internal/adapters/inbound/mcp`)

This agent runs its **own** MCP server (Streamable HTTP, unauthenticated —
see [ADR 0006](./adr/0006-fleet-wide-auth-removal.md)) so that an agentic
host can consume its recommendations the same way it consumes any bounded
context's facts.

| Tool | What it does |
|---|---|
| `get_daily_brief` | Returns the full synthesized `DailyBrief` (including the optional per-path `capacityOutlook`, as on `GET /daily-brief`). |
| `list_open_exceptions` | Lists open exceptions, optionally filtered to a minimum `severity` (`info`/`warning`/`critical`). An unrecognized severity value is rejected, never silently defaulted. |
| `get_flow_balance_exception` | Correlates the E1 signals for one `pathId` (+ `buildingId`/`shiftId` for the staffing lookup) into a ranked `FlowBalanceException`. |
| `explain_travel_factor` | Calls facility-layout's `estimate_travel_distance` for two REQUIRED, caller-supplied location codes (`fromLocationCode`/`toLocationCode`) and classifies the result. The caller must already know both codes — this tool never infers or guesses them (see [ADR 0009](./adr/0009-explain-travel-factor.md)). |
| `detect_stranded_reservation` | The E2 `StrandedReservationException` use case: correlates fulfillment-execution's `diagnose_stuck_tasks` expired/expiring leases for a `taskType` with inventory-storage's `check_availability` usable-stock shortfall for one `sku`. Only ever recommends `revoke_reservation` alongside its mandatory blast radius (`get_bin_occupancy`, requiring both `reservationId` and `binId`) — never on partial evidence; degrades to `hold` otherwise. This tool never calls inventory-storage's `revoke_reservation` write tool itself. |

All five tools are annotated read-only
(`mcp.ToolAnnotations{ReadOnlyHint: true}`). This agent has **zero write
tools** — see the [Governance note](./mcp/governance-note.md) for why that
is a v1 design choice, not an oversight.

## What is not yet exposed

`order-management` and `process-path-management`'s outbound MCP clients
are wired in the composition root (`cmd/agent/main.go`) but not yet
consumed by any use case (`_ = om` / `_ = ppm`) — see
[ADR 0007](./adr/0007-second-wave-outbound-mcp-clients.md) and its
2026-09-26 addendum. That is a separate, still-open follow-up from the E2
StrandedReservation wiring above.

`warehouse-planning`'s outbound client
([ADR 0013](./adr/0013-warehouse-planning-mcp-client-and-capacity-outlook.md))
is consumed only through `get_process_path_capacity` (the daily brief's
capacity outlook); `get_capacity_plan`, `get_storage_capacity` and
`list_station_standards` are wired but not yet consumed by any use case.
Its MCP server is read+write; this agent calls read tools only.
