# warehouse-ops-agent

**⚠️ Study project.** This repository, and the bounded-context services it
reads from, are a personal DDD / hexagonal-architecture learning exercise
modeling a simplified warehouse fleet. Nothing here is production warehouse
software; treat all business behavior, quality claims, and "production-grade"
language as illustrative of the pattern being practiced, not operational
fact.

## What this is

A thin, independently-deployable **read-side / decision-support** mechanism
over the warehouse-systems fleet's bounded contexts. It is a **Customer** of
those contexts' published Model Context Protocol (MCP) Open Host Services —
never a new domain context. It owns no aggregate, enforces no new invariant,
and persists no domain state; its "domain" layer is decision **policy**
(correlation rules over facts read from the upstream contexts, plus a
runtime-signals classifier over Prometheus/Loki telemetry).

It holds an outbound MCP client for eleven contexts: the five original ones
(`wes-work-planning`, `fulfillment-execution`, `workforce-management`,
`facility-layout`, `inventory-storage`) and three second-wave ones
(`labor-performance`, `order-management`, `process-path-management`,
[ADR 0007](./docs/docs/adr/0007-second-wave-outbound-mcp-clients.md)).
`labor-performance` is consumed by the flow-balance utilization overlay
([ADR 0008](./docs/docs/adr/0008-labor-utilization-advisory-correlation.md));
`order-management` and `process-path-management` are wired but not yet
consumed by any use case. `warehouse-planning` (read tools only; its MCP
server is read+write) feeds an optional, fail-open capacity outlook in the
daily brief and has three more read tools wired but unconsumed
([ADR 0013](./docs/docs/adr/0013-warehouse-planning-mcp-client-and-capacity-outlook.md)).
`product-master` (read-only server, contract pinned to its published tool
registry) feeds the master-data gaps report
([ADR 0020](./docs/docs/adr/0020-product-master-mcp-client-and-master-data-gaps.md));
in the kind cluster warehouse-infra sets `PRODUCT_MASTER_MCP_ENDPOINT` to
`product-master-mcp:8090/mcp`, so the report is live there.
`network-inventory-planning` (read tools only) feeds the transfer watch
([ADR 0019](./docs/docs/adr/0019-network-inventory-planning-transfer-watch.md)).
Separately, it hosts the `console-bff` REST
fan-out for `warehouse-console` (ADR 0002/0003).

See [ADR 0001](./docs/docs/adr/0001-warehouse-ops-agent-placement.md) for
the placement decision this repo embodies. The full documentation
site (business context, DDD placement, API surface, governance note, and
every ADR) is published from `docs/` — see
[iqvo.github.io/warehouse-ops-agent](https://iqvo.github.io/warehouse-ops-agent/)
once the `Docs` GitHub Actions workflow has deployed it, or run it locally
with `cd docs && npm install && npm start`.

## Guardrails (non-negotiable)

- **No new domain aggregate; no direct DB writes.** This agent writes to any
  bounded context ONLY via that context's existing published write MCP
  tools (a later slice — none is wired yet).
- **Published contracts only.** No cross-repo Go imports of any upstream
  context's packages, ever — only their published MCP tool schemas (and,
  for the console-bff, plain REST). `internal/architecture/architecture_test.go`'s
  `TestNoDirectDependencyOnBoundedContexts` enforces this for the five
  original contexts' module paths plus `warehouse-planning`'s and
  `product-master`'s.
- **Zero write capability (v1), CI-enforced.**
  `internal/architecture/zerowrite/zerowrite_test.go` fails the build if an
  outbound client (`mcpclient`, `restclient`) gains a mutating HTTP method or
  an inbound MCP tool is registered without `ReadOnlyHint: true`.
- **GitFlow.** `feature/*` branches → PR into `develop`; CI verified green
  before merge. Never commit directly to `main`.
