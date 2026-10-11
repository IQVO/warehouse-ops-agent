---
id: use-cases
title: Use cases
sidebar_label: Use cases
description: Every application use case in internal/application/usecases — what triggers it (REST or MCP), its inputs, the facts it reads, the rules and guards it applies, and what it returns. None raises a domain event.
---

# Use cases

Every use case lives in `internal/application/usecases` and is wired in
`cmd/agent/main.go`. All of them are **read-only queries**: none changes
state in this service (it has none) or in any other context, and none
raises or publishes a domain event (see [Domain events](./domain-events.md)).
There is no Kafka consumer and no scheduler, so the only triggers are an
HTTP request or an MCP tool call.

Two rules run through every use case:

- **Degrade, never guess.** A missing upstream signal becomes an
  `unavailable` entry, a `null` stage, an `omitted` section or a
  conservative `hold`. A default is never invented for a missing fact.
- **Reject, never default, untrusted input.** An unknown enum from a
  caller or from an upstream tool (`LLM_MODE`, a wes rebalance action, a
  severity, a gap kind, a transfer state, a task type) is an error.

## Summary

| Use case | Struct and method | REST trigger | MCP trigger | Optional? |
|---|---|---|---|---|
| Daily brief (E3) | `DailyBrief.Execute` | `GET /daily-brief` | `get_daily_brief`, `list_open_exceptions` | always wired |
| Capacity outlook | `CapacityOutlook` (inside the daily brief) | `GET /daily-brief` | `get_daily_brief` | only with `WAREHOUSE_PLANNING_MCP_ENDPOINT` |
| Flow-balance advisory (E1) | `FlowBalanceAdvisory.Execute` | `GET /flow-balance/{pathId}` | `get_flow_balance_exception` | always wired |
| Explain travel factor | `ExplainTravelFactor.Execute` | `GET /explain-travel-factor` | `explain_travel_factor` | always wired |
| Detect stranded reservation (E2) | `DetectStrandedReservation.Execute` | none | `detect_stranded_reservation` | always wired |
| Master-data gaps | `MasterDataGaps` | `GET /master-data-gaps` | `find_master_data_gaps` | only with `PRODUCT_MASTER_MCP_ENDPOINT` |
| Inbound outlook | `InboundOutlook.Execute` | `GET /inbound-outlook` | `get_inbound_outlook` | only with `INBOUND_RECEIVING_MCP_ENDPOINT` |
| Transfer watch | `TransferWatch.StuckTransfers`, `TransferStatus`, `NetworkImbalance` | `GET /transfer-watch/stuck`, `/transfers/{id}`, `/imbalance` | `triage_stuck_transfers`, `get_transfer_status`, `explain_network_imbalance` | only with `NETWORK_INVENTORY_PLANNING_MCP_ENDPOINT` |
| Order lifecycle (console-bff) | `OrderLifecycle.Execute` | `GET /console/orders/{id}/lifecycle` | none | always wired |
| Console report dashboards | `ConsoleReports.ExecuteWMS`, `ExecuteWES` | `GET /console/reports/wms`, `/wes` | none | always wired |
| Runtime signals | `RuntimeSignals.Execute` | `GET /runtime-signals` | none | always wired |

Request and response shapes are on [HTTP routes](../api/http-routes.md)
and [MCP tools](../mcp/tools.md); this page describes behaviour.

## Daily brief (E3)

**Inputs:** none from the caller. The monitored paths come from
`DAILY_BRIEF_PATH_TARGETS` (`siteCode`, `pathId`, `processPath`,
`buildingId`, `shiftId`, optional planning binding).

