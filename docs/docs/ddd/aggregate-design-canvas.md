---
id: aggregate-design-canvas
title: Aggregate design canvas
sidebar_label: Aggregate design canvas
sidebar_position: 4
description: ddd-crew Aggregate Design Canvas v1.1 applied honestly to warehouse-ops-agent — it has no aggregate root; the decision objects and read models it builds per request are listed instead.
---

# Aggregate design canvas

Following the ddd-crew
[Aggregate Design Canvas v1.1](https://github.com/ddd-crew/aggregate-design-canvas).

## No aggregate root

`warehouse-ops-agent` has **no aggregate root**, so this page carries no
per-aggregate H2 sections. That is a design decision, not a gap:

- `internal/domain/policy/doc.go` states it outright: the package "is
  deliberately NOT a domain in the DDD sense: warehouse-ops-agent owns no
  aggregate, enforces no invariant over persisted state, and persists
  nothing of its own."
- There is no `internal/domain/<aggregate>` package, no repository port in
  `internal/ports` (every port is an outbound *read* client, a telemetry /
  log reader, the `Reasoner`, or `ArbitrationMetrics`), no `migrations/`
  directory and no database driver in `go.mod`.
- No type in the policy package has an identity that survives a request,
  a status enum with transition methods, or a version field. Every value is
  built from upstream reads, returned, and forgotten.
- [ADR 0001](../adr/0001-warehouse-ops-agent-placement.md): if a slice ever
  needs an aggregate, an invariant or persisted state, that slice belongs
  in a bounded-context repo, not here.

The canvas sections map to this repo as follows:

| Canvas section | What applies here |
|---|---|
| 1. Name | none — no aggregate |
| 2. Description | none |
| 3. State transitions | none — no status enum with transition methods exists in `internal/domain/policy` |
| 4. Enforced invariants | none over persisted state. The rejections that do exist are **input validation at an untrusted boundary** (see below) |
| 5. Corrective policies | the degrade-to-`hold` rules below play this role for *recommendations*, not for state |
| 6. Handled commands | none — every inbound message is a query (`GET` routes, read-only MCP tools) |
| 7. Created events | none — no Kafka I/O, no CloudEvents type (see [Domain events](./domain-events.md)) |
| 8. Throughput | n/a for an aggregate; request-rate estimates for the use cases are below |
| 9. Size | n/a — nothing is stored |

## Boundary validation (not invariants)

These reject untrusted input; none guards a stored state.

| Rule | Enforced by | Rejects |
|---|---|---|
| wes `RebalanceAction` is one of `NoActionNeeded`, `ThrottleUpstream`, `ReassignLabor` | `policy.ParseRebalanceAction` | an unknown action from `get_rebalance_recommendation` (the use case returns an error, HTTP 400) |
| `TaskType` is one of `PICK`, `PACK`, `SLAM` | `policy.TaskType.Valid`, checked by `policy.Evaluate` | an unknown `taskType` on `detect_stranded_reservation` |
| `severity` filter is `info`, `warning` or `critical` | `severityRank` + `invalidSeverityErr` (`inbound/mcp`) | an unknown `list_open_exceptions` filter |
| `LLM_MODE` is `off`, `shadow` or `on` | `policy.ParseLLMMode` | an unknown mode — a startup error in `cmd/agent/reasoner.go` |
| An LLM plan uses the closed action vocabulary, `0 ≤ proposedHeads ≤ 50`, heads only with `assign_labor`, non-empty rationale | `policy.ValidatePlan` (wraps `policy.ErrInvalidPlan`) | an out-of-vocabulary or out-of-bounds plan; `Arbitrate` falls back to the deterministic decision |
| A capacity reading is in `ORDER` units and non-negative | `policy.SummarizeCapacityOutlook` | any other unit or a negative rate, turned into an `OmittedReason` |
| Both location codes are supplied | `usecases.ExplainTravelFactor.Execute` | a missing `fromLocationCode` / `toLocationCode` (HTTP 400) |

## Decision objects and read models (listed separately)

These are built per request and are **not** aggregates. Each row names the
pure function that builds it and the degrade rule that plays the
"corrective policy" role.

| Type (`internal/domain/policy`) | Built by | Degrade / corrective rule |
|---|---|---|
| `Decision` (the E1 FlowBalanceException) | `Decide`, then `Arbitrate` | missing wes signal, or missing wfm / fe on the branch in play, yields `Partial: true`, `MissingSignals`, `hold` |
| `UtilizationCorrelation` (ADR 0008 overlay on `Decision`) | `CorrelateUtilization` | nil when labor-performance is unavailable or `UtilizationPct` is null — never 0% |
| `StrandedReservationException` (E2) | `Evaluate` | any missing signal, no `reservationId`, or no bin occupancy yields `hold`; `revoke_reservation` only with a `BlastRadius` |
| `DailyBrief` / `SiteBrief` / `PathBrief` / `OpenException` (E3) | `SynthesizePathBrief`, `deriveExceptions` | an unavailable upstream leaves that fact nil and adds an `Unavailable` entry; exceptions only from ≥ 2 signals |
| `CapacityOutlook` (ADR 0013) | `SummarizeCapacityOutlook`, `OmittedCapacityOutlook` | fail-open: any failure becomes `OmittedReason`; never feeds `deriveExceptions` |
| `TravelFactorCorrelation` (ADR 0009) | `CorrelateTravelFactor` | nil when no reading |
| `RuntimeSignalsReport` / `ServiceSignal` | `ClassifyErrorRate`, `ClassifyLatencyP99`, `ServiceSignal.Severity` | failing source listed in `UnavailableSources` |
| `Arbitration` (ADR 0004) | `Arbitrate` | `on` mode falls back to the deterministic decision with `Source = fallback` |

Application-layer read models with no policy at all:
`usecases.OrderLifecycleResult` (order lifecycle) and
`usecases.DashboardResult` / `ReportSection` (WMS / WES dashboards).

## Throughput and size (estimates)

*Estimate, not measured.* Request rate is driven by humans and agentic
hosts: a console screen or an LLM host polling the daily brief every few
seconds to minutes, flow-balance and stranded-reservation checks on
demand. Each request fans out to between 1 (travel factor) and roughly
5 × N upstream calls (daily brief, N = configured path targets), plus up to
6 Anthropic turns when `LLM_MODE` is not `off` (`defaultMaxTurns = 6`).

*Estimate.* Size per "instance" is zero at rest: every decision object
lives for one request. The only process-lifetime state is listed on the
[Entity relationship](./entity-relationship.md) page.
