---
id: 0012-horizontal-autoscaling-single-deployment
title: "0012. HorizontalPodAutoscaler for the single Deployment, and switching the inbound MCP server to stateless mode"
sidebar_label: "0012 · HPA (single Deployment)"
description: "ADR 0012 — Phase 3 (scalability) for warehouse-ops-agent: one autoscaling/v2 HorizontalPodAutoscaler (min 1, max 3, target CPU 70%) for this repo's single Deployment, default-disabled via values.yaml; and a genuinely necessary companion fix, switching the inbound MCP StreamableHTTPHandler to Stateless mode, since this SDK's default stateful mode keeps an in-memory, per-process Mcp-Session-Id session map that would silently break the moment a second replica existed. No pgxpool dimension applies — this service has no database (ADR 0001/0004)."
---

# 0012. HorizontalPodAutoscaler for the single Deployment, and switching the inbound MCP server to stateless mode

## Status

Accepted — implemented in the same change that introduces this record.
This is Phase 3 (scalability) of the fleet production-readiness plan for
`warehouse-ops-agent`, following the SAME pattern shape
`order-management`'s [ADR 0026](https://claudioed.github.io/order-management/docs/adr/0026-horizontal-autoscaling-and-pgxpool-tuning)
established for that repo's Phase 3 — but scoped down to this repo's
actual architecture, not copied wholesale.

## Context

### Why this repo's Phase 3 scope is narrower than order-management's

`order-management`'s ADR 0026 did two things together: (1) a
per-workload-type `HorizontalPodAutoscaler` for four of its five
Deployments (`api`, `analytics-projector` capped lower, `analytics-reports`,
`frontend`), explicitly excluding `mcp` for a real in-memory-session
reason; and (2) `pgxpool.MaxConns`/`statement_timeout` tuning against a
shared Postgres instance's `max_connections=100` ceiling.

Neither half of that ADR transfers here unmodified:

