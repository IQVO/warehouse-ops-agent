---
id: sequence-diagrams
title: Sequence diagrams
sidebar_label: Sequence diagrams
sidebar_position: 9
description: UML sequence diagrams for every warehouse-ops-agent use case exposed over REST or MCP, derived from the use-case function bodies, with error branches.
---

# Sequence diagrams

One diagram per inbound use case (REST and MCP share the same use case
object, so they share a diagram). Participants follow the hexagonal
layers: client, inbound adapter, use case, policy (the pure
`internal/domain/policy` function standing where an aggregate would sit in
a bounded context), outbound adapter, upstream.

:::note[What is never on these diagrams]
No repository, no transaction, no outbox, no idempotency middleware and no
optimistic-concurrency version check: this service has no database and
handles no command. Every use case is a read fan-out followed by a pure
policy call.
:::

## Daily brief — `GET /daily-brief`, `get_daily_brief`, `list_open_exceptions`

```mermaid
sequenceDiagram
    autonumber
    participant C as Client
    participant IN as inbound http or mcp
    participant UC as usecases.DailyBrief
    participant P as policy
    participant MC as mcpclient
    participant UP as upstream MCP servers
    C->>IN: GET /daily-brief or get_daily_brief or list_open_exceptions
    alt list_open_exceptions with unknown severity
        IN-->>C: tool error invalid severity, never defaulted
    else accepted
        IN->>UC: Execute
        UC->>MC: ListSites
        MC->>UP: facility-layout list_sites
        UP-->>MC: sites or error
        loop each PathTarget
            UC->>MC: GetBacklogTelemetry, GetStaffingGap, GetQueueStatus, DiagnoseStuckTasks
            MC->>UP: wes, workforce-management, fulfillment-execution tools
            alt a call fails or client unset
                UP-->>MC: error
                MC-->>UC: error, fact left nil, Unavailable entry added
            else answered
                UP-->>MC: facts
                MC-->>UC: DTOs mapped to policy facts
            end
            UC->>P: SynthesizePathBrief
            P-->>UC: PathBrief with exceptions when 2 or more signals fire
            opt Outlook configured
                UC->>MC: GetProcessPathCapacity
                MC->>UP: warehouse-planning get_process_path_capacity
                UP-->>MC: capacity or error
                UC->>P: SummarizeCapacityOutlook or OmittedCapacityOutlook
            end
        end
        UC->>UC: group by site, rankBySeverity
        UC-->>IN: policy.DailyBrief
        IN-->>C: 200 dailyBriefDTO, or filtered exceptions
    end
```

Source: `internal/application/usecases/dailybrief.go`,
`internal/application/usecases/capacity_outlook.go`,
`internal/adapters/inbound/http/router.go` (`getDailyBrief`),
`internal/adapters/inbound/mcp/tools.go` (`getDailyBrief`,
`listOpenExceptions`, `severityRank`).
Omits: that `list_sites` failure only leaves site names empty, and the
per-path stuck-task filter `countForPath`.

## Flow-balance exception — `GET /flow-balance/{pathId}`, `get_flow_balance_exception`

```mermaid
sequenceDiagram
    autonumber
    participant C as Client
    participant IN as inbound http or mcp
    participant UC as usecases.FlowBalanceAdvisory
    participant P as policy
    participant MC as mcpclient
    participant UP as upstream MCP servers
    participant R as anthropic.Reasoner
    C->>IN: GET /flow-balance/PATH_ID?buildingId and shiftId, or tool call
    alt use case not wired
        IN-->>C: 503, or tool not registered
    else wired
        IN->>UC: Execute buildingId, shiftId, pathId
        UC->>MC: GetRebalanceRecommendation
        MC->>UP: wes-work-planning get_rebalance_recommendation
        UP-->>MC: recommendation or error
        UC->>P: ParseRebalanceAction
        alt unknown action
            P-->>UC: error
            UC-->>IN: error
            IN-->>C: 400, or tool error
        else valid or wes unavailable
            UC->>MC: GetStaffingGap, DiagnoseStuckTasks
            MC->>UP: workforce-management, fulfillment-execution tools
            UP-->>MC: facts or errors, errors become nil signals
            UC->>P: Decide wes, wfm, fe
            P-->>UC: Decision, Partial hold if a needed signal is missing
            opt wes answered and LP wired and pathId bound to a task type
                UC->>MC: GetTaskTypeUtilization
                MC->>UP: labor-performance get_task_type_utilization
                UC->>P: CorrelateUtilization
            end
            alt LLM_MODE off or no Reasoner
                UC->>UC: Source deterministic
            else shadow or on
                UC->>R: Reason brief with facts and allow-listed tools
                R->>MC: ToolInvoker.Invoke allow-listed read tool, 0 to 6 turns
                R-->>UC: Plan or error
                UC->>P: Arbitrate det, plan, err, mode
                P-->>UC: deterministic in shadow, llm or fallback in on
            end
            UC-->>IN: Decision
            IN-->>C: 200 flowBalanceExceptionDTO
        end
    end
```

