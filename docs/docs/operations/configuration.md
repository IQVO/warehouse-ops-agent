---
id: configuration
title: Configuration
sidebar_label: Configuration
description: Every environment variable the agent binary reads, with its default, whether it is required, what it does, the source file and the Helm value that sets it.
---

# Configuration

The repo builds one binary, `agent` (`cmd/agent`). All of its
configuration comes from environment variables. Most are read in
`internal/config/config.go` (`config.Load`). A few are read elsewhere:
`LOG_LEVEL` in `cmd/agent/main.go`, `CORS_ALLOWED_ORIGINS` in
`internal/adapters/inbound/http/router.go`, and the OpenTelemetry variables
in `internal/observability/telemetry.go`. Nothing is read from a file or a
flag.

Startup fails, and the process exits with status 1, in four cases:

1. `DAILY_BRIEF_PATH_TARGETS` is set but is not a JSON array of targets, or
   is an empty array or `null` ([ADR 0017](../adr/0017-empty-path-targets-is-a-config-error.md)).
2. `LLM_MODE` is not `off`, `shadow`, `on` or empty.
3. `LLM_MODE` is `shadow` or `on` and `ANTHROPIC_API_KEY` is empty.
4. A `LLM_TOOL_ALLOWLIST` entry is not of the form `<upstream>/<tool>`.

Every other bad value falls back to the default or turns the feature off.
The startup log line `warehouse-ops-agent listening` reports which optional
endpoints are configured (for example
`product_master_endpoint_configured: true`) and how many path targets were
loaded.

## `agent` — every variable

"Chart value" is the key in `charts/warehouse-ops-agent/values.yaml` that
renders the variable. "none" means the chart does not set it; use
`extraEnv` for those.

### Listener, logging, CORS

| Variable | Default | Required | Meaning | Source | Chart value |
|---|---|---|---|---|---|
| `AGENT_ADDR` | `:8095` | no | Listen address for every REST route and for `/mcp`. | `internal/config/config.go` | `config.agentAddr` |
| `LOG_LEVEL` | `info` | no | `debug`, `warn` (or `warning`), `error`; any other value means `info`. Logs are JSON on stdout. | `cmd/agent/main.go` | `config.logLevel` |
| `CORS_ALLOWED_ORIGINS` | `http://localhost:5173` | no | Comma-separated origins allowed to call the API from a browser (warehouse-console). | `internal/adapters/inbound/http/router.go` | none |

### Daily brief

| Variable | Default | Required | Meaning | Source | Chart value |
|---|---|---|---|---|---|
| `DAILY_BRIEF_PATH_TARGETS` | one target: `{"siteCode":"WH1","pathId":"pick-zone-a","processPath":"PICK","buildingId":"wh1","shiftId":"shift-1"}` | no | JSON array of the process paths the daily brief monitors. Fields per target: `siteCode` (facility-layout site), `pathId` (wes-work-planning path), `processPath` (fulfillment-execution queue, `PICK`/`PACK`/`SLAM`), `buildingId` and `shiftId` (workforce-management), and the optional `planningPathId`, `unitsPerOrder`, `packagesPerOrder` for the capacity outlook. The `pathId` to `processPath` binding is also what `GET /flow-balance/{pathId}` uses to pick the labor-performance task type. Malformed, `[]` or `null` stops startup. | `internal/config/config.go` (`loadPathTargets`) | `config.dailyBriefPathTargets` |
| `CAPACITY_OUTLOOK_HORIZON` | `8h` | no | Go duration: how far ahead the warehouse-planning capacity window reaches. An unparseable or non-positive value means `8h`. Used only when `WAREHOUSE_PLANNING_MCP_ENDPOINT` is set. | `internal/config/config.go` | `config.capacityOutlookHorizon` |

### Upstream MCP endpoints

Each value is a Streamable HTTP endpoint, for example
`http://wes-work-planning-mcp.warehouse-systems.svc.cluster.local:8090/mcp`.
No credentials are sent ([ADR 0006](../adr/0006-fleet-wide-auth-removal.md)).
The two groups below behave differently when the variable is empty.

