---
id: 0003-console-bff-report-dashboards
title: 0003 — Console-BFF report dashboards aggregating every context's analytics endpoint
sidebar_label: 0003 · Console-BFF report dashboards
description: Why the WMS and WES chart dashboards are assembled by a fan-out in this BFF over each bounded context's own /reports endpoint, with per-section graceful degradation, rather than by a new analytics service or by the browser calling seven services directly.
---

# ADR 0003: Console-BFF report dashboards aggregating every context's analytics endpoint

## Status

Accepted

**Date:** 2026-09-05

## Context

ADR-0002 established this repository as the home of a thin
Backend-for-Frontend for **cross-service reads** the micro-frontend
console cannot satisfy from any single context's API, and shipped one
capability under that heading: `GET /console/orders/{id}/lifecycle`.

Since then, seven contexts have each grown an **analytics data product**:
a separate projector and read-only reports binary over its own analytical
Postgres, published as a pair of endpoints.

| Context | Report endpoint | Grain |
|---|---|---|
| `order-management` | `/reports/funnel` | hour × pathId |
| `inventory-storage` | `/reports/flow-accuracy` | hour × sku × bin |
| `wes-work-planning` | `/reports/throughput` | hour × pathId |
| `fulfillment-execution` | `/reports/throughput` | hour × taskType × station |
| `workforce-management` | `/reports/labor` | hour × pathId |
| `facility-layout` | `/reports/catalog-growth` | **day** × scope |
| `labor-performance` | `/reports/performance` | hour × taskType |

Each also publishes `/reports/<name>/freshness`, returning
`{"lagSeconds": <float>}` — how far that projection trails its event
stream. On all seven, `from` and `to` are **required** RFC3339 query
parameters (verified by reading each repo's own `reports_handler.go`, not
assumed).

The requirement: two operator dashboards — a **WMS Dashboard** and a
**WES Dashboard** — each a handful of charts drawn from three or four of
those contexts at once. Nothing about a chart of order-funnel totals
next to a chart of catalog growth belongs to any one context's domain.

The forces:

- **A dashboard is a cross-service read, which is exactly what ADR-0002
  put here.** Its argument applies unchanged: no service reads another's
  database, and no context can own a screen that is definitionally about
  several contexts.
- **Seven browser calls is a worse contract than one.** Having the SPA
  call seven reports services directly would push per-service base URLs,
  seven CORS configurations, seven auth headers, seven partial-failure
  states, and all the aggregation arithmetic into the browser — and would
  make the fan-out's latency the sum of the slowest round-trip *per
  panel* over the public network rather than over the cluster network.
- **A dashboard must survive a dead upstream.** An operator looking at
  four panels should lose one panel, not the screen. This is the same
  force that shaped `OrderLifecycle`, one level up.
- **Charts are not the place to invent numbers.** `labor-performance`
  deliberately serialises JSON `null` — never a fabricated `0` — for
  `meanEfficiencyPct` when nothing in a bucket was scorable (its
  ADR-0004/0006/0007). Any aggregation over it must preserve that
  distinction or it silently converts "we don't know" into "0%
  efficient", which reads to an operator as a catastrophe rather than as
  an absence.

## Decision

Add a **second console-bff capability** in this repository — alongside,
not replacing, the order-lifecycle read model:

```
GET /console/reports/wms?from=<RFC3339>&to=<RFC3339>
GET /console/reports/wes?from=<RFC3339>&to=<RFC3339>
```

Both return an **identical envelope**, so the console renders either
dashboard with one component that switches only on each section's
`chartKind`:

```json
{
  "from": "2026-09-04T00:00:00Z",
  "to": "2026-09-05T00:00:00Z",
  "generatedAt": "2026-09-05T00:12:00Z",
  "sections": [
    {
      "id": "order-funnel",
      "title": "Order Funnel",
      "sourceContext": "order-management",
      "chartKind": "funnel",
      "available": true,
      "error": null,
      "freshnessLagSeconds": 12.3,
      "series": [{"label": "Received", "value": 120}]
    }
  ]
}
```

The decisions that shape it:

1. **`chartKind` is a closed three-value vocabulary** — `funnel`, `bar`,
   `line` — because the console switches on it to pick
   `warehouse-ui-kit`'s `FunnelChart` / `BarChart` / `LineChart`. All
   three take a `{label, value}[]` prop, so every section — regardless of
   kind — emits that one `series` shape. The BFF decides the chart kind
   because the BFF is what knows the shape of the data it just
   aggregated.

2. **Per-section graceful degradation, never a failed dashboard.** A
   slow, down, erroring, or simply unwired upstream sets that one
   section's `available: false`, a short human `error`, and `series: []`.
   Neither endpoint has an error path for upstream failure at all — the
   use case's `Execute*` methods have no `error` return, which makes the
   guarantee structural rather than a discipline someone must remember.
   This mirrors `OrderLifecycle`'s per-stage tolerance exactly.