- **No per-workload-type breakdown.** `warehouse-ops-agent` has exactly
  **one** Go binary (`cmd/agent`) and therefore exactly one Helm
  Deployment (`charts/warehouse-ops-agent/templates/deployment.yaml`) —
  confirmed by inspection, not assumed: there is no `projector`,
  `reports`, or separate `mcp` binary the way `order-management` has.
  This agent's own inbound MCP server (`/mcp`) and its REST/console-bff
  surface (`/`) are served off the **same** `http.Server`/`http.ServeMux`
  on the same `AGENT_ADDR` (`cmd/agent/main.go`'s `serveAgent`) — one
  process, one port, one Deployment, one HPA decision, not four.
- **No pgxpool dimension.** [ADR 0001](./0001-warehouse-ops-agent-placement.md)
  and [ADR 0004](./0004-llm-reasoner-behind-the-policy-layer.md) already
  establish that this repo "owns no aggregate... and persists no
  state — it holds no database." There is no `pgxpool.Config` anywhere
  in this codebase to tune, and no shared-Postgres connection ceiling to
  budget against. `CLAUDE.md`'s "No persisted state" line is the same
  fact stated for operators: restart this agent and it has forgotten
  nothing, because every fact it reasons over is re-derived from
  upstream MCP/REST/Prometheus/Loki reads at request time.

### Checking for in-memory state that would NOT be safe across replicas

Before concluding this service is horizontally scalable, its
composition root (`cmd/agent/main.go`, `cmd/agent/reasoner.go`) and every
adapter it wires were checked for process-local state that a second
replica would silently duplicate or fail to share:

| State found | Shape | Safe at N replicas? |
|---|---|---|
| `resilience.breaker.go`'s `gobreaker.CircuitBreaker` ([ADR 0011](./0011-reasoner-path-circuit-breaker-timeout-retry.md)) | One breaker instance per process, wrapping the outbound Anthropic call | Yes, with a caveat already documented in ADR-0011's own Consequences: "per-process, not shared across replicas... would need revisiting if this agent is ever horizontally scaled." At N replicas, the fleet's *effective* breaker becomes N independent breakers instead of one — a real, accepted degradation (see Consequences below), not a correctness bug: each replica still fails fast on its own view of the Anthropic dependency's health. |
| `mcpclient.ToolInvoker`'s `specs []ports.ToolSpec` behind a `sync.Mutex` (`internal/adapters/outbound/mcpclient/tool_invoker.go`) | Discovered once per process via `tools/list`, cached in memory, guarded by a mutex for concurrent access **within** that process | Yes — read-only, derived from a stateless upstream `tools/list` call, safe to duplicate independently per replica (each just discovers the same schemas once). |
| **Inbound MCP server's session state** — `github.com/modelcontextprotocol/go-sdk`'s `StreamableHTTPHandler` (`internal/adapters/inbound/mcp/server.go`) | Left at its **default** construction (`NewStreamableHTTPHandler(getServer, nil)`), the SDK runs in **stateful** mode: an in-memory `map[string]*sessionInfo` keyed by the `Mcp-Session-Id` header, created on `initialize` and looked up on every subsequent request (`streamable.go`'s `serveStateful`/`lookupSession`) | **No, not as shipped before this change** — this is exactly the risk `order-management`'s ADR-0026 named for excluding its own `mcp` Deployment from HPA: "a second request carrying the same `Mcp-Session-Id`... could land on a different pod than the one that created the session, which has never heard of it and would reject or silently start a new one." Unlike `order-management`, this agent has no separate `mcp` Deployment to simply exclude — its MCP server is fused into the ONE Deployment this HPA targets, so this had to be fixed, not sidestepped. |

Everything else in the composition root — the five outbound MCP-client
sessions, the REST clients, the telemetry/log readers, the
`toPathTaskTypes`/`toUseCaseTargets` config-mapping helpers — builds a
fresh set of stateless clients per process with no shared mutable state
beyond what's already covered above, and every use case
(`internal/application/usecases`) takes its dependencies as constructor
fields with no package-level `var` state (checked with a repo-wide grep
for `sync.`/package-level `map`/pointer literals — the only non-test
hits are the two rows above and `usecases/console_reports.go`'s
`sync.WaitGroup`, a per-call, per-goroutine-fan-out local, not
persisted across requests).

## Decision

### 1. Fix first: switch the inbound MCP server to `Stateless: true`

`internal/adapters/inbound/mcp/server.go`'s `Handler` now constructs the
`StreamableHTTPHandler` with `&mcp.StreamableHTTPOptions{Stateless: true}`
instead of `nil`. Per the SDK's own doc comment on `Stateless`: *"A
stateless server does not read or set the `Mcp-Session-Id` header, and
uses a temporary session with default initialization parameters for
each request... This mode aligns with the sessionless direction of the
MCP spec (SEP-2567)."* Every tool call becomes a self-contained POST
with no cross-request session lookup, so it is safe to land on any
replica.

This is the right fix, not a workaround, because every one of this
agent's five MCP tools (`get_daily_brief`, `list_open_exceptions`,
`get_flow_balance_exception`, `explain_travel_factor`,
`detect_stranded_reservation`) is already single-turn and read-only —
none of them depends on server-held state from a prior `initialize` or
a previous tool call in the same session, and none needs a
server-initiated push back to the client outside the lifetime of one
request. Verified this doesn't regress behavior:
`internal/adapters/inbound/mcp/server_test.go`'s existing over-the-wire
tests (`TestServer_ToolsListAndCall`,
`TestServer_GetFlowBalanceException_OverTheWire`, etc.), which connect a
real `sdk.Client` over `StreamableClientTransport` and exercise
`ListTools`/`CallTool`, all still pass unchanged — the SDK's client
transparently handles a session-less server on the wire; nothing in
this agent's own contract needed to change.

### 2. One `HorizontalPodAutoscaler`, default-disabled

`charts/warehouse-ops-agent/templates/hpa.yaml` targets the single
Deployment, gated by `.Values.autoscaling.enabled` (default `false` —
merging this PR changes nothing about production replica counts):

```yaml
autoscaling:
  enabled: false
  minReplicas: 1
  maxReplicas: 3
  targetCPUUtilizationPercentage: 70
```

**min 1 / max 3, not order-management's `api` min 1 / max 4.** This is a
deliberately smaller ceiling than the reference pattern's own busiest
workload: this agent is a decision-support aggregator behind an internal
console/MCP surface, not a customer-facing OLTP API — there is no
production traffic history yet to justify matching `api`'s ceiling, and
3 is enough headroom to validate the mechanism works before revisiting
the number with real data.

**CPU-only for v1, memory left as an easy follow-on, not a custom
metric yet.** The task's own framing correctly flags that this agent's
per-request cost is dominated by I/O wait — MCP/REST calls to five
upstream contexts, and (when `LLM_MODE` is not `off`) a network round
trip to the Anthropic API — not CPU-bound work. That is a real argument
for CPU being a *lagging* signal here: a pod can be saturated with
in-flight upstream calls well before its CPU utilization looks high.
Weighed against that: this repo has zero production traffic history
today (`replicaCount: 1` everywhere, HPA off), so there is no baseline
yet to derive a meaningful custom metric (e.g. in-flight-request count,
p99 latency) from — inventing a target number now would be a guess, not
a measurement. This follows the fleet plan's own explicit guidance
verbatim: *"CPU/memory initially, then a custom metric once a baseline
exists."* `values.yaml`'s `targetMemoryUtilizationPercentage` is wired
into the template (commented out, unset by default) as the cheapest
next lever — flip it on once real memory-pressure data justifies it —
deliberately not implemented as a custom
`external`/`pods` metric requiring a metrics-adapter dependency this
repo doesn't have yet.

**No replicas-vs-HPA fight**, same guard shape as `order-management`'s
ADR-0026: `templates/deployment.yaml`'s `spec.replicas` is wrapped in
`{{- if not .Values.autoscaling.enabled }}` (pre-existing in this chart,
unchanged by this PR) — when the HPA is enabled, the Deployment renders
with no `replicas:` field at all, so `helm upgrade` never resets a live
HPA's chosen replica count back to a static value. Verified directly
with `helm template`:

- Default values → 0 `HorizontalPodAutoscaler` resources render, the
  Deployment keeps `replicas: 1`.
- `--set autoscaling.enabled=true` → exactly 1 `HorizontalPodAutoscaler`
  renders (min 1, max 3, cpu target 70%), and the Deployment has **no**
  `replicas:` field.

`helm lint charts/warehouse-ops-agent` passes (one pre-existing
`icon is recommended` INFO, unrelated to this change).

## Consequences

**Positive**

- The single Deployment can now be scaled 1→3 replicas behind a flag
  with no code change and no risk of a replicas/HPA fight on
  `helm upgrade`.
- The stateless-MCP fix closes a real, previously-latent bug — it
  existed independently of this ADR (a single replica never exercises
  the multi-pod session-affinity gap), but this HPA is the first thing
  in this repo's history that would have exposed it in production the
  moment `autoscaling.enabled` was flipped on. Fixing it here, as part
  of making the HPA safe, avoids ever shipping "HPA that silently breaks
  MCP tool calls above 1 replica" as a combination.
- No `pgxpool`/database dimension to reason about, size, or test against
  a shared-instance ceiling — this repo's zero-write, no-database
  architecture ([ADR 0001](./0001-warehouse-ops-agent-placement.md),
  [ADR 0004](./0004-llm-reasoner-behind-the-policy-layer.md)) makes this
  Phase 3 PR meaningfully smaller in scope than every sibling repo's
  equivalent, by design, not by omission.

**Negative / accepted**

- The [ADR-0011](./0011-reasoner-path-circuit-breaker-timeout-retry.md)
  circuit breaker for the Anthropic dependency remains per-process. At
  `maxReplicas: 3`, the fleet's effective breaker for that ONE external,
  paid dependency becomes up to 3 independent breakers instead of one
  coordinated view — a replica whose breaker is OPEN still fails fast on
  its own traffic, but a fresh replica (or one whose breaker never
  tripped) will keep calling Anthropic during a sustained outage until
  its own `ConsecutiveFailures >= 5`/error-rate threshold trips
  independently. This was already the accepted caveat ADR-0011 itself
  named ("would need revisiting if this agent is ever horizontally
  scaled") — this ADR is the point where that caveat stops being
  hypothetical. Not fixed here: a shared breaker state store is a real
  design change (e.g. a distributed state backend) disproportionate to
  a min-1/max-3 v1 HPA with `autoscaling.enabled: false` by default; the
  fleet plan's own risk-based ordering treats this as revisit-when-real,
  not block-the-mechanism.
- CPU-only autoscaling can under-react to upstream-latency-driven
  saturation (many in-flight, low-CPU requests waiting on slow
  upstreams) — an accepted v1 gap per the plan's own staged guidance,
  not a blind spot anyone missed. `targetMemoryUtilizationPercentage` is
  wired as the next-cheapest lever; a genuinely I/O-aware custom metric
  is deferred until there is a real baseline to derive a sensible target
  from.
- `maxReplicas: 3` is a judgment call with zero production traffic
  history behind it, same honesty `order-management`'s ADR-0026 gave its
  own numbers — revisit once this agent has actually run multi-replica
  in front of real load.
- Stateless MCP mode means a client can no longer rely on server-held
  session context across calls, and server-initiated
  requests/notifications outside the lifetime of a single request are
  rejected (per the SDK's own `Stateless` doc comment). Verified this
  doesn't regress any of this agent's five tools today (all single-turn,
  read-only) — but it does foreclose ever adding a tool that depends on
  multi-call session state without first revisiting this decision.

## Alternatives considered

- **Copy order-management's per-workload-type HPA table verbatim.**
  Rejected: there is only one workload here. A per-workload table would
  be inventing structure this repo's architecture doesn't have, not
  reusing the pattern — the task's own framing states this explicitly,
  and inspection of `cmd/`/`charts/warehouse-ops-agent/templates/`
  confirms it (one `cmd/agent`, one `deployment.yaml`).
- **Leave the MCP server stateful and simply document the same
  caveat order-management's `mcp` Deployment carries, without fixing
  it.** Rejected: that caveat only worked for `order-management` because
  `mcp` there is a SEPARATE Deployment the HPA could just exclude.
  Here, MCP and REST share the ONE Deployment this HPA targets — leaving
  the SDK in stateful mode would mean turning on this very HPA could
  break `/mcp` the moment traffic landed on a second replica. Since
  every one of this agent's tools is already single-turn and read-only,
  fixing it (switching to the SDK's own `Stateless` mode) was strictly
  cheaper and more honest than documenting a foreclosure that didn't
  need to exist.
- **A custom/external metric (e.g. in-flight request count, p99
  latency) instead of CPU for v1.** Rejected for now, per the fleet
  plan's own staged guidance and the lack of a production baseline to
  derive a sensible target from — revisit once `autoscaling.enabled`
  has run for real and a genuine I/O-saturation signal is observable.
- **`maxReplicas` matching order-management's `api` ceiling of 4.**
  Rejected: no evidence this agent's traffic profile resembles that
  OLTP API's; 3 is a smaller, more conservative starting ceiling for a
  service with zero multi-replica history.