**Always-built clients.** The composition root builds these eight clients
even when the variable is empty. Every call then fails with
`unsupported protocol scheme ""`, and the use cases that need the client
degrade: an `unavailable` entry in the daily brief, a `hold` in the flow
balance, a `502` from explain-travel-factor.

| Variable | Default | Required | Upstream and use | Source | Chart value |
|---|---|---|---|---|---|
| `WES_WORK_PLANNING_MCP_ENDPOINT` | empty | needed by the daily brief and flow balance | `get_backlog_telemetry`, `get_rebalance_recommendation` | `internal/config/config.go` | `upstreams.wesWorkPlanning.endpoint` |
| `FULFILLMENT_EXECUTION_MCP_ENDPOINT` | empty | needed by the daily brief, flow balance and `detect_stranded_reservation` | `get_queue_status`, `diagnose_stuck_tasks` | `internal/config/config.go` | `upstreams.fulfillmentExecution.endpoint` |
| `WORKFORCE_MANAGEMENT_MCP_ENDPOINT` | empty | needed by the daily brief and flow balance | `get_staffing_gap` | `internal/config/config.go` | `upstreams.workforceManagement.endpoint` |
| `FACILITY_LAYOUT_MCP_ENDPOINT` | empty | needed by explain-travel-factor; the brief only loses site names | `list_sites`, `estimate_travel_distance` | `internal/config/config.go` | `upstreams.facilityLayout.endpoint` |
| `INVENTORY_STORAGE_MCP_ENDPOINT` | empty | needed by `detect_stranded_reservation` | `check_availability`, `get_bin_occupancy` | `internal/config/config.go` | `upstreams.inventoryStorage.endpoint` |
| `LABOR_PERFORMANCE_MCP_ENDPOINT` | empty | no; without it the flow balance has no `utilization` overlay | `get_task_type_utilization` | `internal/config/config.go` | `upstreams.laborPerformance.endpoint` |
| `ORDER_MANAGEMENT_MCP_ENDPOINT` | empty | no; the client is wired but no use case calls it | `get_order` | `internal/config/config.go` | `upstreams.orderManagement.endpoint` |
| `PROCESS_PATH_MANAGEMENT_MCP_ENDPOINT` | empty | no; the client is wired but no use case calls it | `get_process_path`, `list_process_paths` | `internal/config/config.go` | `upstreams.processPathManagement.endpoint` |

**Optional clients.** When the variable is empty no client is built and
the feature is off. Nothing else changes.

| Variable | Default | Required | Effect when empty | Source | Chart value |
|---|---|---|---|---|---|
| `WAREHOUSE_PLANNING_MCP_ENDPOINT` | empty | no | No `capacityOutlook` in the daily brief ([ADR 0013](../adr/0013-warehouse-planning-mcp-client-and-capacity-outlook.md)). | `internal/config/config.go` | `upstreams.warehousePlanning.endpoint` |
| `PRODUCT_MASTER_MCP_ENDPOINT` | empty | no | `GET /master-data-gaps` answers `503` and `find_master_data_gaps` is not registered ([ADR 0020](../adr/0020-product-master-mcp-client-and-master-data-gaps.md)). | `internal/config/config.go` | `upstreams.productMaster.endpoint` |
| `INBOUND_RECEIVING_MCP_ENDPOINT` | empty | no | `GET /inbound-outlook` answers `503` and `get_inbound_outlook` is not registered ([ADR 0021](../adr/0021-inbound-receiving-mcp-client-and-inbound-outlook.md)). | `internal/config/config.go` | `upstreams.inboundReceiving.endpoint` |
| `NETWORK_INVENTORY_PLANNING_MCP_ENDPOINT` | empty | no | The three `/transfer-watch/*` routes answer `503` and the three transfer-watch tools are not registered ([ADR 0019](../adr/0019-network-inventory-planning-transfer-watch.md)). | `internal/config/config.go` | `upstreams.networkInventoryPlanning.endpoint` |
| `INBOUND_STALE_RECEIPT_AGE` | none | no | Go duration, e.g. `6h`. An Open receipt older than this is listed in `staleReceipts`. Unset, unparseable or non-positive means the section is omitted with the reason `INBOUND_STALE_RECEIPT_AGE is not configured`. There is deliberately no default. | `internal/config/config.go` | `config.inboundStaleReceiptAge` |

