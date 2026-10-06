---
id: eventstorming
title: EventStorming
sidebar_label: EventStorming
sidebar_position: 6
description: Design-level EventStorming boards for warehouse-ops-agent's main processes, in ddd-crew cheat-sheet colours — read models, policies and external systems, with no domain events of its own.
---

# EventStorming

Design-level boards in the notation of the ddd-crew
[EventStorming glossary / cheat sheet](https://github.com/ddd-crew/eventstorming-glossary-cheat-sheet).

:::note[No orange stickies]
This service emits and consumes **no domain events** and handles **no
commands** — every inbound message is a query, and there is no Kafka I/O
(see [Domain events](./domain-events.md)). The boards therefore show the
read side: actors, the external systems whose facts are read, the policies
(correlation rules) that combine them, the read models handed back, and
hotspots. The only blue command sticky is the write a *human* may perform
afterwards on another context's own tool; it is drawn outside this service
on purpose.
:::

## Legend

```mermaid
flowchart LR
    A["Actor"]:::actor
    C["Command"]:::command
    AG["Aggregate"]:::aggregate
    E["Domain Event"]:::event
    P["Policy"]:::policy
    R["Read Model"]:::readmodel
    X["External System"]:::external
    H["Hotspot"]:::hotspot
    classDef actor fill:#fff59d,stroke:#b59f00,color:#000,font-size:11px
    classDef command fill:#4aa3df,stroke:#1f618d,color:#000
    classDef aggregate fill:#f7d84a,stroke:#9a7d0a,color:#000
    classDef event fill:#f6a04d,stroke:#a04000,color:#000
    classDef policy fill:#c39bd3,stroke:#6c3483,color:#000
    classDef readmodel fill:#7dcea0,stroke:#1e8449,color:#000
    classDef external fill:#f1948a,stroke:#922b21,color:#000
    classDef hotspot fill:#e74c3c,stroke:#78281f,color:#fff
```

Aggregate and Domain Event appear in the legend only: no sticky of either
kind exists on these boards (see [Aggregate design canvas](./aggregate-design-canvas.md)).

## Board 1: daily brief (E3)

```mermaid
flowchart LR
    Lead["Shift lead or agentic host"]:::actor
    FL["facility-layout list_sites"]:::external
    WP["wes-work-planning get_backlog_telemetry"]:::external
    WM["workforce-management get_staffing_gap"]:::external
    FEq["fulfillment-execution get_queue_status"]:::external
    FEs["fulfillment-execution diagnose_stuck_tasks"]:::external
    WPL["warehouse-planning get_process_path_capacity"]:::external
    P1["Policy: two or more independent signals make an open exception"]:::policy
    P2["Policy: capacity outlook is informational and fail-open"]:::policy
    RM["DailyBrief read model: sites, paths, ranked openExceptions"]:::readmodel
    H1["Hotspot: E3 LLM reasoner coverage not implemented - ADR 0004"]:::hotspot
    Lead --> RM
    FL --> RM
    WP --> P1
    WM --> P1
    FEs --> P1
    FEq --> RM
    P1 --> RM
    WPL --> P2
    P2 --> RM
    H1 -.- RM
    classDef actor fill:#fff59d,stroke:#b59f00,color:#000,font-size:11px
    classDef policy fill:#c39bd3,stroke:#6c3483,color:#000
    classDef readmodel fill:#7dcea0,stroke:#1e8449,color:#000
    classDef external fill:#f1948a,stroke:#922b21,color:#000
    classDef hotspot fill:#e74c3c,stroke:#78281f,color:#fff
```

Source: `internal/application/usecases/dailybrief.go`,
`internal/domain/policy/dailybrief.go`,
`internal/domain/policy/capacity_outlook.go`, ADR 0004 Status line.
Omits: the per-path loop and the `Unavailable` bookkeeping.

## Board 2: flow-balance exception (E1) with overlays

```mermaid
flowchart LR
    Host["Agentic host or HTTP client"]:::actor
    WP["wes-work-planning get_rebalance_recommendation"]:::external
    WM["workforce-management get_staffing_gap"]:::external
    FE["fulfillment-execution diagnose_stuck_tasks"]:::external
    LP["labor-performance get_task_type_utilization"]:::external
    LLM["Anthropic Messages API"]:::external
    P1["Policy: Decide - labor gap or healthy backlog, degrade to hold"]:::policy
    P2["Policy: CorrelateUtilization - claim_flow_problem, starvation, staffing_gap_confirmed"]:::policy
    P3["Policy: Arbitrate and ValidatePlan - off, shadow, on"]:::policy
    RM["FlowBalanceException read model: action, heads, evidence, source"]:::readmodel
    CMD["assign_labor or release_next_work on the owning context"]:::command
    H1["Hotspot: stuck-task reading is system-wide, not path-scoped"]:::hotspot
    Host --> RM
    WP --> P1
    WM --> P1
    FE --> P1
    LP --> P2
    P1 --> P3
    LLM --> P3
    P2 --> RM
    P3 --> RM
    RM -. "human decides, outside this service" .-> CMD
    H1 -.- FE
    classDef actor fill:#fff59d,stroke:#b59f00,color:#000,font-size:11px
    classDef command fill:#4aa3df,stroke:#1f618d,color:#000
    classDef policy fill:#c39bd3,stroke:#6c3483,color:#000
    classDef readmodel fill:#7dcea0,stroke:#1e8449,color:#000
    classDef external fill:#f1948a,stroke:#922b21,color:#000
    classDef hotspot fill:#e74c3c,stroke:#78281f,color:#fff
```

Source: `internal/application/usecases/flow_balance_advisory.go`,
`internal/domain/policy/flow_balance.go`,
`internal/domain/policy/utilization_correlation.go`,
`internal/domain/policy/arbitrate.go`.
Omits: the explain-travel-factor advisory (ADR 0009), which is a separate
request, not part of this decision.

## Board 3: stranded reservation (E2)

```mermaid
flowchart LR
    Planner["Inventory planner or agentic host"]:::actor
    FE["fulfillment-execution diagnose_stuck_tasks"]:::external
    IS1["inventory-storage check_availability"]:::external
    IS2["inventory-storage get_bin_occupancy"]:::external
    P1["Policy: Evaluate - expired leases AND usable at or below threshold"]:::policy
    P2["Policy: no revoke without reservationId and blast radius"]:::policy
    RM["StrandedReservationException read model: detected, action, evidence, blastRadius"]:::readmodel
    CMD["revoke_reservation on inventory-storage"]:::command
    H1["Hotspot: E2 has an MCP tool but no REST route"]:::hotspot
    H2["Hotspot: act slice needs an authorization gate and human confirmation"]:::hotspot
    Planner --> RM
    FE --> P1
    IS1 --> P1
    P1 --> P2
    IS2 --> P2
    P2 --> RM
    RM -. "human decides, outside this service" .-> CMD
    H1 -.- RM
    H2 -.- CMD
    classDef actor fill:#fff59d,stroke:#b59f00,color:#000,font-size:11px
    classDef command fill:#4aa3df,stroke:#1f618d,color:#000
    classDef policy fill:#c39bd3,stroke:#6c3483,color:#000
    classDef readmodel fill:#7dcea0,stroke:#1e8449,color:#000
    classDef external fill:#f1948a,stroke:#922b21,color:#000
    classDef hotspot fill:#e74c3c,stroke:#78281f,color:#fff
```

Source: `internal/application/usecases/stranded_reservation.go`,
`internal/domain/policy/stranded_reservation.go`,
`docs/docs/mcp/governance-note.md`, `docs/docs/api-surface.md`.
Omits: the per-branch evidence lines.

## Sticky inventory

| Sticky | Kind | Code evidence |
|---|---|---|
| Shift lead / agentic host / HTTP client / planner | Actor | callers of `internal/adapters/inbound/{http,mcp}` |
| `list_sites`, `get_backlog_telemetry`, `get_rebalance_recommendation`, `get_staffing_gap`, `get_queue_status`, `diagnose_stuck_tasks`, `get_task_type_utilization`, `check_availability`, `get_bin_occupancy`, `get_process_path_capacity` | External System | `internal/adapters/outbound/mcpclient/*.go` `callTool` literals |
| Anthropic Messages API | External System | `internal/adapters/outbound/llm/anthropic/reasoner.go` |
| Two-or-more-signals rule | Policy | `policy.deriveExceptions` |
| Capacity outlook fail-open | Policy | `policy.SummarizeCapacityOutlook`, `usecases.CapacityOutlook.Execute` |
| Decide | Policy | `policy.Decide`, `decideLaborGap`, `decideHealthyBacklog` |
| CorrelateUtilization | Policy | `policy.CorrelateUtilization` |
| Arbitrate / ValidatePlan | Policy | `policy.Arbitrate`, `policy.ValidatePlan` |
| Evaluate / blast-radius rule | Policy | `policy.Evaluate`, `holdException`, `revokeException` |
| DailyBrief, FlowBalanceException, StrandedReservationException | Read Model | `policy.DailyBrief`, `policy.Decision`, `policy.StrandedReservationException` |
| `assign_labor`, `release_next_work`, `revoke_reservation` | Command (on another context, by a human) | named in `policy.RecommendedAction` / `policy.StrandedReservationAction`; no client method exists (`zerowrite` tests) |
| E3 reasoner not implemented | Hotspot | ADR 0004 Status |
| Stuck-task reading not path-scoped | Hotspot | `policy.StuckTasksSignal` doc comment |
| E2 has no REST route | Hotspot | `inbound/http/router.go` has no stranded-reservation route |
| Act slice guardrails | Hotspot | Governance note, "The future act slice" |