3. **`from`/`to` are optional here, unlike on all seven upstreams**,
   defaulting to a 24-hour trailing window ending now. This is a screen a
   human opens without necessarily knowing what window to ask for. A
   parameter that *is* supplied must still be well-formed: a malformed or
   inverted window is a 400, never a silently substituted default.

4. **The fan-out is concurrent**, diverging from `OrderLifecycle`'s
   documented sequential v1. That choice was right for a 4-hop
   single-entity lookup where each hop's join key comes from the previous
   one; here the 3–4 calls per dashboard are fully independent, and
   serialising them would multiply dashboard latency by the number of
   contexts for no benefit. Sections are reassembled by index, so render
   order never depends on which upstream answered first.

5. **Freshness annotates, it does not gate.** `freshnessLagSeconds` comes
   from each context's own freshness endpoint, fetched alongside the
   report. If the freshness call fails but the report succeeded, the
   section stays `available: true` with `freshnessLagSeconds: null`.

6. **Null means are skipped, never zeroed.** `labor-performance`'s
   nullable `meanEfficiencyPct` is carried as `*float64` from the port
   DTO all the way to the series; a task type whose mean is null gets no
   bar. Modelling it as a plain `float64` anywhere in the chain would
   launder "no data" into "0%" at the first JSON decode.

7. **A third port shape.** `internal/ports/console_reports_clients.go`
   joins the MCP client ports (LLM-facing, curated tools) and the
   order-lifecycle REST ports (OLTP entity lookups). These are distinct
   because they call a *different process* — each context's `*-reports`
   reader binary, backed by a different analytical database, on a
   different base URL from that context's OLTP API.

Each upstream's fetch-and-aggregate lives in its own small function
behind a `sectionSpec`, so adding an eighth context is one spec entry
plus one aggregate function — never a change to the fan-out machinery.

## Consequences

- The console gets two dashboards from two calls, with one envelope, one
  CORS origin, and one partial-failure model.
- This BFF's failure surface grows: it now depends on seven more services
  at request time. The degradation model means that shows up as missing
  panels rather than as an outage, and each degraded section names the
  context that failed.
- The aggregation arithmetic (which counters sum into "Allocated", how
  day buckets fold across scopes) now lives here rather than in the
  owning context. That is a real coupling: a context that adds a counter
  gets no new chart until this repo maps it. The alternative — each
  context publishing a pre-chunked chart series — would push console
  presentation concerns into seven domains, which is worse.
- Two contexts (`wes-work-planning`, `fulfillment-execution`) expose the
  same `/reports/throughput` path with different row shapes. They get two
  separate ports rather than one shared abstraction; the shared path is a
  coincidence, not a contract.
- `labor-performance`'s reports reader is the newest in the fleet and may
  not be deployed in a given environment. That is a normal, tested
  degradation of one WES section, not a deployment ordering constraint on
  this service.

### Deferred

- **Local-dev port assignments for the `*-reports` binaries are new here
  and not yet wired fleet-wide.** Every `*-reports` binary currently
  defaults to the same `HTTP_ADDR=":8092"` — which also collides with
  `e2e-tests/env.sh`'s `INVENTORY_MCP_PORT` — so running more than one
  locally already requires per-service overrides. This repo's config
  therefore assigns a range, mirroring `env.sh`'s existing 8081–8086 OLTP
  ordering shifted by +20 and clear of the 8081–8096 OLTP/MCP/agent
  range:

  | Env var | Default |
  |---|---|
  | `FACILITY_LAYOUT_REPORTS_REST_URL` | `http://localhost:8101` |
  | `INVENTORY_STORAGE_REPORTS_REST_URL` | `http://localhost:8102` |
  | `WES_WORK_PLANNING_REPORTS_REST_URL` | `http://localhost:8103` |
  | `FULFILLMENT_EXECUTION_REPORTS_REST_URL` | `http://localhost:8104` |
  | `WORKFORCE_MANAGEMENT_REPORTS_REST_URL` | `http://localhost:8105` |
  | `ORDER_MANAGEMENT_REPORTS_REST_URL` | `http://localhost:8106` |
  | `LABOR_PERFORMANCE_REPORTS_REST_URL` | `http://localhost:8107` |

  These are **defaults this repo proposes, not a convention the fleet
  currently honours**. Propagating them into `e2e-tests/env.sh` and each
  repo's `docker-compose.yml` is deliberately out of scope: silently
  editing seven other repositories' compose files from this change would
  be a fleet-wide decision made in a BFF PR. Until that wiring lands, a
  local run needs the env vars set explicitly, and any unset/unreachable
  upstream degrades its own section — visibly, by design.
- **Charts are not yet paginated or downsampled.** A very wide `from`/`to`
  produces one point per hour bucket with no server-side thinning.
- **No caching.** Every dashboard load re-fans-out. The upstreams are
  reading pre-aggregated projections, so this is cheap today; a shared
  short-TTL cache is the obvious next step if it stops being.