### console-bff REST base URLs

The order-lifecycle route calls the OLTP APIs, and the two dashboards call
each context's separate `*-reports` reader binary. Every client uses a 5 s
timeout. A wrong or unreachable URL degrades the stage or section; it
never fails startup.

| Variable | Default | Required | Called by | Requests | Source | Chart value |
|---|---|---|---|---|---|---|
| `ORDER_MANAGEMENT_REST_URL` | `http://localhost:8086` | no | `GET /console/orders/{id}/lifecycle` | `GET /orders/<id>` | `internal/config/config.go` | `restUrls.orderManagement` |
| `INVENTORY_STORAGE_REST_URL` | `http://localhost:8082` | no | order lifecycle | `GET /reservations?demandRef=` | `internal/config/config.go` | `restUrls.inventoryStorage` |
| `WES_WORK_PLANNING_REST_URL` | `http://localhost:8083` | no | order lifecycle | `GET /work-units?reference=` | `internal/config/config.go` | `restUrls.wesWorkPlanning` |
| `FULFILLMENT_EXECUTION_REST_URL` | `http://localhost:8084` | no | order lifecycle | `GET /tasks?orderRef=` | `internal/config/config.go` | `restUrls.fulfillmentExecution` |
| `FACILITY_LAYOUT_REPORTS_REST_URL` | `http://localhost:8101` | no | `GET /console/reports/wms`, section `catalog-growth` | `GET /reports/catalog-growth` and `/freshness` | `internal/config/config.go` | `reportsUrls.facilityLayout` |
| `INVENTORY_STORAGE_REPORTS_REST_URL` | `http://localhost:8102` | no | WMS dashboard, section `inventory-flow-accuracy` | `GET /reports/flow-accuracy` and `/freshness` | `internal/config/config.go` | `reportsUrls.inventoryStorage` |
| `WES_WORK_PLANNING_REPORTS_REST_URL` | `http://localhost:8103` | no | `GET /console/reports/wes`, section `planning-throughput` | `GET /reports/throughput` and `/freshness` | `internal/config/config.go` | `reportsUrls.wesWorkPlanning` |
| `FULFILLMENT_EXECUTION_REPORTS_REST_URL` | `http://localhost:8104` | no | WES dashboard, section `fulfillment-throughput` | `GET /reports/throughput` and `/freshness` | `internal/config/config.go` | `reportsUrls.fulfillmentExecution` |
| `WORKFORCE_MANAGEMENT_REPORTS_REST_URL` | `http://localhost:8105` | no | WES dashboard, section `labor-management` | `GET /reports/labor` and `/freshness` | `internal/config/config.go` | `reportsUrls.workforceManagement` |
| `ORDER_MANAGEMENT_REPORTS_REST_URL` | `http://localhost:8106` | no | WMS dashboard, section `order-funnel` | `GET /reports/funnel` and `/freshness` | `internal/config/config.go` | `reportsUrls.orderManagement` |
| `LABOR_PERFORMANCE_REPORTS_REST_URL` | `http://localhost:8107` | no | WES dashboard, section `labor-performance` | `GET /reports/performance` and `/freshness` | `internal/config/config.go` | `reportsUrls.laborPerformance` |

The `8101`–`8107` defaults are a local port assignment made in
`config.go`. Every `*-reports` binary itself defaults to `:8092`, so run
them with per-service overrides when several run on one machine. The
chart renders a `*_REST_URL` or `*_REPORTS_REST_URL` key only when its
value is non-empty, so with empty values the binary falls back to these
localhost defaults. In the kind cluster, warehouse-infra
(`terraform/ops-agent.tf`) sets all eleven to the in-cluster Services, for
example `http://order-management-reports.warehouse-systems.svc.cluster.local:80`.

### Runtime signals

