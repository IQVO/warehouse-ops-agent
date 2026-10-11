---
id: index
title: Warehouse Ops Agent
sidebar_label: Introduction
description: The read-side decision-support agent that correlates the fleet's bounded contexts into one ranked, human-gated recommendation.
---

# Warehouse Ops Agent

:::warning[Study project]
This documentation site is an educational Domain-Driven Design exercise. It
follows real industry-standard patterns and terminology, but it is **not a
production system** and is **not affiliated with, endorsed by, or
representative of any real-world company**.
:::

**Warehouse Ops Agent** is the fleet's *agentic* layer: the "AI teammate
that sees, analyzes, and recommends" over the warehouse-systems bounded
contexts — originally five (`inventory-storage`, `wes-work-planning`,
`fulfillment-execution`, `workforce-management`, `facility-layout`), plus
three second-wave MCP clients (`labor-performance`, `order-management`,
`process-path-management`; see
[ADR 0007](../adr/0007-second-wave-outbound-mcp-clients.md)), plus
`warehouse-planning` (read tools only,
[ADR 0013](../adr/0013-warehouse-planning-mcp-client-and-capacity-outlook.md)), plus
`product-master` (read-only,
[ADR 0020](../adr/0020-product-master-mcp-client-and-master-data-gaps.md)), plus
`inbound-receiving` (read-only,
[ADR 0021](../adr/0021-inbound-receiving-mcp-client-and-inbound-outlook.md)), plus
`network-inventory-planning` (read tools only,
[ADR 0019](../adr/0019-network-inventory-planning-transfer-watch.md)). It is a
**Customer** of those contexts' published MCP Open Host Services — it owns
no aggregate, enforces no new business invariant, and persists no domain
state. Its "domain" layer is decision **policy**: pure correlation rules
over facts read from those contexts.

## What it is not

It is **not a sixth bounded context** in the domain sense. See
[ADR 0001](../adr/0001-warehouse-ops-agent-placement.md) for the full
placement reasoning: there is no aggregate or invariant for this repo to
own, so calling it a "context" would be a domain in name only.

## The see → analyze → act surface it consumes

