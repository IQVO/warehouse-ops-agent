---
id: observability
title: Observability
sidebar_label: Observability
description: Every OpenTelemetry metric instrument, span and structured log line warehouse-ops-agent emits, how they leave the process, which dashboards show them, and suggested alerts.
---

# Observability

The agent pushes traces and metrics over OTLP/gRPC to an OpenTelemetry
Collector and writes JSON logs to stdout. It exposes **no** `/metrics`
scrape endpoint of its own. Everything below is read from the code on this
branch; the Prometheus series names are derived with the fleet's naming
rule and were not observed live.

## Export pipeline

| Signal | How it leaves the process | Source |
|---|---|---|
| Traces | OTLP/gRPC, insecure, batch span processor, to `OTEL_EXPORTER_OTLP_ENDPOINT` (default `localhost:4317`; a bare `host:port` gets `http://` prepended) | `internal/observability/telemetry.go` (`Setup`) |
| Metrics | OTLP/gRPC, periodic reader, same endpoint | `internal/observability/telemetry.go` |
| Logs | JSON (`log/slog` `JSONHandler`) on stdout; Alloy tails pod stdout into Loki in the kind cluster | `cmd/agent/main.go` (`newLogger`) |

Resource attributes on every span and metric: `service.name`
(`OTEL_SERVICE_NAME`, default `warehouse-ops-agent`), `service.version`
(`SERVICE_VERSION`, default `dev`) and `deployment.environment.name`
(`ENVIRONMENT`, default `local`), merged with the SDK defaults.
The propagator is W3C `tracecontext` plus `baggage`.

An unreachable Collector never blocks startup: the exporters dial lazily,
and export errors are logged as `opentelemetry error` at warn level. If
`Setup` itself fails, the agent logs `opentelemetry setup degraded` and
`opentelemetry disabled; traces and metrics will not be exported`, and
keeps serving.

The chart sets the OTel variables only when `otel.enabled` is `true` (the
default), pointing at
`otel-collector.observability.svc.cluster.local:4317`.

## Metric instruments

### Instruments this repo creates

| Instrument | Type | Unit | Attributes | When it is recorded | Source |
|---|---|---|---|---|---|
| `ops_agent_llm_arbitrations_total` | Int64Counter | `{arbitration}` | `use_case` (today only `flow_balance_advisory`), `mode` (`shadow` or `on`), `source` (`deterministic`, `llm`, `fallback`) | once per flow-balance decision that went through the reasoner | `internal/adapters/outbound/telemetry/arbitration_metrics.go` |
| `ops_agent_llm_agreement_total` | Int64Counter | `{agreement}` | `use_case`, `mode`, `agree` (bool) | only when the model returned a valid plan; `agree` is `true` when its action equals the deterministic one | same file |
| `circuit_breaker.state` | Int64Gauge | `{state}` | `dependency` (today only `anthropic-llm`) | on every breaker state change: `0` closed, `1` half-open, `2` open | `internal/adapters/outbound/telemetry/circuit_breaker_metrics.go` |

All three are registered by `wireReasoner` (`cmd/agent/reasoner.go`), and
only when `LLM_MODE` is `shadow` or `on`. With the default `off` none of
them exists. The breaker gauge has no data point until the first state
change, so "no series" means "closed since startup", not "unknown".

The meters are named `warehouse-ops-agent/llm` and
`warehouse-ops-agent/resilience`. Through the Collector's Prometheus
exporter the series become `ops_agent_llm_arbitrations_total`,
`ops_agent_llm_agreement_total` and `circuit_breaker_state`.

The shadow-mode rollout gate from
[ADR 0004](../adr/0004-llm-reasoner-behind-the-policy-layer.md) is the
agreement ratio:

```promql
sum(rate(ops_agent_llm_agreement_total{use_case="flow_balance_advisory", agree="true"}[1h]))
/
sum(rate(ops_agent_llm_agreement_total{use_case="flow_balance_advisory"}[1h]))
```

