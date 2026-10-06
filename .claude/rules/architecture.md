---
paths:
  - "internal/**"
  - "cmd/**"
---

# Architecture — package layout, adapter families, LLM reasoner design

## Three driving-use-case families in one process

1. The **MCP-Customer / decision-support** path (daily brief, E1
   flow-balance correlation with the ADR-0008 utilization overlay, ADR-0009
   explain-travel-factor) — the "agentic" surface.
2. The **console-bff** REST fan-out (ADR 0002/0003) backing
   `warehouse-console`'s Order Lifecycle screen and WMS/WES report
   dashboards — a separate concern with a separate outbound adapter family
   and separate REST clients.
3. The **runtime-signals** report (`GET /runtime-signals`) — Istio
   error-rate / p99 latency from Prometheus plus error-log counts from Loki,
   classified by `policy.ClassifyErrorRate` / `policy.ClassifyLatencyP99`.

No persisted state: every fact is re-derived from upstream reads at request
time. Listens on `AGENT_ADDR` (default `:8095`): REST at `/` (chi, all
`GET`) and this agent's own MCP server (Streamable HTTP) at `/mcp`, all
read-only.

## Architecture

Hexagonal / Ports & Adapters, same shape as the five sibling bounded-context
repos:

```
cmd/agent/                           composition root (main.go, reasoner.go)
internal/
  domain/policy/                     pure decision-policy layer:
                                        arbitrate.go   — combines deterministic
                                                          Decision + optional LLM
                                                          Plan (ADR 0004)
                                        dailybrief.go  — E3 daily brief correlation
                                        capacity_outlook.go — ADR 0013 shaping of
                                                          warehouse-planning's path
                                                          capacity (informational)
                                        flow_balance.go — E1 flow-balance correlation
                                        utilization_correlation.go — ADR 0008
                                                          overlay on E1
                                        travel_factor.go — ADR 0009
                                        runtime_signals.go — ClassifyErrorRate /
                                                          ClassifyLatencyP99
                                                          threshold classifiers
                                        stranded_reservation.go — E2, exposed as the
                                                          detect_stranded_reservation MCP tool
  application/usecases/              orchestrates policy over ports:
                                        dailybrief.go, flow_balance_advisory.go,
                                        explain_travel_factor.go,
                                        runtime_signals.go,
                                        stranded_reservation.go, order_lifecycle.go
                                        (console-bff), capacity_outlook.go (ADR 0013,
                                        fail-open section of the daily brief),
                                        console_reports*.go
                                        (console-bff WMS/WES dashboards)
  ports/                             OUT: one client interface per upstream
                                      context (clients.go: WesWorkPlanning,
                                      FulfillmentExecution, InventoryStorage,
                                      WorkforceManagement, FacilityLayout;
                                      clients_phase2.go: OrderManagementMCP,
                                      LaborPerformance, ProcessPathManagement;
                                      clients_planning.go: WarehousePlanning,
                                      read tools only — ADR 0013)
                                      + TelemetryReader, LogReader, Reasoner,
                                      ArbitrationMetrics + console-bff's
                                      separate REST port shapes
  adapters/
    inbound/
      http/          chi router: GET /healthz, /daily-brief,
                      /flow-balance/{pathId}, /explain-travel-factor,
                      /console/orders/{id}/lifecycle,
                      /console/reports/wms, /console/reports/wes,
                      /runtime-signals
      mcp/            this agent's OWN MCP server: get_daily_brief,
                      list_open_exceptions, get_flow_balance_exception,
                      explain_travel_factor (all ReadOnlyHint: true)
    outbound/
      mcpclient/      one thin, schema-typed MCP client per upstream
                      context, Streamable HTTP, unauthenticated
                      (facility_layout.go, fulfillment_execution.go,
                      inventory_storage.go, wes_work_planning.go,
                      workforce_management.go, labor_performance.go,
                      order_management.go, process_path_management.go,
                      warehouse_planning.go),
                      plus tool_invoker.go / session.go used by the LLM
                      reasoner's tool-use loop
      restclient/     console-bff's REST clients — a SEPARATE family from
                      mcpclient, pointed at each context's OLTP API
                      (clients.go) and separately at each context's
                      *-reports analytics reader binary
                      (reports_clients.go) — different process, different
                      Postgres, different base URL from the OLTP one
      llm/anthropic/  ADR-0004 Reasoner implementation: Anthropic Messages
                      API with tool use, restricted to the mcpclient
                      sessions/tools on LLM_TOOL_ALLOWLIST
      telemetry/      Prometheus HTTP API reader (prometheus_reader.go;
                      stub.go when PROMETHEUS_URL is unset) + the LLM
                      arbitration OTel counters (arbitration_metrics.go)
      logs/           Loki query_range reader (loki_reader.go; nil when
                      LOKI_URL is unset)
  config/             env-var configuration loader (internal/config/config.go)
  architecture/       arch-go hexagonal + no-cross-context-import fitness
                      tests (architecture_test.go, fitness_test.go) and
                      the zero-write scan (zerowrite/zerowrite_test.go)
  observability/      OTel setup + slog bridge
```