Source: `internal/application/usecases/flow_balance_advisory.go`,
`internal/domain/policy/{flow_balance,arbitrate,utilization_correlation}.go`,
`internal/adapters/outbound/llm/anthropic/reasoner.go`,
`internal/adapters/outbound/mcpclient/tool_invoker.go`.
Omits: the circuit breaker, timeout and retry around each Anthropic call
(ADR 0011), the `llm.tool_call` audit log, and the arbitration metrics.

## Explain travel factor — `GET /explain-travel-factor`, `explain_travel_factor`

```mermaid
sequenceDiagram
    autonumber
    participant C as Client
    participant IN as inbound http or mcp
    participant UC as usecases.ExplainTravelFactor
    participant P as policy
    participant MC as mcpclient
    participant FL as facility-layout MCP
    C->>IN: pathId, fromLocationCode, toLocationCode
    alt use case not wired
        IN-->>C: 503, or tool not registered
    else wired
        IN->>UC: Execute
        alt either location code empty
            UC-->>IN: ErrInvalidInput both codes required
            IN-->>C: 400, or tool error
        else both supplied
            UC->>MC: EstimateTravelDistance from, to
            MC->>FL: estimate_travel_distance
            alt upstream rejected a validation slug
                FL-->>MC: isError text slug-colon-detail, slug malformed-, invalid-, required or validation-failed
                UC-->>IN: ErrInvalidInput wrapping the upstream error
                IN-->>C: 400, or tool error
            else other upstream rejection or outage, slug-less legacy text included
                FL-->>MC: error, e.g. not-found, internal-error, no slug, unreachable
                UC-->>IN: empty result and error
                IN-->>C: 502, or tool error
            else distance
                FL-->>MC: metres, estimated
                UC->>P: CorrelateTravelFactor reading
                P-->>UC: travel_significant above 60m, else travel_negligible
                UC-->>IN: TravelFactorResult
                IN-->>C: 200 metresM, estimated, kind, rationale
            end
        end
    end
```

Source: `internal/application/usecases/explain_travel_factor.go`,
`internal/domain/policy/travel_factor.go`,
`internal/adapters/outbound/mcpclient/tool_error.go`,
`internal/adapters/inbound/http/router.go` (`getExplainTravelFactor`).
Decided 2026-10-06 ([ADR 0018](../adr/0018-mcp-tool-error-slug-classification.md)):
the upstream's tool-error text is `<slug>: <detail>` (fleet convention); only
the explicit validation slugs (`malformed-*`, `invalid-*`, `*-required`,
`validation-failed`, `missing-location-code`) are classified as invalid input
(400). Not-found, `internal-error`, any other slug and slug-less text from an
older facility-layout stay 502.
Omits: the use case's nil-`Facility` branch, which returns an empty result
with no error.

## Stranded reservation — `detect_stranded_reservation` (MCP only)

```mermaid
sequenceDiagram
    autonumber
    participant C as Agentic host
    participant IN as inbound mcp
    participant UC as usecases.DetectStrandedReservation
    participant P as policy
    participant MC as mcpclient
    participant FE as fulfillment-execution MCP
    participant IS as inventory-storage MCP
    C->>IN: detect_stranded_reservation taskType, sku, minUsableThreshold, reservationId, binId
    IN->>UC: Execute StrandedReservationRequest
    UC->>MC: DiagnoseStuckTasks withinSeconds
    MC->>FE: diagnose_stuck_tasks
    FE-->>MC: tasks or error, error sets StuckTasksAvailable false
    opt sku supplied
        UC->>MC: CheckAvailability sku
        MC->>IS: check_availability
        IS-->>MC: usable or error, error leaves Availability nil
    end
    opt reservationId and binId supplied
        UC->>MC: GetBinOccupancy binId
        MC->>IS: get_bin_occupancy
        IS-->>MC: bin lines or error
        UC->>UC: toBlastRadius sums Reserved for the sku
    end
    UC->>P: Evaluate Inputs
    alt unknown taskType
        P-->>UC: error
        IN-->>C: tool error
    else stuck tasks unavailable, no expired leases, no availability, or usable above threshold
        P-->>UC: hold, Detected false
    else correlated but no reservationId or no bin occupancy
        P-->>UC: hold, Detected true
    else fully correlated
        P-->>UC: revoke_reservation with BlastRadius
    end
    UC-->>IN: StrandedReservationException
    IN-->>C: detected, action, rationale, evidence, blastRadius
```

Source: `internal/application/usecases/stranded_reservation.go`,
`internal/domain/policy/stranded_reservation.go`,
`internal/adapters/inbound/mcp/tools.go` (`detectStrandedReservation`).
Omits: the evidence line appended on each branch. No REST route exists for
this use case.

## Order lifecycle — `GET /console/orders/{id}/lifecycle`

