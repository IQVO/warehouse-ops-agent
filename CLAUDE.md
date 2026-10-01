# CLAUDE.md — warehouse-ops-agent

`warehouse-ops-agent` is a single Go binary that is a **thin, read-side /
decision-support Customer** of the warehouse-systems fleet's bounded
contexts: the five original ones (`wes-work-planning`,
`fulfillment-execution`, `inventory-storage`, `workforce-management`,
`facility-layout`) plus three second-wave MCP clients
(`labor-performance`, `order-management`, `process-path-management`,
ADR 0007). It owns no aggregate, enforces no domain invariant, and persists
no state — it holds no database. It correlates facts read from those
contexts' published MCP Open Host Services (plus plain REST for the
console-bff, and Prometheus/Loki for the runtime-signals report) through a
pure decision-**policy** layer, with a real LLM ("reasoner") optionally
consulted behind that policy layer as of ADR 0004. This repo has **no
`apis/` directory and no OpenAPI/AsyncAPI spec by design** — see
"Role in the fleet" below.

> ⚠️ **Study project.** This repo and its upstream services are a
> personal DDD/hexagonal-architecture learning exercise. Treat all
> "production-grade" language as illustrating the pattern being practiced,
> not an operational claim.

## Project Overview

- **Module**: `github.com/claudioed/warehouse-ops-agent`, Go 1.26.6.
- **Entrypoint**: `cmd/agent/main.go` (composition root) +
  `cmd/agent/reasoner.go` (wires the ADR-0004 LLM reasoner).
- **Listens on** `AGENT_ADDR` (default `:8095`), serving:
  - REST at `/` (chi router, all `GET` — `/healthz`, `/daily-brief`,
    `/flow-balance/{pathId}`, `/explain-travel-factor`,
    `/console/orders/{id}/lifecycle`, `/console/reports/wms`,
    `/console/reports/wes`, `/runtime-signals`)
  - This agent's **own** MCP server (Streamable HTTP) at `/mcp`
    (`get_daily_brief`, `list_open_exceptions`,
    `get_flow_balance_exception`, `explain_travel_factor` — all read-only)
- **No persisted state.** Restart it and it has forgotten nothing; every
  fact it reasons over is re-derived from upstream MCP reads (or REST /
  Prometheus / Loki reads) at request time.
- **Three independent driving-use-case families in one process**:
  1. The **MCP-Customer / decision-support** path (daily brief, E1
     flow-balance correlation with the ADR-0008 utilization overlay,
     ADR-0009 explain-travel-factor) — this is the "agentic" surface.
  2. The **console-bff** REST fan-out (ADR 0002/0003) backing
     `warehouse-console`'s Order Lifecycle screen and WMS/WES report
     dashboards — a separate concern, separate outbound adapter family,
     separate REST clients.
  3. The **runtime-signals** report (`GET /runtime-signals`) — Istio
     error-rate / p99 latency from Prometheus plus error-log counts from
     Loki, classified by `policy.ClassifyErrorRate` /
     `policy.ClassifyLatencyP99`.

## Role in the fleet: Customer of Open Host Services via MCP

Per [ADR 0001](docs/docs/adr/0001-warehouse-ops-agent-placement.md) and the
[subdomain classification](docs/docs/ddd/subdomain-classification.md) doc,
`warehouse-ops-agent` is **deliberately not classified** alongside the
fleet's Core/Supporting/Generic bounded contexts. It is a **read-side/
decision-support mechanism**: a CQRS-style read model that spans context
boundaries, plus a policy (tactical-pattern sense) layer — not a bounded
context of its own.

Consequences that matter for anyone touching this repo:

- **No `apis/openapi.yaml` or `apis/asyncapi.yaml`, and none is ever
  expected.** This repo produces no REST/event contract for other services
  to consume against a schema; its REST surface (`docs/docs/api-surface.md`)
  is small and documented by hand in prose, kept in sync with
  `internal/adapters/inbound/http` and `internal/adapters/inbound/mcp` by
  convention, not generation. If asked to "add OpenAPI docs" here, check
  first whether the ask actually belongs to one of the upstream
  bounded-context repos instead.
- **No cross-repo Go imports of any upstream context, ever.**
  Enforced (for the five original module paths) by `internal/architecture/architecture_test.go`'s
  `TestNoDirectDependencyOnBoundedContexts`, which fails the build the
  moment `go.mod`/`go.sum` reference any of:
  `github.com/claudioed/fulfillment-execution`,
  `github.com/claudioed/wes-work-planning`,
  `github.com/claudioed/workforce-management`,
  `github.com/claudioed/inventory-storage`,
  `github.com/claudioed/facility-layout`. Only their published MCP tool
  contracts (and, for console-bff, plain REST endpoints) are valid
  integration points.
- **Domain types are hand-mirrored, not imported.** Enums like
  `RebalanceAction`/`TaskType` in `internal/domain/policy` are local copies
  of the upstream vocabulary, validated at the MCP tool-call boundary
  rather than type-shared.
