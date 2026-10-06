---
id: class-diagram
title: Class diagram
sidebar_label: Class diagram
sidebar_position: 7
description: UML class diagrams of warehouse-ops-agent's internal/domain/policy package and its hexagonal ports and adapters.
---

# Class diagram

UML class diagrams (Mermaid `classDiagram`) of `internal/domain/policy`,
split by policy so no diagram exceeds about 25 classes, plus the hexagonal
ports-and-adapters view.

:::note[Stereotypes used]
There is no `<<AggregateRoot>>`, `<<Entity>>`, `<<DomainEvent>>` or
`<<Repository>>` in this codebase (see
[Aggregate design canvas](./aggregate-design-canvas.md)). Policy result
types and their parts are `<<ValueObject>>`; string enums are
`<<Enumeration>>`; the stateless rule functions are grouped into a
`<<Policy>>` box named after their file (Go has no class for them). Ports
are `<<Port>>` interfaces.
:::

## E1 flow balance, ADR 0004 arbitration and ADR 0008 overlay

```mermaid
classDiagram
    direction LR
    class FlowBalancePolicy {
        <<Policy>>
        +Decide(pathId, wes, wfm, fe) Decision
        +ParseRebalanceAction(raw) RebalanceAction
        +CorrelateUtilization(queueDepth, util) UtilizationCorrelation
    }
    class ArbitrationPolicy {
        <<Policy>>
        +ParseLLMMode(raw) LLMMode
        +ValidatePlan(p) error
        +Arbitrate(det, plan, planErr, mode) Arbitration
        MaxProposedHeads = 50
        ErrInvalidPlan
    }
    class Decision {
        <<ValueObject>>
        +PathId string
        +RecommendedAction RecommendedAction
        +ProposedHeads int
        +Rationale string
        +Partial bool
        +MissingSignals []string
        +Evidence []FlowBalanceEvidenceEntry
        +Utilization *UtilizationCorrelation
        +Source DecisionSource
    }
    class FlowBalanceEvidenceEntry {
        <<ValueObject>>
        +Source string
        +Detail string
    }
    class RebalanceSignal {
        <<ValueObject>>
        +Source string
        +PathId string
        +Action RebalanceAction
        +BacklogDepth int
        +WIP int
    }
    class StaffingSignal {
        <<ValueObject>>
        +PlannedHeads int
        +ActiveHeads int
        +Understaffed bool
    }
    class StuckTasksSignal {
        <<ValueObject>>
        +Count int
        +Reasons []string
    }
    class UtilizationSignal {
        <<ValueObject>>
        +TaskType string
        +TaskSeconds int64
        +IdleSeconds int64
        +UtilizationPct *float64
    }
    class UtilizationCorrelation {
        <<ValueObject>>
        +Kind UtilizationCorrelationKind
        +Rationale string
    }
    class PlanProposal {
        <<ValueObject>>
        +RecommendedAction RecommendedAction
        +ProposedHeads int
        +Rationale string
    }
    class Arbitration {
        <<ValueObject>>
        +Decision Decision
        +Source DecisionSource
        +Agree *bool
        +Reason string
    }
    class RebalanceAction {
        <<Enumeration>>
        NoActionNeeded
        ThrottleUpstream
        ReassignLabor
    }
    class RecommendedAction {
        <<Enumeration>>
        assign_labor
        release_next_work
        hold
    }
    class UtilizationCorrelationKind {
        <<Enumeration>>
        claim_flow_problem
        starvation
        staffing_gap_confirmed
    }
    class LLMMode {
        <<Enumeration>>
        off
        shadow
        on
    }
    class DecisionSource {
        <<Enumeration>>
        deterministic
        llm
        fallback
    }
    Decision *-- FlowBalanceEvidenceEntry
    Decision *-- UtilizationCorrelation
    Decision --> RecommendedAction
    Decision --> DecisionSource
    RebalanceSignal --> RebalanceAction
    UtilizationCorrelation --> UtilizationCorrelationKind
    Arbitration *-- Decision
    PlanProposal --> RecommendedAction
    FlowBalancePolicy ..> RebalanceSignal : reads
    FlowBalancePolicy ..> StaffingSignal : reads
    FlowBalancePolicy ..> StuckTasksSignal : reads
    FlowBalancePolicy ..> UtilizationSignal : reads
    FlowBalancePolicy ..> Decision : builds
    ArbitrationPolicy ..> PlanProposal : validates
    ArbitrationPolicy ..> LLMMode
    ArbitrationPolicy ..> Arbitration : builds
```

