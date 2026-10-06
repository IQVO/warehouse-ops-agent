---
id: ubiquitous-language
title: Ubiquitous language
sidebar_label: Ubiquitous language
description: The exact vocabulary of warehouse-ops-agent, each term mapped to its code identifier, and the terms it borrows from its upstream contexts without redefining them.
---

# Ubiquitous language

`warehouse-ops-agent` speaks two kinds of vocabulary: terms it coins
itself for its own correlation policies, and terms it borrows verbatim
from its upstream contexts because it never redefines a fact another
context already owns. This glossary is part of the
[DDD artifact pack](../ddd/ddd-artifacts.md); every term maps to a code
identifier, and a ⚠ marks a term whose code name differs from the spoken
one.

## Terms this agent coins

| Term | Code identifier | Meaning |
|---|---|---|
| **FlowBalanceException (E1)** ⚠ | `policy.Decision`, built by `policy.Decide`; use case `usecases.FlowBalanceAdvisory` | The correlation of a wes-work-planning rebalance recommendation, a workforce-management staffing gap, and a fulfillment-execution stuck-task diagnostic for one process path into a single ranked recommendation. The spoken name survives in the MCP tool `get_flow_balance_exception`; the Go type is `Decision`. |
| **StrandedReservation (E2)** ⚠ | `policy.StrandedReservationException`, built by `policy.Evaluate`; use case `usecases.DetectStrandedReservation` | The correlation of fulfillment-execution's expired/expiring task leases with inventory-storage's usable-stock shortfall for the affected SKU into a `revoke_reservation`-or-`hold` recommendation. Exposed as the MCP tool `detect_stranded_reservation`. |
| **DailyBrief (E3)** | `policy.DailyBrief`, `policy.SiteBrief`, `policy.PathBrief`; `policy.SynthesizePathBrief`; use case `usecases.DailyBrief` | The synthesized, cross-path, cross-site operational summary: every monitored path's raw facts plus the open exceptions derived from them. |
| **OpenException** | `policy.OpenException` (`Kind`, `Severity`, `Evidence`) | One path's flagged, human-gated exception: which correlation rule fired (`Kind`, today only `flow_balance_risk`), how badly (`Severity`), and its full evidence trail. Raised only when two or more independent signals fire (`deriveExceptions`). |
| **Severity** | `policy.Severity`: `info`, `warning`, `critical`; plus `SeverityNormal` (`normal`) via the alias `SignalSeverity` | Coarse ranking. Two correlated signals is `warning`, three is `critical`. Runtime signals add `normal`. |
| **Evidence trail** ⚠ | `policy.FlowBalanceEvidenceEntry` (E1, `Source` / `Detail`), `policy.EvidenceEntry` (E2, `Tool` / `Summary`), `OpenException.Evidence []string` (E3) | The list of facts behind a decision, each naming the upstream tool call that produced it. Three different shapes in code for one spoken concept. |
| **Blast radius** | `policy.BlastRadius`, `policy.BinLine` | The mandatory "what would this write touch" readout (SKU, bin, reservation, quantity freed, full bin-line snapshot) that must accompany a `revoke_reservation` recommendation. Built from `inventory-storage.get_bin_occupancy`. |
| **Partial / MissingSignals** ⚠ | `Decision.Partial`, `Decision.MissingSignals` (E1 only); `PathBrief.Unavailable` (E3) | The typed degrade state when an upstream read failed. Only the E1 `Decision` carries `Partial` / `MissingSignals`; a `PathBrief` lists failed sources in `Unavailable`; a `StrandedReservationException` has neither and instead degrades to `hold` with an evidence line naming the missing source. |
| **PathTarget** | `policy.PathTarget`, `usecases.PathTarget`, `config.PathTarget` (`DAILY_BRIEF_PATH_TARGETS`) | Deployment-time configuration binding each upstream context's own naming for "the same" process path: wes's `PathId`, fulfillment-execution's `ProcessPath` queue name, workforce-management's `(BuildingId, ShiftId, PathId)` key, under a facility-layout `SiteCode`; optionally warehouse-planning's `PlanningPathId` and conversion factors. Never inferred. |
| **Recommended action** ⚠ | `policy.RecommendedAction` (`assign_labor`, `release_next_work`, `hold` — constant `FlowBalanceActionHold`); `policy.StrandedReservationAction` (`revoke_reservation`, `hold` — constant `ActionHold`) | The closed set of levers a decision can rank, split across two enums in code. `hold` is the safe default when the evidence does not clearly support a lever. |
| **UtilizationCorrelation** | `policy.UtilizationCorrelation`, `policy.UtilizationCorrelationKind`; `policy.CorrelateUtilization` | The ADR-0008 overlay on a FlowBalanceException: queue depth crossed with labor-performance's measured utilization, yielding `claim_flow_problem`, `starvation` or `staffing_gap_confirmed`, or nil. |
| **TravelFactorCorrelation** | `policy.TravelFactorCorrelation`, `policy.TravelFactorOutcomeKind`; `policy.CorrelateTravelFactor` | The ADR-0009 classification of a facility-layout travel distance between two caller-supplied locations: `travel_significant` above 60 m, else `travel_negligible`. |
| **Capacity outlook** | `policy.CapacityOutlook`, `policy.CapacityStepFact`; `policy.SummarizeCapacityOutlook`; use case `usecases.CapacityOutlook` | ADR 0013: warehouse-planning's path capacity (ORDER per hour), bottleneck step and binding constraint over the next `CAPACITY_OUTLOOK_HORIZON`, shown next to a path's facts. Informational and fail-open (`OmittedReason`). |
| **ServiceSignal / RuntimeSignalsReport** | `policy.ServiceSignal`, `policy.RuntimeSignalsReport`; `policy.ClassifyErrorRate`, `policy.ClassifyLatencyP99` | One service's runtime health (5xx error rate, p99 latency, recent error-log count) and the fleet-wide report of them, each classified `normal` / `warning` / `critical`. |
| **LLM mode / decision source** | `policy.LLMMode` (`off`, `shadow`, `on`), `policy.DecisionSource` (`deterministic`, `llm`, `fallback`) | ADR 0004: whether a model plan may replace the deterministic decision, and which path actually produced the returned one. |
| **Plan** ⚠ | `ports.Plan` (from the reasoner), `policy.PlanProposal` (validated view), `policy.Arbitration` | The model's proposal in the closed action vocabulary, submitted through the `submit_plan` tool and gated by `policy.ValidatePlan` / `policy.Arbitrate`. |