- **Zero write capability today (v1), CI-enforced.**
  `internal/architecture/zerowrite/zerowrite_test.go` statically scans
  `internal/adapters/outbound/{restclient,mcpclient}` for a mutating HTTP
  method literal and `internal/adapters/inbound/mcp/tools.go` for a tool
  registered without `ReadOnlyHint: true` — part of `make arch-test` /
  CI's `arch-test` job. It fails the build the moment either surface grows
  write capability, not just on code review. Every tool this agent's own MCP
  server exposes is `ReadOnlyHint: true`; there is no `AssignLabor`,
  `ReleaseNextWork`, or `RevokeReservation` method anywhere in this
  codebase to call even by mistake. See
  [governance-note.md](docs/docs/mcp/governance-note.md) for the future
  write-capable slice's two non-negotiable guardrails (an authorization
  gate + explicit human confirmation before any write executes).
- **Auth is currently fully removed, fleet-wide** (
  [ADR 0006](docs/docs/adr/0006-fleet-wide-auth-removal.md), superseding
  ADR 0005): this agent's inbound REST/MCP surfaces are unauthenticated,
  and its outbound MCP-client calls to the upstreams carry no bearer
  key. `OIDC-AUTH-SPEC.md` and the static-key/scope plumbing were deleted,
  not disabled — do not resurrect env vars like `OIDC_ISSUER_URL`,
  `MCP_READ_KEY`, `*_MCP_READ_KEY` from old code/docs you may see referenced
  in commit history; they are gone by design.

## Events: CloudEvents 1.0 is MANDATORY

This service **currently has no Kafka I/O at all** — it neither produces nor
consumes any Kafka message (no Kafka client in `go.mod`; it reads sibling
contexts only through their MCP/REST surfaces). The fleet rule still binds
any FUTURE Kafka integration added here: every message on integration
`warehouse.<ctx>.events` AND analytics `warehouse.<ctx>.analytics` topics is
a CloudEvents 1.0 event in structured content mode. This is a hard fleet
rule, not a preference:

- No flat envelope (`event_id`/`event_type`/`occurred_at`), no dual-write,
  no dual-read, no envelope toggle env var (`EVENT_ENVELOPE_MODE` is gone).
- Build/validate/(un)marshal with `github.com/cloudevents/sdk-go/v2/event`
  via `internal/adapters/kafka/cloudevents/` (copy the fleet reference
  helper from warehouse-harness-template's `templates/cloudevents/`);
  transport stays kafka-go.
- Kafka header `content-type: application/cloudevents+json; charset=UTF-8`.
- Required attributes: `specversion=1.0`, `id` (UUID, stable across outbox
  redelivery), `source=/warehouse/warehouse-ops-agent`, `type`, `subject`
  (aggregate id), `time` (occurred-at, UTC),
  `datacontenttype=application/json`,
  `dataschema=urn:warehouse:warehouse-ops-agent:<events|analytics>:<EventName>:v<N>`.
- `type` = `com.warehouse.<subdomain>.<bounded-context>.<entity>.<EventName>`;
  this service has no row in the fleet subdomain table yet — any first
  Kafka integration must add one (ADR) before publishing. Breaking payload
  change => new `.v2` type + new dataschema version, never mutate.
- Consumers dispatch on the FULL `type`, ignore unknown types, dedupe on
  `id`, and DLQ/skip (never crash, never parse a legacy shape) anything that
  fails CloudEvents validation. A consumer of a sibling's topic must use
  the exact `type` strings from the fleet's cross-service type catalogue.

Full standard and the fleet's cross-service type catalogue: warehouse-docs
`docs/strategic-design/event-standard-cloudevents.md` (each Kafka-using
service carries it as its "CloudEvents 1.0 as the mandatory event envelope"
ADR under `docs/docs/adr/`).

## Architecture

Hexagonal / Ports & Adapters, same shape as the sibling bounded-context
repos. Full package layout, the two-outbound-adapter-family split
(mcpclient vs restclient), and the ADR-0004 model-backed reasoner design
(LLM_MODE off/shadow/on, tool allowlist, fallback semantics):
`.claude/rules/architecture.md`.

Non-negotiable, CI-enforced boundary rules (no cross-context Go imports,
zero write capability in v1, hand-mirrored domain types, current auth
status): `.claude/rules/architecture-guardrails.md`.

## Key Commands

```bash
# Build / vet / format
go build ./...
go vet ./...
gofmt -w .                 # or: make fmt

# Run standalone, pointed at the fleet's MCP servers (ports = e2e-tests env.sh)
export FACILITY_LAYOUT_MCP_ENDPOINT=http://localhost:8091/mcp
export INVENTORY_STORAGE_MCP_ENDPOINT=http://localhost:8092/mcp
export WES_WORK_PLANNING_MCP_ENDPOINT=http://localhost:8093/mcp
export FULFILLMENT_EXECUTION_MCP_ENDPOINT=http://localhost:8094/mcp
export WORKFORCE_MANAGEMENT_MCP_ENDPOINT=http://localhost:8095/mcp
export AGENT_ADDR=:8096
go run ./cmd/agent

# Or run against the full fleet via the shared e2e harness
# (needs the warehouse-infra kind cluster up: Kafka is its shared broker at localhost:9092)
cd ~/warehouse-systems/e2e-tests
bash scripts/01-build.sh         # builds all binaries incl. this agent
bash scripts/02-up-infra.sh      # checks Kafka, starts the harness Postgres instances
bash scripts/03-up-services.sh   # starts every service + MCP server + this agent

# Hit the daily brief
curl -s http://localhost:8096/daily-brief | jq .

# Quality gate (mirrors CI)
make check       # fast pre-commit bundle: fmt-check vet build lint test
make check-all   # + coverage + arch-test (pre-push gate)
make mutation-fast  # gremlins unleash ./internal/domain (see .gremlins.yaml) — CI's blocking mutation job
make vuln           # govulncheck ./...
lefthook install # once, to activate pre-commit/pre-push git hooks

# Docs site (Docusaurus) — local dev
cd docs && npm install && npm start
```

### Configuration (env vars)

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

Plus `AGENT_ADDR` (default `:8095`), `PROMETHEUS_URL` / `LOKI_URL`
(runtime-signals sources; unset Prometheus → stub reader, unset Loki →
reported in `unavailableSources`), `RUNTIME_SIGNALS_NAMESPACE` (default
`warehouse-systems`), `RUNTIME_SIGNALS_SERVICES` (comma-separated, default
the eight backend contexts), `DAILY_BRIEF_PATH_TARGETS` (optional JSON
array overriding the process paths the daily brief monitors — defaults to
the single path the e2e-tests bootstrap scenario seeds), and the
console-bff's own separate REST base URLs (`ORDER_MANAGEMENT_REST_URL`,
`INVENTORY_STORAGE_REST_URL`, `WES_WORK_PLANNING_REST_URL`,
`FULFILLMENT_EXECUTION_REST_URL`, plus seven `*_REPORTS_REST_URL` vars for
the analytics dashboards) — see `internal/config/config.go` for every
default value.