Source: `internal/domain/policy/flow_balance.go`,
`internal/domain/policy/arbitrate.go`,
`internal/domain/policy/utilization_correlation.go`.
Omits: `Source`/`PathId` fields on `StaffingSignal` and `StuckTasksSignal`,
`Associates`/`WindowSeconds`/`OpenGapSeconds` on `UtilizationSignal`, the
threshold constants (`UtilizationQueueDepthHighThreshold = 50`,
`UtilizationLowPctThreshold = 60.0`,
`UtilizationHighIdleShareThreshold = 0.40`), and unexported helpers.

## E3 daily brief, ADR 0013 capacity outlook and runtime signals

```mermaid
classDiagram
    direction LR
    class DailyBriefPolicy {
        <<Policy>>
        +SynthesizePathBrief(target, backlog, staffing, queue, stuck, unavailable) PathBrief
        -deriveExceptions(brief) []OpenException
    }
    class CapacityOutlookPolicy {
        <<Policy>>
        +SummarizeCapacityOutlook(planningPathId, start, end, fact) CapacityOutlook
        +OmittedCapacityOutlook(planningPathId, start, end, reason) CapacityOutlook
    }
    class RuntimeSignalsPolicy {
        <<Policy>>
        +ClassifyErrorRate(rate) SignalSeverity
        +ClassifyLatencyP99(p99ms) SignalSeverity
    }
    class DailyBrief {
        <<ValueObject>>
        +GeneratedAt time.Time
        +Sites []SiteBrief
        +OpenExceptions []OpenException
    }
    class SiteBrief {
        <<ValueObject>>
        +SiteCode string
        +SiteName string
        +Paths []PathBrief
    }
    class PathBrief {
        <<ValueObject>>
        +Target PathTarget
        +Backlog *BacklogFact
        +Staffing *StaffingFact
        +Queue *QueueFact
        +Stuck *StuckTasksFact
        +Unavailable []string
        +Exceptions []OpenException
        +CapacityOutlook *CapacityOutlook
    }
    class PathTarget {
        <<ValueObject>>
        +SiteCode string
        +PathId string
        +ProcessPath string
        +BuildingId string
        +ShiftId string
    }
    class BacklogFact {
        <<ValueObject>>
        +BacklogDepth int
        +WIP int
        +OverAlarmThreshold bool
    }
    class StaffingFact {
        <<ValueObject>>
        +PlannedHeads int
        +ActiveHeads int
        +Understaffed bool
    }
    class QueueFact {
        <<ValueObject>>
        +Depth int
    }
    class StuckTasksFact {
        <<ValueObject>>
        +Count int
    }
    class OpenException {
        <<ValueObject>>
        +Kind ExceptionKind
        +SiteCode string
        +PathId string
        +Severity Severity
        +Summary string
        +Evidence []string
    }
    class CapacityOutlook {
        <<ValueObject>>
        +PlanningPathId string
        +WindowStart time.Time
        +WindowEnd time.Time
        +NormalizedRate float64
        +BottleneckStep string
        +BindingConstraint string
        +Steps []CapacityStepFact
        +Warnings []string
        +OmittedReason string
    }
    class CapacityStepFact {
        <<ValueObject>>
        +Step string
        +NormalizedRate float64
        +BindingConstraint string
    }
    class PathCapacityFact {
        <<ValueObject>>
        +NormalizedRate float64
        +NormalizedUnit string
        +BottleneckStep string
    }
    class RuntimeSignalsReport {
        <<ValueObject>>
        +GeneratedAt string
        +Services []ServiceSignal
        +UnavailableSources []string
    }
    class ServiceSignal {
        <<ValueObject>>
        +ServiceName string
        +ErrorRate float64
        +LatencyP99MS float64
        +RecentErrorLogs int
        +Severity() SignalSeverity
    }
    class Severity {
        <<Enumeration>>
        info
        warning
        critical
        normal
    }
    class ExceptionKind {
        <<Enumeration>>
        flow_balance_risk
    }
    DailyBrief *-- SiteBrief
    DailyBrief *-- OpenException
    SiteBrief *-- PathBrief
    PathBrief *-- PathTarget
    PathBrief *-- BacklogFact
    PathBrief *-- StaffingFact
    PathBrief *-- QueueFact
    PathBrief *-- StuckTasksFact
    PathBrief *-- OpenException
    PathBrief *-- CapacityOutlook
    CapacityOutlook *-- CapacityStepFact
    PathCapacityFact *-- CapacityStepFact
    OpenException --> Severity
    OpenException --> ExceptionKind
    RuntimeSignalsReport *-- ServiceSignal
    ServiceSignal --> Severity
    DailyBriefPolicy ..> PathBrief : builds
    CapacityOutlookPolicy ..> PathCapacityFact : reads
    CapacityOutlookPolicy ..> CapacityOutlook : builds
    RuntimeSignalsPolicy ..> Severity
```