### The two outbound adapter families are deliberately not unified

`mcpclient` (synchronous MCP tool calls, at request time, backing the
decision-support use cases and the LLM reasoner's tool use) and
`restclient` (plain HTTP calls backing the `console-bff` fan-out) answer
different questions for different callers — an LLM host asking "what needs
attention right now" versus a browser asking "what happened to order X".
See [context-map.md](docs/docs/ecosystem/context-map.md) for the full
Mermaid diagram and the per-context MCP-tool / REST-endpoint table.

### Runtime signals (`GET /runtime-signals`)

`usecases.RuntimeSignals` reads Istio request metrics from Prometheus
(`istio_requests_total` 5xx fraction, `istio_request_duration_milliseconds_bucket`
p99) per service over a 10-minute window, plus error/fatal lines from Loki
scoped to `RUNTIME_SIGNALS_NAMESPACE`. The only decision is in
`policy/runtime_signals.go`: error rate warning ≥ 1% / critical ≥ 5%, p99
warning ≥ 1000 ms / critical ≥ 3000 ms, and any recent error log lifts a
service to at least `warning`. A failing Prometheus/Loki query (or unset
`LOKI_URL`) is reported in `unavailableSources`, never a request failure;
an unset `PROMETHEUS_URL` uses the stub reader (metrics read as 0).

### Model-backed reasoner (ADR 0004)

`GET /flow-balance/{pathId}` can consult a real Anthropic model behind the
deterministic policy layer. `policy.Decide` (deterministic) always runs
first; `LLM_MODE` controls what the model's plan may do with the result:

| `LLM_MODE` | behaviour |
|---|---|
| `off` (default) | model never called; byte-for-byte pre-ADR-0004 behaviour |
| `shadow` | model called, plan logged + counted (`ops_agent_llm_agreement_total{agree}`), deterministic decision still returned |
| `on` | a schema-valid plan replaces action/heads/rationale; deterministic decision is the fallback on error, timeout, or out-of-vocabulary output (logged `source=fallback`) |

The model's **only actuators are MCP read tools** (`LLM_TOOL_ALLOWLIST`,
comma-separated `<upstream>/<tool>`), invoked through the exact same
`mcpclient` sessions the deterministic path uses, schema-validated on every
call, and logged as `llm.tool_call`. It answers only through a
`submit_plan` tool whose schema is `policy`'s closed action vocabulary;
`policy.ValidatePlan`/`policy.Arbitrate` reject anything outside it. An
unrecognized `LLM_MODE` is a **startup error**, never a silent fallback to
`off`; a non-`off` mode with no `ANTHROPIC_API_KEY` is also a startup
error. See
[ADR 0004](docs/docs/adr/0004-llm-reasoner-behind-the-policy-layer.md) for
the full design and rationale.

### Reasoner-path resilience (ADR 0011)

The outbound Anthropic Messages API call inside
`internal/adapters/outbound/llm/anthropic`'s `Reasoner.call` (one HTTP
request per tool-use turn, not the whole `Reason` loop) is wrapped in a
per-dependency `sony/gobreaker` circuit breaker
(`ReadyToTrip`: 5 consecutive failures, or >50% failure rate once at
least 10 requests have been seen), a `resilience.CallTimeout`-derived
timeout capped at 12s per call, and a bounded `cenkalti/backoff/v4`
jittered retry (max 3 attempts, transient errors only — a 5xx or a
transport failure, never a 4xx). `internal/resilience` is a fresh,
repo-local package mirroring order-management's own circuit-breaker ADR shape (never
imported across repos — see the "no cross-context Go imports" guardrail
above, which applies fleet-wide, not just to the five sibling bounded
contexts). A breaker-OPEN rejection surfaces as an ordinary `Reasoner`
error, so it flows through the SAME existing
`policy.Arbitrate`/`LLMOn`-mode fallback ADR-0004 already specified —
formalizing, not replacing, that fallback. State transitions publish
`circuit_breaker_state{dependency="anthropic-llm"}` via
`internal/adapters/outbound/telemetry.CircuitBreakerMetrics`, reusing the
same OTel meter `ArbitrationMetrics` already uses. See
[ADR 0011](docs/docs/adr/0011-reasoner-path-circuit-breaker-timeout-retry.md)
for the full design and the numbers' rationale.