- **Untrusted model input.** Every tool argument this agent accepts (and
  the LLM reasoner's own `submit_plan` output) is validated; unknown enum
  values are rejected, never silently defaulted.

## Architecture

Hexagonal / Ports & Adapters, same shape as the sibling services:

```
cmd/agent/                          composition root (main.go, reasoner.go)
internal/
  domain/policy/                    pure decision-policy layer: flow balance
                                     (E1), stranded reservation (E2), daily
                                     brief (E3), LLM arbitration (ADR 0004),
                                     utilization correlation (ADR 0008),
                                     travel factor (ADR 0009), runtime-signal
                                     threshold classifiers
  application/usecases/             orchestrates policy over ports
  ports/                            OUT: one client interface per upstream
                                     context + TelemetryReader, LogReader,
                                     Reasoner, console-bff REST ports
  adapters/
    inbound/
      http/                           chi router (8 GET routes, see below)
      mcp/                            this agent's own MCP server (5
                                        read-only tools)
    outbound/mcpclient/             one thin, schema-typed MCP client per
                                     upstream context (9), Streamable HTTP
    outbound/restclient/            console-bff REST clients: 4 OLTP APIs
                                     + 7 *-reports analytics readers
    outbound/telemetry/             Prometheus HTTP API reader (stub when
                                     PROMETHEUS_URL is unset) + LLM
                                     arbitration metrics
    outbound/logs/                  Loki query_range reader
    outbound/llm/anthropic/         ADR-0004 Reasoner (Anthropic Messages
                                     API with tool use)
  config/                           env-var configuration loader
  observability/                    OTel setup + slog bridge
  architecture/                     arch-go + zero-write fitness tests
```

### Inbound surface

REST (all `GET`, unauthenticated — [ADR 0006](./docs/docs/adr/0006-fleet-wide-auth-removal.md)):
`/healthz`, `/daily-brief`, `/flow-balance/{pathId}`,
`/explain-travel-factor`, `/console/orders/{id}/lifecycle`,
`/console/reports/wms`, `/console/reports/wes`, `/runtime-signals`,
`/master-data-gaps`.
MCP at `/mcp` (Streamable HTTP, stateless): `get_daily_brief`,
`list_open_exceptions`, `get_flow_balance_exception`,
`explain_travel_factor`, `detect_stranded_reservation`,
`find_master_data_gaps` (all `ReadOnlyHint: true`; the last four are
registered only when their use case is wired). Full details:
[`docs/docs/api-surface.md`](./docs/docs/api-surface.md).

The ddd-crew DDD artifact pack (core domain chart, bounded context canvas,
context map, EventStorming, class / ER / sequence diagrams, and an honest
"no aggregate, no database, no events" record) lives under
[`docs/docs/ddd/`](./docs/docs/ddd/ddd-artifacts.md).

## Configuration

One Streamable-HTTP endpoint per upstream context, read from the
environment (no bearer keys — every upstream MCP server is
unauthenticated; an empty endpoint means that client is skipped):

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
| warehouse-planning | `WAREHOUSE_PLANNING_MCP_ENDPOINT` |
| product-master | `PRODUCT_MASTER_MCP_ENDPOINT` (unset = client disabled, fail-open; ADR 0020) |
| network-inventory-planning | `NETWORK_INVENTORY_PLANNING_MCP_ENDPOINT` (unset = client disabled; ADR 0019) |

Plus:

- `AGENT_ADDR` (default `:8095`) — this agent's own listen address, serving
  REST at `/` and its MCP server at `/mcp`.
- `DAILY_BRIEF_PATH_TARGETS` — optional JSON array overriding which process
  paths the daily brief monitors; defaults to the single `pick-zone-a` path
  the e2e-tests bootstrap scenario seeds when unset. A set value that is not
  a valid JSON array of targets, or that is an empty array (`[]`), aborts
  startup with a config error naming the variable (it never silently falls
  back to the default; unset the variable to get the default, ADR 0017).
  Each target may also carry
  optional `planningPathId`, `unitsPerOrder`, `packagesPerOrder` (ADR 0013):
  the warehouse-planning process-path id and workload conversion factors for
  the capacity outlook. There is no default for any of them.
- `CAPACITY_OUTLOOK_HORIZON` (Go duration, default `8h`) — how far ahead the
  daily brief's warehouse-planning capacity window extends; only meaningful
  when `WAREHOUSE_PLANNING_MCP_ENDPOINT` is set (unset = no outlook, no
  client, brief unchanged).
- `PROMETHEUS_URL`, `LOKI_URL` — back `GET /runtime-signals`. An unset
  `LOKI_URL` (or a failing Loki query) lists `loki` in `unavailableSources`;
  an unset `PROMETHEUS_URL` falls back to a no-op stub reader, so metrics
  read as zero (`normal`) rather than unavailable — only a failing
  Prometheus query lists `prometheus`.
  `RUNTIME_SIGNALS_NAMESPACE` (default `warehouse-systems`) scopes the Loki
  query; `RUNTIME_SIGNALS_SERVICES` (comma-separated) defaults to eight
  backend contexts (order-management, inventory-storage, wes-work-planning,
  fulfillment-execution, workforce-management, facility-layout,
  labor-performance, process-path-management; `internal/config/config.go`).
  product-master, warehouse-planning and the network contexts are not in
  that default: list them in the override to include them.
- console-bff REST base URLs: `ORDER_MANAGEMENT_REST_URL`,
  `INVENTORY_STORAGE_REST_URL`, `WES_WORK_PLANNING_REST_URL`,
  `FULFILLMENT_EXECUTION_REST_URL` (defaults `localhost:8086/8082/8083/8084`),
  and seven `*_REPORTS_REST_URL` vars for the analytics dashboards
  (defaults `localhost:8101`–`8107`). See `internal/config/config.go`.
- `CORS_ALLOWED_ORIGINS` (default `http://localhost:5173`), `LOG_LEVEL`,
  and the standard `OTEL_EXPORTER_OTLP_ENDPOINT` / `OTEL_SERVICE_NAME` /
  `SERVICE_VERSION` / `ENVIRONMENT`.

### Model-backed reasoner (ADR 0004)

`GET /flow-balance/{pathId}` can consult a real LLM behind the policy layer.
The deterministic `policy.Decide` always runs first; `LLM_MODE` decides what
the model's plan may do with its result:

| `LLM_MODE` | behaviour |
|---|---|
| `off` (default) | model never called; pre-ADR-0004 behaviour byte-for-byte |
| `shadow` | model called, plan logged and counted (`ops_agent_llm_agreement_total{agree}`), deterministic decision returned |
| `on` | a valid plan replaces action/heads/rationale; deterministic decision is the fallback on error, timeout or out-of-vocabulary output (`source=fallback` in the log line) |

The model's ONLY actuators are MCP read tools: `LLM_TOOL_ALLOWLIST`
(comma-separated `<upstream>/<tool>`, default = the five read tools the
deterministic path already uses) is invoked through the same `mcpclient`
sessions as everything else, every call schema-validated
upstream and logged as `llm.tool_call`. It answers only through a
`submit_plan` tool whose schema is the policy package's closed action
vocabulary; `policy.ValidatePlan` rejects anything else.

Env: `ANTHROPIC_API_KEY` (required unless `off`; startup fails loudly
otherwise), `LLM_MODEL` (default `claude-sonnet-4-5`), `LLM_TIMEOUT`
(default `8s`), `LLM_BASE_URL` (tests/proxies). An unrecognised `LLM_MODE`
is a startup error, never a silent `off`.

## Quality gate

```
make check          # fast pre-commit bundle: fmt-check vet build lint test
make check-all      # + coverage (90% gate) + arch-test (pre-push gate)
make mutation-fast  # gremlins over ./internal/domain (CI's blocking mutation job)
make vuln           # govulncheck ./...
```

`lefthook install` once to activate the pre-commit/pre-push git hooks.

## Status

Shipped on `develop` (all read-only, recommendations-only):

- **E3 daily brief** — `GET /daily-brief`, `get_daily_brief`,
  `list_open_exceptions`: backlog (wes), staffing gap (workforce-management),
  queue depth and stuck tasks (fulfillment-execution), grouped by
  facility-layout site. A path is an open exception only when at least two
  independent signals correlate; an unavailable upstream degrades that
  path to a typed partial result.
- **E1 flow-balance exception** — `GET /flow-balance/{pathId}`,
  `get_flow_balance_exception`, with the optional ADR-0004 LLM reasoner and
  the ADR-0008 labor-utilization overlay.
- **explain_travel_factor** (ADR 0009) — `GET /explain-travel-factor`,
  `explain_travel_factor`. A facility-layout tool rejection whose error text
  starts with a validation slug (`<slug>: <detail>` fleet convention —
  `malformed-*`, `invalid-*`, `*-required`, `validation-failed`,
  `missing-location-code`) is a 400; every other rejection, slug-less
  legacy text included, stays 502 (ADR 0018).
- **E2 stranded-reservation exception** — `detect_stranded_reservation`:
  correlates fulfillment-execution's expired-lease tasks with
  inventory-storage's usable-stock shortfall for one SKU into a ranked
  `revoke_reservation`-or-`hold` recommendation, with a mandatory blast
  radius before any revoke is recommended.
- **console-bff** (ADR 0002/0003) — order lifecycle and WMS/WES report
  dashboards.
- **Runtime signals** — `GET /runtime-signals`: per-service Istio 5xx rate
  and p99 latency from Prometheus plus error-log counts from Loki, classified
  by `policy.ClassifyErrorRate` (warning ≥ 1%, critical ≥ 5%) and
  `policy.ClassifyLatencyP99` (warning ≥ 1000 ms, critical ≥ 3000 ms).
- **Master-data gaps** (ADR 0020) — `GET /master-data-gaps` /
  `find_master_data_gaps`: product-master products that are unclassified or
  whose declared and measured dimensions disagree (product-master's own
  discrepancy flag), read through `list_products`. Off (503 / tool absent)
  unless `PRODUCT_MASTER_MCP_ENDPOINT` is set; warehouse-infra sets it in the
  kind cluster.
- **Transfer watch** (ADR 0019) — `GET /transfer-watch/stuck`,
  `/transfer-watch/transfers/{id}`, `/transfer-watch/imbalance` and the MCP
  tools `triage_stuck_transfers`, `get_transfer_status`,
  `explain_network_imbalance` over network-inventory-planning's read tools.
  Off (503 / tools absent) unless `NETWORK_INVENTORY_PLANNING_MCP_ENDPOINT`
  is set.

No write path exists (see the
[governance note](./docs/docs/mcp/governance-note.md)).