Source: `internal/domain/policy/dailybrief.go`,
`internal/domain/policy/capacity_outlook.go`,
`internal/domain/policy/runtime_signals.go`.
Omits: `ServiceSignal.ErrorRateSev` / `LatencyP99Sev` / `SampleWindowMins`,
`PathCapacityFact.Steps` / `Warnings`, the threshold constants
(`ErrorRateWarningThreshold = 0.01`, `ErrorRateCriticalThreshold = 0.05`,
`LatencyP99WarningMS = 1000`, `LatencyP99CriticalMS = 3000`). `normal` is
the `SeverityNormal` constant of the alias `SignalSeverity = Severity`.

## E2 stranded reservation and ADR 0009 travel factor

```mermaid
classDiagram
    direction LR
    class StrandedReservationPolicy {
        <<Policy>>
        +Evaluate(in Inputs) StrandedReservationException
    }
    class TravelFactorPolicy {
        <<Policy>>
        +CorrelateTravelFactor(reading) TravelFactorCorrelation
        TravelFactorDistanceThresholdMetres = 60.0
    }
    class Inputs {
        <<ValueObject>>
        +TaskType TaskType
        +StuckTasks []StuckTaskSignal
        +StuckTasksAvailable bool
        +MinUsableThreshold int
        +Availability *AvailabilitySignal
        +ReservationId string
        +Bin *BlastRadius
    }
    class StuckTaskSignal {
        <<ValueObject>>
        +TaskId string
        +Type TaskType
        +LeaseStationId string
        +Reason string
    }
    class AvailabilitySignal {
        <<ValueObject>>
        +SKU string
        +Usable int
    }
    class BlastRadius {
        <<ValueObject>>
        +SKU string
        +BinId string
        +ReservationId string
        +QuantityFreed int
        +BinLines []BinLine
    }
    class BinLine {
        <<ValueObject>>
        +StockUnitId string
        +SKU string
        +Reserved int
        +Usable int
        +State string
    }
    class StrandedReservationException {
        <<ValueObject>>
        +Detected bool
        +Action StrandedReservationAction
        +ReservationId string
        +Rationale string
        +Evidence []EvidenceEntry
        +BlastRadius *BlastRadius
    }
    class EvidenceEntry {
        <<ValueObject>>
        +Tool string
        +Summary string
    }
    class TaskType {
        <<Enumeration>>
        PICK
        PACK
        SLAM
        +Valid() bool
    }
    class StrandedReservationAction {
        <<Enumeration>>
        revoke_reservation
        hold
    }
    class TravelDistanceReading {
        <<ValueObject>>
        +Source string
        +From string
        +To string
        +MetresM float64
        +Estimated bool
    }
    class TravelFactorCorrelation {
        <<ValueObject>>
        +Kind TravelFactorOutcomeKind
        +Rationale string
    }
    class TravelFactorOutcomeKind {
        <<Enumeration>>
        travel_significant
        travel_negligible
    }
    Inputs *-- StuckTaskSignal
    Inputs *-- AvailabilitySignal
    Inputs *-- BlastRadius
    BlastRadius *-- BinLine
    StrandedReservationException *-- EvidenceEntry
    StrandedReservationException *-- BlastRadius
    StrandedReservationException --> StrandedReservationAction
    Inputs --> TaskType
    StuckTaskSignal --> TaskType
    StrandedReservationPolicy ..> Inputs : reads
    StrandedReservationPolicy ..> StrandedReservationException : builds
    TravelFactorPolicy ..> TravelDistanceReading : reads
    TravelFactorPolicy ..> TravelFactorCorrelation : builds
    TravelFactorCorrelation --> TravelFactorOutcomeKind
```

Source: `internal/domain/policy/stranded_reservation.go`,
`internal/domain/policy/travel_factor.go`.
Omits: unexported helpers (`stuckByType`, `holdException`,
`revokeException`, `addEvidence`).

## Hexagonal ports and adapters