Each upstream context already exposes a curated, intent-level MCP read
surface (the fleet's
[MCP Governance Charter](https://iqvo.github.io/fulfillment-execution/docs/mcp/governance-charter)).
This agent's outbound adapters (`internal/adapters/outbound/mcpclient/`)
are thin, schema-typed clients over exactly those tools — never a Go
import of any sibling's internal packages:

| Upstream context | Read tools the client implements | Consumed today by |
|---|---|---|
| `wes-work-planning` | `get_backlog_telemetry`, `get_rebalance_recommendation` | daily brief, flow balance |
| `fulfillment-execution` | `get_queue_status`, `find_claimable_work`, `diagnose_stuck_tasks` | daily brief, flow balance |
| `workforce-management` | `get_staffing_gap`, `propose_path_heads` | daily brief, flow balance (`get_staffing_gap`) |
| `facility-layout` | `list_sites`, `get_site_layout`, `get_zone_grid`, `estimate_travel_distance` | daily brief (`list_sites`), explain travel factor |
| `inventory-storage` | `check_availability`, `get_bin_occupancy` | E2 stranded reservation (MCP tool `detect_stranded_reservation`) |
| `labor-performance` | `get_associate_scorecard`, `get_task_type_performance`, `get_labor_standard`, `get_task_type_utilization` | flow-balance utilization overlay (`get_task_type_utilization`, [ADR 0008](../adr/0008-labor-utilization-advisory-correlation.md)) |
| `order-management` | `get_order` | nothing yet (wired, unconsumed) |
| `process-path-management` | `get_process_path`, `list_process_paths` | nothing yet (wired, unconsumed) |
| `warehouse-planning` | `get_process_path_capacity`, `get_capacity_plan`, `get_storage_capacity`, `list_station_standards` (read tools only; its write tools are never called) | daily brief capacity outlook (`get_process_path_capacity`, [ADR 0013](../adr/0013-warehouse-planning-mcp-client-and-capacity-outlook.md)); the other three wired, unconsumed |
| `product-master` | `get_product`, `list_products`, `get_product_classification`, `get_physical_profile` (its whole, read-only surface) | master-data gaps report (`list_products`, `GET /master-data-gaps` / `find_master_data_gaps`, [ADR 0020](../adr/0020-product-master-mcp-client-and-master-data-gaps.md)); the other three wired, unconsumed. Switched on in the kind cluster (warehouse-infra sets `PRODUCT_MASTER_MCP_ENDPOINT`) |
| `inbound-receiving` | `get_asn`, `list_asns`, `get_appointment`, `list_appointments`, `get_receipt`, `list_receipts`, `list_docks` (its whole, read-only surface) | inbound outlook (`list_asns`, `list_appointments`, `list_receipts`; `GET /inbound-outlook` / `get_inbound_outlook`, [ADR 0021](../adr/0021-inbound-receiving-mcp-client-and-inbound-outlook.md)); the other four wired, unconsumed. Off until `INBOUND_RECEIVING_MCP_ENDPOINT` is set |
| `network-inventory-planning` | `get_transfer`, `list_transfers`, `find_stuck_transfers`, `simulate_transfer_options` (read tools only) | transfer watch (`/transfer-watch/*`, `triage_stuck_transfers`, `get_transfer_status`, `explain_network_imbalance`, [ADR 0019](../adr/0019-network-inventory-planning-transfer-watch.md)); `list_transfers` wired, unconsumed |

Beyond MCP, the agent also reads Prometheus and Loki for its
[runtime-signals report](../api-surface.md), and fans out over plain REST
for the `console-bff` routes (see the [context map](../ecosystem/context-map.md)).

"Analyze" is the pure `internal/domain/policy` correlation layer described
below. "Act" is deliberately **not built yet** — see
[Governance note](../mcp/governance-note.md#v1-scope-read-only-recommendations-only).

## The three exceptions

| Exception | What it correlates | Recommended action |
|---|---|---|
| **E1 — FlowBalanceException** | wes's rebalance recommendation + workforce-management's staffing gap + fulfillment-execution's stuck-task diagnostic, for one process path | `assign_labor`, `release_next_work`, or `hold` |
| **E2 — StrandedReservation** | fulfillment-execution's expired/expiring task leases + inventory-storage's usable-stock shortfall for the affected SKU | `revoke_reservation` (with a mandatory blast-radius readout) or `hold` |
| **E3 — DailyBrief** | backlog telemetry, staffing gap, queue depth, and stuck-task counts across every monitored path, grouped by facility-layout site | flags a path an **open exception** only when **two or more independent signals** correlate — never a single metric alone |

Every decision this agent returns carries its full **evidence trail**
(which upstream tool reading drove the call) and degrades to a
conservative `hold` — never a guess — whenever a signal it needs is
unavailable. See [Domain vision](../business-context/domain-vision.md) for
why that degrade-to-hold discipline is the whole point of the design.

## Where to go next

- [Getting started](./getting-started.md) — run it locally.
- [Architecture](./architecture.md) — the single binary, the hexagonal
  layout and the dependency rules.
- [Configuration](../operations/configuration.md),
  [Runbook](../operations/runbook.md),
  [Observability](../operations/observability.md) and
  [Troubleshooting](../operations/troubleshooting.md) — operating it.
- [Testing](../development/testing.md) — the test pyramid and CI jobs.
- [Use cases](../ddd/use-cases.md) — what each query does and the rules
  it applies.
- [HTTP routes](../api/http-routes.md) and [MCP tools](../mcp/tools.md) —
  the full REST and MCP contract.
- [Integration](../ecosystem/integration.md) — every upstream and
  downstream edge and its failure behaviour.
- [Domain vision](../business-context/domain-vision.md) — why this agent
  exists the way it does, and the guardrails that keep it that way.
- [Ubiquitous language](../business-context/ubiquitous-language.md) — the
  exact vocabulary this service uses, including the terms it borrows from
  its upstream contexts.
- [DDD artifacts (ddd-crew)](../ddd/ddd-artifacts.md) — core domain chart,
  bounded context canvas, EventStorming, class / ER / sequence diagrams.
- [API surface](../api-surface.md) — the REST and MCP tools this agent
  exposes.
- [Context map](../ecosystem/context-map.md) — how this agent sits among
  the bounded contexts.
- [Governance note](../mcp/governance-note.md) — the read-only v1 posture
  and what a future write-capable slice would require.
- [Architecture Decision Records](../adr/index.md) — the decisions, and why.
