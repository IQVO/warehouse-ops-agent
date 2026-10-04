---
paths:
  - "internal/config/**"
  - "cmd/**"
  - "charts/**"
---

# Configuration (env vars) and running locally

Module `github.com/claudioed/warehouse-ops-agent`. Entrypoint
`cmd/agent/main.go` (composition root) + `cmd/agent/reasoner.go` (wires the
ADR-0004 LLM reasoner). Every default value lives in
`internal/config/config.go`.

## Env vars

One Streamable-HTTP endpoint per upstream MCP context:

| Context | Endpoint env var |
|---|---|
| wes-work-planning | `WES_WORK_PLANNING_MCP_ENDPOINT` |
| fulfillment-execution | `FULFILLMENT_EXECUTION_MCP_ENDPOINT` |
| inventory-storage | `INVENTORY_STORAGE_MCP_ENDPOINT` |
| workforce-management | `WORKFORCE_MANAGEMENT_MCP_ENDPOINT` |
| facility-layout | `FACILITY_LAYOUT_MCP_ENDPOINT` |
| labor-performance | `LABOR_PERFORMANCE_MCP_ENDPOINT` |
| order-management | `ORDER_MANAGEMENT_MCP_ENDPOINT` |
| process-path-management | `PROCESS_PATH_MANAGEMENT_MCP_ENDPOINT` |
| warehouse-planning | `WAREHOUSE_PLANNING_MCP_ENDPOINT` (unset = no client and no capacity outlook; read tools only, ADR 0013) |

Plus `AGENT_ADDR` (default `:8095`), `PROMETHEUS_URL` / `LOKI_URL`
(runtime-signals sources; unset Prometheus → stub reader, unset Loki →
reported in `unavailableSources`), `RUNTIME_SIGNALS_NAMESPACE` (default
`warehouse-systems`), `RUNTIME_SIGNALS_SERVICES` (comma-separated, default
the eight backend contexts), `DAILY_BRIEF_PATH_TARGETS` (optional JSON
array overriding the process paths the daily brief monitors — defaults to
the single path the e2e-tests bootstrap scenario seeds; each target may also
carry optional `planningPathId` / `unitsPerOrder` / `packagesPerOrder` for the
ADR 0013 capacity outlook, never defaulted), `CAPACITY_OUTLOOK_HORIZON` (Go
duration, default `8h`; only used when warehouse-planning is configured), and the
console-bff's own separate REST base URLs (`ORDER_MANAGEMENT_REST_URL`,
`INVENTORY_STORAGE_REST_URL`, `WES_WORK_PLANNING_REST_URL`,
`FULFILLMENT_EXECUTION_REST_URL`, plus seven `*_REPORTS_REST_URL` vars for
the analytics dashboards).

LLM reasoner (ADR 0004): `LLM_MODE` (`off`/`shadow`/`on`, default `off`),
`ANTHROPIC_API_KEY` (required unless `off`; never logged),
`LLM_MODEL` (default `claude-sonnet-4-5`), `LLM_TIMEOUT` (default `8s`),
`LLM_BASE_URL` (tests/proxies), `LLM_TOOL_ALLOWLIST` (comma-separated
`<upstream>/<tool>`, defaults to the five read tools the deterministic path
already uses).

## Run standalone, pointed at the fleet's MCP servers

Ports = e2e-tests env.sh:

```bash
export FACILITY_LAYOUT_MCP_ENDPOINT=http://localhost:8091/mcp
export INVENTORY_STORAGE_MCP_ENDPOINT=http://localhost:8092/mcp
export WES_WORK_PLANNING_MCP_ENDPOINT=http://localhost:8093/mcp
export FULFILLMENT_EXECUTION_MCP_ENDPOINT=http://localhost:8094/mcp
export WORKFORCE_MANAGEMENT_MCP_ENDPOINT=http://localhost:8095/mcp
export AGENT_ADDR=:8096
go run ./cmd/agent
curl -s http://localhost:8096/daily-brief | jq .
```

## Or run against the full fleet via the shared e2e harness

Needs the warehouse-infra kind cluster up (Kafka is its shared broker at
localhost:9092). Use this rather than standing up every upstream MCP server
by hand:

```bash
cd ~/warehouse-systems/e2e-tests
bash scripts/01-build.sh         # builds all binaries incl. this agent
bash scripts/02-up-infra.sh      # checks Kafka, starts the harness Postgres instances
bash scripts/03-up-services.sh   # starts every service + MCP server + this agent
```