```mermaid
sequenceDiagram
    autonumber
    participant C as warehouse-console
    participant IN as inbound http
    participant UC as usecases.OrderLifecycle
    participant RC as restclient
    participant OM as order-management REST
    participant IS as inventory-storage REST
    participant WP as wes-work-planning REST
    participant FE as fulfillment-execution REST
    C->>IN: GET /console/orders/ORDER_ID/lifecycle
    alt use case not wired
        IN-->>C: 503
    else wired
        IN->>UC: Execute orderId
        UC->>RC: GetOrder
        RC->>OM: GET /orders/ORDER_ID
        alt 404
            OM-->>RC: not found
            UC-->>IN: ports.ErrNotFound
            IN-->>C: 404 order not found
        else any other error
            OM-->>RC: error
            UC->>UC: order stage nil, warn and continue
        else found
            OM-->>RC: order
        end
        UC->>RC: GetReservationsByDemandRef orderId
        RC->>IS: GET /reservations?demandRef=ORDER_ID
        UC->>RC: GetWorkUnitsByReference orderId
        RC->>WP: GET /work-units?reference=ORDER_ID
        loop each work unit, sequential
            UC->>RC: GetTasksByOrderRef workUnitId
            RC->>FE: GET /tasks?orderRef=WORK_UNIT_ID
        end
        UC-->>IN: OrderLifecycleResult, failed stages left empty
        IN-->>C: 200 orderLifecycleDTO
    end
```

Source: `internal/application/usecases/order_lifecycle.go`,
`internal/adapters/outbound/restclient/clients.go`,
`internal/adapters/inbound/http/router.go` (`getOrderLifecycle`).
Omits: the DTO mapping (`packageSealed` / `labelApplied` are derived from a
completed `SLAM` task).

Decided 2026-10-06: kept — an unreachable order-management leaves the
order-management stage `null` in a 200 (ADR 0002 degrade-to-null; only a 404
fails the request). The agent is an advisory read-side aggregator; one
dependency outage must not fail the whole view.

## WMS / WES dashboards — `GET /console/reports/wms`, `GET /console/reports/wes`

```mermaid
sequenceDiagram
    autonumber
    participant C as warehouse-console
    participant IN as inbound http
    participant UC as usecases.ConsoleReports
    participant RC as restclient reports
    participant RR as context reports readers
    C->>IN: GET /console/reports/wms or wes, optional from and to
    alt not wired
        IN-->>C: 503
    else malformed timestamp, or to not after from
        IN-->>C: 400
    else valid
        IN->>UC: ResolveWindow, default trailing 24h
        IN->>UC: ExecuteWMS or ExecuteWES
        par each section concurrently
            UC->>RC: report call for the window
            RC->>RR: GET /reports/... from, to
            alt unwired or error
                RR-->>RC: error
                UC->>UC: section available false, empty series
            else ok
                RR-->>RC: rows
                UC->>RC: freshness call
                RC->>RR: GET /reports/.../freshness
                RR-->>RC: lag or error, error only drops the annotation
            end
        end
        UC-->>IN: DashboardResult in declaration order
        IN-->>C: 200 dashboardDTO
    end
```

Source: `internal/application/usecases/console_reports.go`,
`internal/application/usecases/console_reports_{wms,wes}.go`,
`internal/adapters/outbound/restclient/reports_clients.go`,
`internal/adapters/inbound/http/router.go` (`serveDashboard`).
Omits: the per-section series aggregation. WMS has 3 sections
(order-management, inventory-storage, facility-layout); WES has 4
(wes-work-planning, fulfillment-execution, workforce-management,
labor-performance).

## Runtime signals — `GET /runtime-signals`

```mermaid
sequenceDiagram
    autonumber
    participant C as Client
    participant IN as inbound http
    participant UC as usecases.RuntimeSignals
    participant P as policy
    participant LK as Loki
    participant PR as Prometheus
    C->>IN: GET /runtime-signals
    alt not wired
        IN-->>C: 503
    else wired
        IN->>UC: Execute
        alt LOKI_URL unset or query fails
            UC->>UC: loki added to unavailableSources
        else
            UC->>LK: GET /loki/api/v1/query_range, error lines in namespace
            LK-->>UC: entries tallied per app or container label
        end
        loop each monitored service
            UC->>PR: GET /api/v1/query istio_requests_total, total and 5xx
            UC->>PR: GET /api/v1/query p99 of istio_request_duration_milliseconds_bucket
            PR-->>UC: samples or error, error adds prometheus to unavailableSources
            UC->>P: ClassifyErrorRate, ClassifyLatencyP99
        end
        UC-->>IN: RuntimeSignalsReport
        IN-->>C: 200 services with severity, unavailableSources
    end
```

Source: `internal/application/usecases/runtime_signals.go`,
`internal/domain/policy/runtime_signals.go`,
`internal/adapters/outbound/telemetry/prometheus_reader.go`,
`internal/adapters/outbound/logs/loki_reader.go`.
Omits: the stub reader used when `PROMETHEUS_URL` is unset (all metrics
read as zero, `normal`), and `ServiceSignal.Severity` lifting any service
with recent error logs to at least `warning`.