```mermaid
classDiagram
    direction LR
    class HTTPRouter {
        <<InboundAdapter>>
        inbound/http NewRouter
        8 GET routes + /mcp mount
    }
    class MCPServer {
        <<InboundAdapter>>
        inbound/mcp NewServer
        5 read-only tools
    }
    class DailyBriefUC {
        <<UseCase>>
        usecases.DailyBrief
    }
    class FlowBalanceAdvisoryUC {
        <<UseCase>>
        usecases.FlowBalanceAdvisory
    }
    class ExplainTravelFactorUC {
        <<UseCase>>
        usecases.ExplainTravelFactor
    }
    class DetectStrandedReservationUC {
        <<UseCase>>
        usecases.DetectStrandedReservation
    }
    class CapacityOutlookUC {
        <<UseCase>>
        usecases.CapacityOutlook
    }
    class OrderLifecycleUC {
        <<UseCase>>
        usecases.OrderLifecycle
    }
    class ConsoleReportsUC {
        <<UseCase>>
        usecases.ConsoleReports
    }
    class RuntimeSignalsUC {
        <<UseCase>>
        usecases.RuntimeSignals
    }
    class MCPClientPorts {
        <<Port>>
        WesWorkPlanningClient
        FulfillmentExecutionClient
        InventoryStorageClient
        WorkforceManagementClient
        FacilityLayoutClient
        LaborPerformanceClient
        OrderManagementMCPClient
        ProcessPathManagementClient
        WarehousePlanningClient
    }
    class RESTClientPorts {
        <<Port>>
        OrderManagementClient
        InventoryReservationsClient
        WorkUnitsClient
        TasksByOrderClient
        7 x ReportClient
    }
    class ObservabilityPorts {
        <<Port>>
        TelemetryReader
        LogReader
        ArbitrationMetrics
    }
    class ReasonerPorts {
        <<Port>>
        Reasoner
        ToolInvoker
    }
    class mcpclient {
        <<OutboundAdapter>>
        Session + 9 typed clients + ToolInvoker
    }
    class restclient {
        <<OutboundAdapter>>
        4 OLTP + 7 reports clients
    }
    class telemetry_logs {
        <<OutboundAdapter>>
        PrometheusReader, StubReader, LokiReader, ArbitrationMetrics
    }
    class anthropic {
        <<OutboundAdapter>>
        Reasoner with gobreaker
    }
    HTTPRouter --> DailyBriefUC
    HTTPRouter --> FlowBalanceAdvisoryUC
    HTTPRouter --> ExplainTravelFactorUC
    HTTPRouter --> OrderLifecycleUC
    HTTPRouter --> ConsoleReportsUC
    HTTPRouter --> RuntimeSignalsUC
    MCPServer --> DailyBriefUC
    MCPServer --> FlowBalanceAdvisoryUC
    MCPServer --> ExplainTravelFactorUC
    MCPServer --> DetectStrandedReservationUC
    DailyBriefUC --> CapacityOutlookUC
    DailyBriefUC --> MCPClientPorts
    FlowBalanceAdvisoryUC --> MCPClientPorts
    FlowBalanceAdvisoryUC --> ReasonerPorts
    FlowBalanceAdvisoryUC --> ObservabilityPorts
    ExplainTravelFactorUC --> MCPClientPorts
    DetectStrandedReservationUC --> MCPClientPorts
    CapacityOutlookUC --> MCPClientPorts
    OrderLifecycleUC --> RESTClientPorts
    ConsoleReportsUC --> RESTClientPorts
    RuntimeSignalsUC --> ObservabilityPorts
    mcpclient ..|> MCPClientPorts
    mcpclient ..|> ReasonerPorts
    restclient ..|> RESTClientPorts
    telemetry_logs ..|> ObservabilityPorts
    anthropic ..|> ReasonerPorts
```

Source: `internal/adapters/inbound/http/router.go`,
`internal/adapters/inbound/mcp/{server,tools}.go`,
`internal/application/usecases/*.go`, `internal/ports/*.go`,
`internal/adapters/outbound/**`, `cmd/agent/main.go`.
Omits: the `ToolInvoker` lives in `mcpclient` but implements the
`ports.ToolInvoker` half of `ReasonerPorts` only (the anthropic adapter
implements `Reasoner`); `cmd/agent` (the only package that wires layers);
`internal/config`, `internal/observability`, `internal/resilience`. Every
use case also depends on `internal/domain/policy` (not drawn).