## Terms this agent borrows, unredefined

These originate in an upstream context and are never given a second
meaning here — this agent's policy layer treats them as opaque facts
read across an MCP tool-call boundary, validated against the same closed
enum the upstream context defines, never reinterpreted:

| Word | Owning context | Code identifier here | What it means there |
|---|---|---|---|
| **RebalanceAction** (`NoActionNeeded`, `ThrottleUpstream`, `ReassignLabor`) | `wes-work-planning` | `policy.RebalanceAction`, `policy.ParseRebalanceAction` | The action `get_rebalance_recommendation` returns for a path's current flow state. |
| **TaskType** (`PICK`, `PACK`, `SLAM`) | `fulfillment-execution` | `policy.TaskType`, `TaskType.Valid` | The kind of task a lease belongs to. |
| **SKU**, **usable stock**, **reservation** | `inventory-storage` | `policy.AvailabilitySignal`, `policy.BinLine`, `ports.Availability`, `ports.BinOccupancy` | Product identity and stock-state vocabulary; see that context's own ubiquitous-language page for the full model. |
| **SiteCode**, **Zone**, **location code** | `facility-layout` | `PathTarget.SiteCode`, `policy.TravelDistanceReading` | Physical-structure vocabulary; read to group the daily brief and to measure travel, never to reason about placement legality. |
| **BuildingId**, **ShiftId** | `workforce-management` | `PathTarget.BuildingId`, `PathTarget.ShiftId` | Staffing-plan scoping keys. |
| **Utilization** | `labor-performance` | `policy.UtilizationSignal`, `ports.TaskTypeUtilization` | Busy versus idle time per task type; a null `utilizationPct` means nothing observed, never 0%. |
| **Process-path capacity**, **bottleneck step**, **binding constraint** | `warehouse-planning` | `policy.PathCapacityFact`, `ports.ProcessPathCapacity` | Planned throughput of a multi-step path, normalized to ORDER per hour. |

## Words this agent deliberately does not use about itself

`Aggregate`, `Invariant`, `Domain event`, `Bounded context`. Per
[Subdomain classification](../ddd/subdomain-classification.md), using any
of these words about `warehouse-ops-agent` itself would misrepresent what
this repo is. The DDD artifact pack uses them only to record their
absence (for example the [Aggregate design canvas](../ddd/aggregate-design-canvas.md)
and [Domain events](../ddd/domain-events.md) pages).