**Reads:** facility-layout `list_sites` once; per target wes-work-planning
`get_backlog_telemetry`, workforce-management `get_staffing_gap`,
fulfillment-execution `get_queue_status` and `diagnose_stuck_tasks`
(`withinSeconds = 0`, filtered to the target's `processPath`).

**Rules** (`policy` daily-brief functions, `deriveExceptions`):

- Three independent signals per path: backlog over its alarm threshold,
  the path understaffed, more than zero stuck tasks. Queue depth is
  reported but is not a signal.
- An open exception (`flow_balance_risk`) needs **at least two** signals:
  two is `warning`, three is `critical`. One signal alone is operating
  noise.
- `openExceptions` is ranked `critical` first across every path.
- A failed call adds `"<upstream>: <error>"` to the path's `unavailable`
  list and the fact is left out; the brief is still produced.

`list_open_exceptions` returns the same exceptions filtered by a minimum
`severity` (`info`, `warning`, `critical`); any other value is rejected.

### Capacity outlook

When warehouse-planning is configured and a target has a
`planningPathId`, the brief adds `capacityOutlook`: warehouse-planning
`get_process_path_capacity` for `[now, now + CAPACITY_OUTLOOK_HORIZON)`
at the target's `siteCode`, with `unitsPerOrder` and `packagesPerOrder`
when set. The result is either a summary (orders per hour, bottleneck
step, binding constraint, per-step rates, warnings) or an
`omittedReason`; it never fails the brief
([ADR 0013](../adr/0013-warehouse-planning-mcp-client-and-capacity-outlook.md)).

## Flow-balance advisory (E1)

**Inputs:** `pathId`, `buildingId`, `shiftId`.

**Reads:** wes-work-planning `get_rebalance_recommendation`,
workforce-management `get_staffing_gap`, fulfillment-execution
`diagnose_stuck_tasks` (`withinSeconds = 900`), then labor-performance
`get_task_type_utilization` for the task type bound to `pathId`.

**Rules** (`policy.Decide`, `flow_balance.go`):

| wes action | staffing | stuck tasks | Decision |
|---|---|---|---|
| unavailable | any | any | `hold`, partial: no lever without the anchor signal |
| `ReassignLabor` or `ThrottleUpstream` | unavailable | any | `hold`, partial |
| `ReassignLabor` or `ThrottleUpstream` | understaffed | any | `assign_labor`, `proposedHeads` = planned minus active, at least 1 |
| `ReassignLabor` or `ThrottleUpstream` | fully staffed | unavailable | `hold`, partial |
| `ReassignLabor` or `ThrottleUpstream` | fully staffed | some or none | `hold` for human review |
| `NoActionNeeded` | any | unavailable | `hold`, partial |
| `NoActionNeeded` | any | more than zero | `hold` |
| `NoActionNeeded` | unavailable | zero | `hold`, partial |
| `NoActionNeeded` | fully staffed | zero | `release_next_work` |
| `NoActionNeeded` | understaffed | zero | `hold`: the gap is visible but not forced into an action |

Any other wes action is rejected by `ParseRebalanceAction` and the request
fails (`400` on REST, a tool error on MCP). Every decision carries one
evidence entry per signal that arrived.

**Utilization overlay** ([ADR 0008](../adr/0008-labor-utilization-advisory-correlation.md),
`CorrelateUtilization`): additive, never changes the action. With a deep
queue (backlog depth above 50) and low utilization (below 60 % and idle
share above 40 %) it is `claim_flow_problem`; a shallow queue with low
utilization is `starvation`; a deep queue with healthy utilization is
`staffing_gap_confirmed`. Nothing observed in the window is never read as
0 %.

**LLM arbitration** ([ADR 0004](../adr/0004-llm-reasoner-behind-the-policy-layer.md),
`policy.Arbitrate`): the deterministic decision is always computed first.
With `LLM_MODE=shadow` the model's plan is logged and counted and the
deterministic decision is returned (`source: deterministic`). With `on` a
plan that passes `ValidatePlan` (action in the closed set, `proposedHeads`
0–50 and only for `assign_labor`, non-empty rationale) replaces action,
heads and rationale (`source: llm`); evidence and partiality stay as the
deterministic pass saw them. Any error, timeout or invalid plan falls
back (`source: fallback`).

## Explain travel factor

**Inputs:** `pathId` (logged only), `fromLocationCode`, `toLocationCode`.

**Reads:** facility-layout `estimate_travel_distance`.

**Rules:** both codes are required; the use case never infers a location
([ADR 0009](../adr/0009-explain-travel-factor.md)). A distance above 60 m
is `travel_significant`, otherwise `travel_negligible`; a graph-estimated
distance is called out as an estimate. A missing code or a facility-layout
validation slug wraps `ErrInvalidInput`; any other failure is an upstream
error.

## Detect stranded reservation (E2)

**Inputs:** `taskType` (`PICK`, `PACK`, `SLAM`), `withinSeconds`, `sku`,
`minUsableThreshold`, optional `reservationId` and `binId`.

**Reads:** fulfillment-execution `diagnose_stuck_tasks`; inventory-storage
`check_availability` when `sku` is set and `get_bin_occupancy` when both
`reservationId` and `binId` are set.

**Rules** (`policy.Evaluate`), checked in order, each failure ending in
`hold` with its reason:

1. `taskType` must be known, otherwise an error.
2. The expired-lease signal must be available.
3. There must be expired or expiring leases of `taskType`.
4. The availability signal must be available.
5. Usable stock must be at or below `minUsableThreshold`.
6. A `reservationId` must have been supplied.
7. The bin occupancy (blast radius) must have been read.

Only then is the answer `revoke_reservation`, with the blast radius (bin,
reservation, quantity freed, bin lines). The use case never calls
inventory-storage's `revoke_reservation`.

## Master-data gaps

**Inputs:** optional `kind` (`unclassified` or `dimension-discrepancy`),
optional `cursor`.

**Reads:** product-master `list_products`, 500 per page, at most 10 pages.
With `kind=unclassified` only unclassified products are requested.

**Rules** (`policy.FindMasterDataGaps`): a product is `unclassified` when
it has no handling classification, and `dimension-discrepancy` when
product-master flags that declared and measured dimensions disagree. An
unknown `kind` is rejected. A product-master failure fails the whole
report: there is no partial mode. `complete: false` plus `nextCursor`
when the 5,000-product bound is hit
([ADR 0020](../adr/0020-product-master-mcp-client-and-master-data-gaps.md)).

## Inbound outlook

**Inputs:** none; `INBOUND_STALE_RECEIPT_AGE` from configuration.

**Reads:** inbound-receiving `list_asns` (state `Registered`),
`list_appointments` (next 24 hours), `list_receipts` (`Open`, only when
the stale age is set) and `list_receipts` (`Closed`).

**Rules** (`policy` inbound functions): an ASN is overdue when its own
expected arrival has passed; appointments in `Booked` or `CheckedIn`
within 24 hours are listed; an `Open` receipt older than the stale age is
stale; a receipt closed today (agent's local day) with discrepancies is
reported. Each section is built independently: a failed section is
`omitted` with its reason, and the whole call fails only when every
attempted section failed. Each scan reads at most 5,000 records
([ADR 0021](../adr/0021-inbound-receiving-mcp-client-and-inbound-outlook.md)).

## Transfer watch

**Inputs:** `olderThanMinutes` (positive), optional non-terminal `state`,
`limit` 0–200 for the stuck list; `transferId` for the status; none for
the imbalance.

**Reads:** network-inventory-planning `find_stuck_transfers`,
`get_transfer` or `simulate_transfer_options`.

**Rules** (`policy.TriageTransfer`, `ExplainImbalance`): a stuck
transfer's cause comes from its state alone (`ALLOCATING` is
`inventory-reply-missing`; `ALLOCATED`, `PICKED` are
`floor-work-not-progressing`; `IN_TRANSIT`, `ARRIVED` are
`destination-receipt-missing`), and causes are tallied highest first. The
imbalance lists `short` sites before `covered` ones and never proposes a
quantity or route. A validation slug wraps `ErrInvalidInput`, a
`*-not-found` slug `ErrTransferNotFound`
([ADR 0019](../adr/0019-network-inventory-planning-transfer-watch.md)).

## Order lifecycle (console-bff)

**Inputs:** the order id.

**Reads** (REST, in order): order-management `GET /orders/<id>`,
inventory-storage `GET /reservations?demandRef=<id>`, wes-work-planning
`GET /work-units?reference=<id>`, then fulfillment-execution
`GET /tasks?orderRef=<workUnitId>` once per work unit.

**Rules:** each stage is independent; a failed call nulls only its stage.
The only error is order-management's `404`, which becomes a `404`. Tasks
are joined through work-unit ids, not the plain order id
([ADR 0002](../adr/0002-micro-frontend-console-architecture.md)).

## Console report dashboards

**Inputs:** optional `from` and `to` (RFC3339); default window is the
trailing 24 hours.

**Reads:** three `*-reports` readers for WMS, four for WES, each report
plus its `/freshness`, all sections concurrently.

**Rules** (`ConsoleReports`): `to` must be after `from`, checked before
any upstream is called. A failed report marks only its section
`available: false`; a failed freshness call only nulls
`freshnessLagSeconds`. A labor-performance task type with no scorable
tasks gets no bar rather than a zero bar
([ADR 0003](../adr/0003-console-bff-report-dashboards.md)).

## Runtime signals

**Inputs:** none; services and namespace from configuration, window fixed
at 10 minutes.

**Reads:** Prometheus (Istio request totals, 5xx totals, p99 latency per
service) and Loki (error and fatal lines in the namespace, at most 50).

**Rules** (`policy` runtime-signal thresholds): error rate is `warning`
from 1 % and `critical` from 5 %; p99 latency is `warning` from 1000 ms
and `critical` from 3000 ms; a service's severity is the worse of the two,
raised to at least `warning` when it has recent error logs. An
unconfigured or failing Loki, or a failing Prometheus query, is listed in
`unavailableSources` rather than reported as healthy.