| Variable | Default | Required | Meaning | Source | Chart value |
|---|---|---|---|---|---|
| `PROMETHEUS_URL` | empty | no | Prometheus base URL for `GET /runtime-signals` (queries `/api/v1/query`). Empty means a stub reader that returns no samples: every metric reads 0 and `prometheus` is not listed as unavailable. | `internal/config/config.go`, `cmd/agent/main.go` (`newTelemetryReader`) | `prometheus.url` |
| `LOKI_URL` | empty | no | Loki base URL (queries `/loki/api/v1/query_range`). Empty means no log reader, and `loki` is listed in `unavailableSources`. | `internal/config/config.go`, `cmd/agent/main.go` (`newLogReader`) | `runtimeSignals.lokiUrl` |
| `RUNTIME_SIGNALS_NAMESPACE` | `warehouse-systems` | no | Kubernetes namespace the Loki query is scoped to. | `internal/config/config.go` | `runtimeSignals.namespace` |
| `RUNTIME_SIGNALS_SERVICES` | `order-management,inventory-storage,wes-work-planning,fulfillment-execution,workforce-management,facility-layout,labor-performance,process-path-management` | no | Comma-separated Istio `destination_service_name` values (and Loki `app` labels) to report on. product-master, warehouse-planning, inbound-receiving and the network contexts are not in the default. | `internal/config/config.go` | `runtimeSignals.services` (a list, joined with commas) |

warehouse-infra's `terraform/ops-agent.tf` (origin/develop) sets neither
`PROMETHEUS_URL` nor `LOKI_URL`. In the kind cluster `/runtime-signals`
therefore reports zeros and lists `loki` as unavailable until the chart
values `prometheus.url` and `runtimeSignals.lokiUrl` are set.

### LLM reasoner (ADR 0004)

These variables matter only for `GET /flow-balance/{pathId}` and
`get_flow_balance_exception`. The deterministic policy always runs first;
see [ADR 0004](../adr/0004-llm-reasoner-behind-the-policy-layer.md) and
[ADR 0011](../adr/0011-reasoner-path-circuit-breaker-timeout-retry.md).

| Variable | Default | Required | Meaning | Source | Chart value |
|---|---|---|---|---|---|
| `LLM_MODE` | `off` | no | `off`: the model is never called. `shadow`: the model is called, its plan is logged and counted, and the deterministic decision is returned. `on`: a valid plan replaces the action, heads and rationale, and the deterministic decision is the fallback on error, timeout or invalid output. Any other value stops startup. | `internal/config/config.go`, `cmd/agent/reasoner.go`, `internal/domain/policy/arbitrate.go` | `llm.mode` |
| `ANTHROPIC_API_KEY` | empty | yes when `LLM_MODE` is `shadow` or `on` | Sent as the `x-api-key` header to the Anthropic Messages API. Never logged. | `internal/config/config.go` | `credentials.anthropicApiKey`, or a Secret named by `credentials.existingSecret` with key `ANTHROPIC_API_KEY`; the env entry is rendered only when `llm.mode` is not `off` |
| `LLM_MODEL` | empty, which the adapter turns into `claude-sonnet-4-5` | no | Model id sent in every request. | `internal/config/config.go`, `internal/adapters/outbound/llm/anthropic/reasoner.go` | `llm.model` |
| `LLM_BASE_URL` | empty, which means `https://api.anthropic.com` | no | Base URL for `POST /v1/messages`; for tests and proxies. | `internal/config/config.go`, `internal/adapters/outbound/llm/anthropic/reasoner.go` | none |
| `LLM_TIMEOUT` | `8s` | no | Go duration bounding one whole reasoning call (every tool-use turn). An unparseable or non-positive value means `8s`. | `internal/config/config.go` | `llm.timeout` |
| `LLM_TOOL_ALLOWLIST` | `wes-work-planning/get_backlog_telemetry,wes-work-planning/get_rebalance_recommendation,workforce-management/get_staffing_gap,fulfillment-execution/get_queue_status,fulfillment-execution/diagnose_stuck_tasks` | no | Comma-separated `<upstream>/<tool>` MCP read tools the model may call. Only five upstream names have a session in the reasoner: `wes-work-planning`, `fulfillment-execution`, `workforce-management`, `inventory-storage`, `facility-layout`. An entry for any other upstream is accepted but never offered to the model. | `internal/config/config.go`, `cmd/agent/reasoner.go` | `llm.toolAllowList` (a list) |