### Instruments from libraries

`internal/adapters/inbound/http/router.go` installs two otelchi v0.12.3
metric middlewares on every route, `/mcp` included
([ADR 0010](../adr/0010-standard-metrics-convention.md)):

| Instrument | Type | Unit | Attributes |
|---|---|---|---|
| `http.server.request.duration` | Float64Histogram | `s` | `http.method`, `http.scheme`, `http.route` (the chi pattern, e.g. `/flow-balance/{pathId}`); scope attribute `service.name` |
| `http.server.active_requests` | Int64UpDownCounter | `{request}` | same as above |

otelchi v0.12.3's default attribute set has **no status-code attribute**,
so a 5xx rate cannot be computed from these series. Use Kong's
`kong_http_requests_total{code=~"5.."}`, the `http request` log line's
`status` field, or Istio's `istio_requests_total` instead.

`go.opentelemetry.io/contrib/instrumentation/runtime` v0.71.0
(`runtime.Start` in `telemetry.go`) adds the Go runtime instruments:
`go.memory.used`, `go.memory.limit`, `go.memory.allocated`,
`go.memory.allocations`, `go.memory.gc.goal`, `go.goroutine.count`,
`go.processor.limit` and `go.config.gogc`.

No other instrument exists. In particular there is no counter per
upstream call, per MCP tool call or per degraded section; those show up
only in logs.

## Traces

| Span | Kind | Attributes | Created by |
|---|---|---|---|
| the chi route pattern, e.g. `/daily-brief`, `/flow-balance/{pathId}`, `/mcp` | server | otelchi's standard HTTP attributes | `otelchi.Middleware` with `WithChiRoutes` (`router.go`) |
| `mcp.tool <name>`, e.g. `mcp.tool get_daily_brief` | internal | `mcp.tool.name`; status `Error` with the message when the tool fails | `addTool` in `internal/adapters/inbound/mcp/tools.go` |
| `llm.tool_call` | internal | `llm.tool_call.upstream`, `llm.tool_call.tool`; status `Error` on a refusal or a failed call | `executeToolCall` in `internal/adapters/outbound/llm/anthropic/reasoner.go` |

Outbound calls are **not** instrumented: the MCP client, the REST clients
and the Prometheus, Loki and Anthropic clients use a plain `http.Client`
with no `otelhttp` transport. They create no client spans and send no
`traceparent`, so a trace stops at this agent and does not continue into
the upstream context.

## Logs

Every record is a JSON object with slog's `time`, `level` and `msg`.
`observability.TraceHandler` adds `trace_id` and `span_id` to any record
logged with a context that carries an active span. Only the request
logger (`http request`) and the reasoner's `llm.tool_call` line log with
the request context; the use-case warnings below call `Warn` without a
context, so they carry no `trace_id`. Correlate them by time and by the
`pathId`, `orderId` or `section` field instead.