LLM reasoner (ADR 0004): `LLM_MODE` (`off`/`shadow`/`on`, default `off`),
`ANTHROPIC_API_KEY` (required unless `off`; never logged),
`LLM_MODEL` (default `claude-sonnet-4-5`), `LLM_TIMEOUT` (default `8s`),
`LLM_BASE_URL` (tests/proxies), `LLM_TOOL_ALLOWLIST` (comma-separated
`<upstream>/<tool>`, defaults to the five read tools the deterministic path
already uses).

## Testing

```bash
go test ./... -race                                  # unit tests, no DB/live MCP
go test ./internal/architecture/... -v                # arch fitness tests
make coverage                                          # coverage run + 90% gate
```

- **Coverage gate is 90%**, scoped to
  `./internal/domain/...,./internal/application/...,./internal/adapters/inbound/...`
  (see `Makefile`'s `COVERPKG` / CI's `ci.yml` `test` job).
- **No database, no live MCP servers required for unit tests** — every use
  case is tested against fakes (`internal/application/usecases/fakes_test.go`).
- **The one legitimate env-gated test in this fleet**: an
  `-tags=integration` test may hit the real Anthropic API only when
  `ANTHROPIC_API_KEY` is set locally (per ADR 0004's Consequences — hosted
  models cannot use testcontainers). Every other integration-style test in
  this fleet must use testcontainers, never an env-var skip gate; this repo
  is the documented exception, not a precedent to copy elsewhere.
- **Architecture fitness tests** (`internal/architecture/architecture_test.go`)
  are the executable form of the "no upstream Go imports" and hexagonal
  dependency rules — run them (`make arch-test` / `go test
  ./internal/architecture/... -v`) after any adapter/import change, not
  just before push.
- To exercise this agent end-to-end against the real fleet, use the shared
  `e2e-tests` harness (see Key Commands above) rather than trying to stand
  up every upstream MCP server by hand.

## Docs site

This repo has its own Docusaurus site under `docs/` (business context, DDD
placement/subdomain-classification, context map, API surface, governance
note, every ADR). It publishes via `.github/workflows/docs.yml` to
`https://claudioed.github.io/warehouse-ops-agent/` on push to `main`
touching `docs/**`. `docs/docs/api-surface.md` is the hand-maintained
prose doc for this repo's REST + MCP surface — there is no generated
OpenAPI page because there is no `apis/openapi.yaml` (see "Role in the
fleet" above); if that page drifts from
`internal/adapters/inbound/{http,mcp}`, the code is authoritative, not the
doc.