warehouse-infra runs the kind cluster with `llm.mode = "shadow"` and the
key in the Secret `warehouse-ops-agent-anthropic`
(`terraform/ops-agent.tf`).

### OpenTelemetry

| Variable | Default | Required | Meaning | Source | Chart value |
|---|---|---|---|---|---|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | no | OTLP/gRPC Collector address for traces and metrics. A bare `host:port` gets `http://` prepended. An unreachable Collector only drops telemetry and logs `opentelemetry error`. | `internal/observability/telemetry.go` | `otel.endpoint` (rendered when `otel.enabled`) |
| `OTEL_SERVICE_NAME` | `warehouse-ops-agent` | no | `service.name` resource attribute, and the service name passed to otelchi. | `internal/observability/telemetry.go` | `otel.serviceName` |
| `SERVICE_VERSION` | `dev` | no | `service.version` resource attribute. | `internal/observability/telemetry.go` | the image tag, or the chart `appVersion` |
| `ENVIRONMENT` | `local` | no | `deployment.environment.name` resource attribute. | `internal/observability/telemetry.go` | `environment` |

## Fixed values (not configurable)

These are constants in the code. Changing one needs a code change.

| Value | Setting | Source |
|---|---|---|
| Upstream MCP tool call timeout | 10 s per call, with a fresh MCP session per call | `internal/adapters/outbound/mcpclient/session.go` |
| REST client timeout (OLTP and `*-reports`) | 5 s | `cmd/agent/main.go` |
| Prometheus and Loki client timeout | 5 s | `cmd/agent/main.go` |
| Runtime-signals window and Loki line limit | 10 minutes, 50 lines | `cmd/agent/main.go`, `internal/application/usecases/runtime_signals.go` |
| Stuck-task window for the flow balance | 15 minutes (`withinSeconds = 900`) | `internal/application/usecases/flow_balance_advisory.go` |
| Master-data and inbound scan bound | 500 records per page, 10 pages | `internal/application/usecases/master_data_gaps.go`, `inbound_outlook.go` |
| Transfer-watch page limit | at most 200 | `internal/application/usecases/transfer_watch.go` |
| Reasoner per-attempt timeout, retries | 12 s per Anthropic call, at most 3 attempts with jittered backoff (100 ms to 1 s) on transport errors and 5xx | `internal/adapters/outbound/llm/anthropic/reasoner.go` |
| Reasoner turns and tokens | 6 tool-use turns, 1024 max tokens per request | `internal/adapters/outbound/llm/anthropic/reasoner.go` |
| Reasoner circuit breaker | opens after 5 consecutive failures, or more than 50% failures once 10 requests are counted in a 30 s window; stays open 30 s; 1 probe when half-open | `internal/resilience/breaker.go` |
| Reasoner tool discovery at startup | bounded by 15 s | `cmd/agent/reasoner.go` |
| HTTP server | `ReadHeaderTimeout` 5 s; on SIGINT or SIGTERM in-flight requests get 10 s to finish | `cmd/agent/main.go` |

## Example: a local run against the e2e harness

The ports match `e2e-tests/env.sh`. The agent moves to `:8096` because the
harness puts workforce-management's MCP server on `:8095`:

```bash
export FACILITY_LAYOUT_MCP_ENDPOINT=http://localhost:8091/mcp
export INVENTORY_STORAGE_MCP_ENDPOINT=http://localhost:8092/mcp
export WES_WORK_PLANNING_MCP_ENDPOINT=http://localhost:8093/mcp
export FULFILLMENT_EXECUTION_MCP_ENDPOINT=http://localhost:8094/mcp
export WORKFORCE_MANAGEMENT_MCP_ENDPOINT=http://localhost:8095/mcp
export AGENT_ADDR=:8096
export DAILY_BRIEF_PATH_TARGETS='[{"siteCode":"WH1","pathId":"pick-zone-a","processPath":"PICK","buildingId":"wh1","shiftId":"shift-1"}]'
go run ./cmd/agent
```
