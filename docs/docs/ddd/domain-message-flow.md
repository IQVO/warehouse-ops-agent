---
id: domain-message-flow
title: Domain message flow
sidebar_label: Domain message flow
sidebar_position: 5
description: ddd-crew Domain Message Flow Modelling for warehouse-ops-agent — four key scenarios, every message a real REST route or MCP tool, all queries.
---

# Domain message flow

Following the ddd-crew
[Domain Message Flow Modelling](https://github.com/ddd-crew/domain-message-flow-modelling)
notation: every arrow is prefixed `cmd:` (command), `evt:` (event) or
`qry:` (query) and numbered.

:::note[Only queries]
Every message in this service's flows is a `qry:`. It issues no command to
any context (zero write tools, CI-enforced) and publishes or consumes no
event (no Kafka I/O). Where a human would act on a recommendation, that
step happens outside this service and is shown as a note, not an arrow.
:::

## 1. Morning brief for a shift lead

An agentic host asks for the daily brief; the agent fans out per
configured path target and returns open exceptions ranked critical-first.

```mermaid
sequenceDiagram
    autonumber
    actor Lead as Shift lead
    participant Host as Agentic host
    participant WOA as warehouse-ops-agent
    participant FL as facility-layout
    participant WP as wes-work-planning
    participant WM as workforce-management
    participant FE as fulfillment-execution
    participant WPL as warehouse-planning
    Lead->>Host: qry: what needs attention today
    Host->>WOA: qry: MCP get_daily_brief
    WOA->>FL: qry: MCP list_sites
    loop each configured path target
        WOA->>WP: qry: MCP get_backlog_telemetry
        WOA->>WM: qry: MCP get_staffing_gap
        WOA->>FE: qry: MCP get_queue_status
        WOA->>FE: qry: MCP diagnose_stuck_tasks
        opt WAREHOUSE_PLANNING_MCP_ENDPOINT set
            WOA->>WPL: qry: MCP get_process_path_capacity
        end
    end
    WOA-->>Host: qry: DailyBrief with ranked openExceptions
    Host-->>Lead: qry: brief and evidence trail
```

Source: `internal/application/usecases/dailybrief.go`,
`internal/application/usecases/capacity_outlook.go`,
`internal/adapters/inbound/mcp/tools.go`.
Omits: `list_open_exceptions` (same fan-out, filtered by severity) and the
REST twin `GET /daily-brief`.

## 2. Flow-balance exception with the LLM reasoner in shadow mode

```mermaid
sequenceDiagram
    autonumber
    participant Host as Agentic host
    participant WOA as warehouse-ops-agent
    participant WP as wes-work-planning
    participant WM as workforce-management
    participant FE as fulfillment-execution
    participant LP as labor-performance
    participant LLM as Anthropic Messages API
    Host->>WOA: qry: MCP get_flow_balance_exception
    WOA->>WP: qry: MCP get_rebalance_recommendation
    WOA->>WM: qry: MCP get_staffing_gap
    WOA->>FE: qry: MCP diagnose_stuck_tasks
    opt wes answered and pathId is bound to a task type
        WOA->>LP: qry: MCP get_task_type_utilization
    end
    opt LLM_MODE shadow or on
        WOA->>LLM: qry: POST /v1/messages with allow-listed read tools
        LLM-->>WOA: qry: tool_use for an allow-listed read tool
        WOA->>WP: qry: MCP read tool on the allow-list
        LLM-->>WOA: qry: submit_plan
    end
    WOA-->>Host: qry: FlowBalanceException with source and evidence
```

Source: `internal/application/usecases/flow_balance_advisory.go`,
`internal/domain/policy/arbitrate.go`,
`internal/adapters/outbound/llm/anthropic/reasoner.go`,
`internal/config/config.go` (default `LLM_TOOL_ALLOWLIST`).
Omits: retries and the circuit breaker on the Anthropic call (ADR 0011),
and that an allow-listed tool may target wes, workforce-management or
fulfillment-execution (only one is drawn).

## 3. Stranded reservation triage

```mermaid
sequenceDiagram
    autonumber
    actor Planner as Inventory planner
    participant Host as Agentic host
    participant WOA as warehouse-ops-agent
    participant FE as fulfillment-execution
    participant IS as inventory-storage
    Planner->>Host: qry: is SKU stock stranded behind expired PICK leases
    Host->>WOA: qry: MCP detect_stranded_reservation
    WOA->>FE: qry: MCP diagnose_stuck_tasks
    WOA->>IS: qry: MCP check_availability
    opt reservationId and binId supplied
        WOA->>IS: qry: MCP get_bin_occupancy
    end
    WOA-->>Host: qry: revoke_reservation with blast radius, or hold
    Note over Planner,IS: Any actual revoke is a human calling inventory-storage directly - this agent has no write tool
```

Source: `internal/application/usecases/stranded_reservation.go`,
`internal/domain/policy/stranded_reservation.go`,
`internal/adapters/inbound/mcp/tools.go`.
Omits: the degrade branches (each yields `hold`), detailed on the
[sequence diagrams](./sequence-diagrams.md) page.

## 4. Order lifecycle in the console

```mermaid
sequenceDiagram
    autonumber
    actor Op as Operator
    participant WC as warehouse-console
    participant WOA as warehouse-ops-agent console-bff
    participant OM as order-management
    participant IS as inventory-storage
    participant WP as wes-work-planning
    participant FE as fulfillment-execution
    Op->>WC: qry: open order lifecycle
    WC->>WOA: qry: GET /console/orders/ORDER_ID/lifecycle
    WOA->>OM: qry: GET /orders/ORDER_ID
    WOA->>IS: qry: GET /reservations?demandRef=ORDER_ID
    WOA->>WP: qry: GET /work-units?reference=ORDER_ID
    loop each returned work unit
        WOA->>FE: qry: GET /tasks?orderRef=WORK_UNIT_ID
    end
    WOA-->>WC: qry: orderLifecycle DTO
    WC-->>Op: qry: timeline
```

Source: `internal/application/usecases/order_lifecycle.go`,
`internal/adapters/outbound/restclient/clients.go`,
`internal/adapters/inbound/http/router.go`.
Omits: the WMS / WES dashboard fan-out (concurrent, seven `*-reports`
readers) and the runtime-signals flow, both drawn on the
[sequence diagrams](./sequence-diagrams.md) page.