| `msg` | Level | Fields | Emitted when |
|---|---|---|---|
| `http request` | info, error for status at least 500 | `method`, `path` (CR/LF stripped), `status`, `duration_ms`, `bytes`, `request_id` | every request, `/mcp` included (`RequestLogger`, `internal/adapters/inbound/http/logging.go`) |
| `warehouse-ops-agent listening` | info | `addr`, `http_routes`, `mcp_route`, one `*_endpoint_configured` bool per MCP upstream, `path_targets` | startup |
| `llm reasoner disabled` / `llm reasoner configured` | info | `mode`; when configured also `model`, `timeout`, `tools` | startup |
| `llm reasoner: tool discovery failed for an upstream` | warn | `error` | startup, per upstream that did not answer `tools/list` |
| `llm reasoner: circuit breaker metrics unavailable` | warn | `error` | startup, if the gauge could not be registered |
| `flow_balance_advisory: <upstream> unavailable` | warn | `pathId`, `error` | wes-work-planning, workforce-management, fulfillment-execution or labor-performance failed |
| `flow_balance_advisory: partial decision` | warn | `pathId`, `missingSignals` | the decision lacked a signal |
| `flow_balance_advisory: llm arbitration` | info | `pathId`, `mode`, `source`, `model`, `latency_ms`, `tool_calls`, `deterministic_action`, and when present `llm_action`, `llm_heads`, `agree`, `reason` | every reasoner run |
| `llm.tool_call` | info | `upstream`, `tool`, `outcome`, `args_sha256`, `latency_ms` | every tool call the model asked for, refusals included; raw arguments are never logged |
| `explain_travel_factor: facility-layout unavailable` | warn | `pathId`, `from`, `to`, `error` | the facility-layout call failed |
| `order_lifecycle: <context> unavailable` | warn | `orderId`, `error`, and `workUnitId` for fulfillment-execution | one stage of the lifecycle failed |
| `console_reports: section unwired` / `section unavailable` / `freshness unavailable` | warn | `section`, `sourceContext`, `error` | a dashboard section had no client, its report failed, or only its `/freshness` call failed |
| `transfer_watch: network-inventory-planning call failed` | warn | `tool`, `error` | any transfer-watch upstream error |
| `opentelemetry error` | warn | `error` | an OTel export or SDK error |
| `warehouse-ops-agent exited with error` | error | `error` | startup failed (config or reasoner wiring); the process exits 1 |
| `http server failed` | error | `error` | `ListenAndServe` failed, e.g. the port is taken |

Every user-supplied value in a log field passes through `sanitizeForLog`,
which strips CR and LF (CWE-117).

## Dashboards

No dashboard in this repo. In warehouse-infra (`origin/develop`):

- `terraform/dashboards/kong-gateway-overview.json` shows request rate,
  5xx rate and upstream latency per Kong service, which includes the
  agent's `/api/warehouse-ops-agent` route.
- `terraform/dashboards/go-runtime.json` has a `service_name` variable
  (`label_values(service_name)`) and plots `go.goroutine.count` and
  `go.memory.used`, so `warehouse-ops-agent` can be selected there.
- There is **no** per-context dashboard for the agent:
  `scripts/gen-context-dashboards.py` generates them only for the bounded
  contexts, and `logs-overview.json` explicitly excludes
  `app="warehouse-ops-agent"` from its fleet-wide error panels. Query Loki
  directly with `{app="warehouse-ops-agent"}`.

## Suggested alerts

None of these exist yet; they are proposals grounded in the instruments
above.

| Alert | Expression (Prometheus or LogQL) | Why |
|---|---|---|
| LLM breaker open | `max(circuit_breaker_state{dependency="anthropic-llm"}) == 2` for 5m | the model is being skipped; in `on` mode every flow-balance answer is a `fallback` |
| Shadow agreement low | the agreement ratio above `< 0.8` over 1h with at least 20 samples | gate for moving from `shadow` to `on` |
| `on`-mode fallbacks | `sum(rate(ops_agent_llm_arbitrations_total{mode="on", source="fallback"}[15m])) > 0` | the model is failing or producing invalid plans |
| Upstream degradation | `sum by (msg) (count_over_time({app="warehouse-ops-agent"} \| json \| level="WARN" \| msg=~".*unavailable.*" [10m])) > 10` | a sibling context is down; answers are partial |
| Agent 5xx at the edge | `sum(rate(kong_http_requests_total{service=~".*warehouse-ops-agent.*", code=~"5.."}[5m])) > 0` | `502` from explain-travel-factor, master-data gaps, inbound outlook or transfer watch means an upstream failed |
| Slow answers | `histogram_quantile(0.95, sum by (le, http_route) (rate(http_server_request_duration_seconds_bucket{service_name="warehouse-ops-agent"}[5m]))) > 10` | each MCP call may take up to 10 s, and the brief runs them in sequence |

## Related pages

- [Runbook](./runbook.md)
- [Troubleshooting](./troubleshooting.md)
- [Configuration](./configuration.md) — the OTel variables.
