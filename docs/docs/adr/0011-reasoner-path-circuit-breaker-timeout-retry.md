---
id: 0011-reasoner-path-circuit-breaker-timeout-retry
title: "0011. Reasoner-path resilience: circuit breaker, deadline-derived timeout, bounded retry"
sidebar_label: "0011 · Reasoner resilience"
description: Wrapping the outbound Anthropic Messages API call behind a per-dependency circuit breaker, a context-deadline-derived timeout, and a bounded jittered retry, falling back to ADR-0004's existing deterministic path on breaker OPEN.
---

# 0011. Reasoner-path resilience: circuit breaker, deadline-derived timeout, bounded retry

## Status

Accepted

## Context

[ADR 0004](./0004-llm-reasoner-behind-the-policy-layer.md) wired a real,
hosted Anthropic model behind the deterministic policy layer, and its
"Negative / accepted" section already names the consequence: *"A hosted-
model dependency on the request path of `/flow-balance` and
`/daily-brief`. Mitigated by the timeout + fallback."* That mitigation
existed only as a per-call `context.WithTimeout` inside `Reason` and
`policy.Arbitrate`'s existing "any Reasoner error is `planErr`, `LLMOn`
mode falls back to the deterministic Decision" logic — there was no
circuit breaker, no bounded retry, and no `circuit_breaker_state` gauge.
A sustained Anthropic outage (a real 5xx storm, not a single blip) would
retry-storm the API on every request that reached `LLMOn` mode, paying
the full per-call timeout on every single request instead of failing
fast once the dependency was known to be down — exactly the gap the
fleet-wide production-readiness plan's section 2.6 calls out: *"ops-
agent's LLM-provider call is itself an external dependency... wrap the
provider call in the SAME breaker+timeout+retry pattern as [cross-context
sync calls], with the existing deterministic fallback as the OPEN-state
behavior — this formalizes what ADR-0004 already conceptually requires
but doesn't yet implement as breaker+metrics."*

`order-management`'s [ADR-0025](https://claudioed.github.io/order-management/docs/adr/0025-resilience-circuit-breakers-retry-dlq-shutdown)
solved the equivalent problem for that service's own outbound
dependencies (`inventory-storage`, `product-classification`) with a
shared `internal/resilience` package (a trip-condition helper, a
deadline-propagation helper, and a `StateRecorder` interface for the
Prometheus gauge) plus a per-dependency `BreakerClient` wrapping each
outbound client. This ADR reuses that SAME shape — not the same Go
package. This repo never imports another bounded context's or another
repo's Go packages (`internal/architecture`'s
`TestNoDirectDependencyOnBoundedContexts`, and more generally the
fleet's bounded-context isolation convention); `internal/resilience` here
is a fresh, repo-local package with the identical `ReadyToTrip`/
`CallTimeout`/`StateRecorder`/`RecordStateChange` API, ported by hand.

Two things distinguish this dependency from `order-management`'s REST
calls to sibling contexts, and shape the numbers chosen below:

1. **It is the only outbound dependency this repo has that costs real
   money per call and does real inference work**, not a cache/DB-backed
   REST lookup. A blind high-attempt retry policy would both waste spend
   and risk starving the per-request timeout budget the deterministic
   path needs to still answer promptly.
2. **A working answer from a healthy Anthropic API is not instant** the
   way a REST call to a sibling bounded context is — `Reason`'s own
   pre-existing default end-to-end timeout is already 8s for a
   multi-turn tool-use loop. A single HTTP attempt inside that loop
   therefore needs a per-call budget longer than a typical REST call's
   timeout would use, or a legitimate slow-but-healthy response would be
   killed by the resilience wrapper before the loop's own timeout ever
   gets a chance to.

## Decision

### 1. A repo-local `internal/resilience` package, same shape as ADR-0025's

`internal/resilience/breaker.go` carries `ReadyToTrip` (open on
`ConsecutiveFailures >= 5` OR, once at least 10 requests have been seen
in the current window, `TotalFailures/Requests > 0.5`), `StateRecorder`
(the Prometheus-gauge wiring contract), and `RecordStateChange` (adapts a
`StateRecorder` into `gobreaker.Settings.OnStateChange`, with a nil
recorder as a documented no-op). `internal/resilience/deadline.go` carries
`CallTimeout(ctx, maxPerCall)`: if the inbound context's own deadline is
already at or inside `maxPerCall`, it is honored unchanged; otherwise the
call is capped at `maxPerCall` from now. Neither file has any dependency
on `order-management`'s module — both were written directly from reading
that repo's `internal/resilience/{breaker,deadline}.go` as a reference
pattern, not imported.

### 2. The circuit breaker wraps the outbound HTTP call, not the whole `Reason` loop

`internal/adapters/outbound/llm/anthropic`'s `Reasoner.call` (one
Anthropic Messages API request) is wrapped in a
`gobreaker.CircuitBreaker[*apiResponse]`, constructed once per
`Reasoner` (one breaker per process, since this adapter has exactly one
outbound dependency), settings:

```go
gobreaker.Settings{
    Name:        "anthropic-llm",
    MaxRequests: resilience.DefaultMaxRequests, // 1 half-open probe
    Interval:    resilience.DefaultInterval,    // 30s closed-state rolling window
    Timeout:     resilience.DefaultCooldown,    // 30s OPEN cooldown before a probe
    ReadyToTrip: resilience.ReadyToTrip,
    IsExcluded:  func(err error) bool { return errors.Is(err, context.Canceled) },
    OnStateChange: resilience.RecordStateChange("anthropic-llm", recorder),
}
```

A multi-turn `Reason` call therefore contributes ONE breaker
success/failure per turn (one `call` per turn), never per retry attempt
inside that turn — a bounded retry storm on a single turn is not treated
as N separate failures.

`context.Canceled` is excluded from counting either way (the caller
giving up is not the dependency's fault), mirroring
`inventorystorage.BreakerClient`'s identical exclusion in
order-management.

### 3. Timeout: capped at 12s per call, derived from the inbound deadline

`call` wraps its context with `resilience.CallTimeout(ctx, 12*time.Second)`
before executing. 12 seconds is deliberately longer than the
fleet's typical cross-context REST-call timeout (a `sync_edge_env` HTTP
check or an MCP tool call is usually sub-second to a few seconds) — an
LLM inference turn is genuinely slower work, and undercutting it would
turn a healthy-but-slow model response into a manufactured failure. It
is still well inside `Reason`'s own outer per-request timeout (default
8s per `Reason` call, configurable via `LLM_TIMEOUT`) for the common
case, and in production the caller's own inbound-request deadline
(propagated from the HTTP handler) is almost always the binding,
tighter constraint — the 12s figure only matters when no tighter
deadline exists (a background job, or a test with a bare
`context.Background()`).

### 4. Retry: `cenkalti/backoff/v4`, max 3 attempts, transient errors only

`retryingRequest` retries `doRequest` with `backoff.NewExponentialBackOff`
(100ms initial, 1s max interval, full jitter — the library's default),
bounded to `maxRetryAttempts = 3` total attempts (1 original + 2
retries) and to the same per-call deadline from step 3. Only a
*transient* error is retried:

- a transport-level failure (dial error, timeout, connection reset —
  wrapped as `*transientError`), or
- a 5xx HTTP status from the Anthropic API (wrapped as
  `*apiStatusError` carrying the real status code).

A 4xx status (bad API key, malformed request — permanent, retrying can
never fix it) or a response-decode failure returns via
`backoff.Permanent`, so it consumes exactly one attempt, not three. This
mirrors `productclassification.BreakerClient`'s identical
transient-vs-permanent split in order-management, adapted to HTTP status
codes instead of that adapter's own error taxonomy.

### 5. Breaker OPEN falls back through the EXISTING mechanism — nothing new is invented

A breaker rejection (`gobreaker.ErrOpenState` or
`gobreaker.ErrTooManyRequests`, checked via `errors.Is`) is returned from
`call` as an ordinary `error`, wrapped with the dependency name for
diagnostics. `Reason` propagates it unchanged, exactly like any other
Reasoner failure (a real transport error, an exhausted retry, a timeout).
`usecases.FlowBalanceAdvisory.arbitrate` already treats any `Reasoner`
error as `policy.Arbitrate`'s `planErr` argument, and `Arbitrate` already
routes `LLMOn` mode to the deterministic `Decision` with
`Source=fallback` on any `planErr` — this is the exact mechanism ADR-0004
described as "Mitigated by the timeout + fallback" but had never
exercised under a genuinely tripped breaker. No new fallback path, no new
`Decision` shape, no new field: a breaker-OPEN request produces a
byte-identical `Decision` to a plain Reasoner-unavailable request today.
Verified in
`internal/application/usecases/end_to_end_resilience_test.go`
(`TestFlowBalanceAdvisory_BreakerOpen_FallsBackToDeterministic`): the
decision returned once the breaker is OPEN is asserted equal, field by
field, to the decision `LLMOff` mode produces with no Reasoner at all.

### 6. Prometheus gauge: `circuit_breaker_state{dependency="anthropic-llm"}`

`internal/adapters/outbound/telemetry.CircuitBreakerMetrics` implements
`resilience.StateRecorder` against the SAME global `MeterProvider`
`ArbitrationMetrics` already installs — one OTel instrument,
`circuit_breaker.state` (Int64Gauge, 0=closed/1=half-open/2=open), which
the OTel Collector's Prometheus exporter republishes as
`circuit_breaker_state`. `cmd/agent/reasoner.go`'s `wireReasoner`
constructs it and passes it as `anthropic.Config.BreakerRecorder`; a
registration failure is logged and downgrades to a nil recorder
(`resilience.RecordStateChange`'s documented no-op) rather than blocking
startup — mirrors `NewArbitrationMetrics`' existing error contract in
this same composition root.

## Consequences

**Positive**

- A sustained Anthropic outage now fails fast (breaker OPEN,
  effectively zero latency, zero spend on the tripped dependency) instead
  of retry-storming a dead API on every `LLMOn`-mode request.
- The fallback ADR-0004 already specified is now provably exercised
  under a real tripped-breaker condition, not just a single simulated
  transport error — closing the exact gap the plan's section 2.6 named.
- `circuit_breaker_state{dependency="anthropic-llm"}` gives an operator
  the same at-a-glance signal order-management's dashboard already has
  for its own breakers, extending the fleet's resilience observability
  convention to this repo's one external (non-MCP, non-sibling-context)
  dependency.

**Negative / accepted**

- 12s is a judgment call, not a measured p99 — there is no production
  load history for this endpoint yet. If real latency data later shows
  it too generous (masking a slow-but-technically-successful call as
  acceptable) or too tight (tripping on legitimate slow turns), revisit
  the constant; it is a single named constant (`maxCallTimeout`) for
  exactly this reason.
- The breaker is per-process, not shared across replicas (this service
  runs as a single Deployment today, so this is currently equivalent to
  a fleet-wide breaker, but would need revisiting if this agent is ever
  horizontally scaled — same caveat every other per-dependency breaker
  in this fleet already carries).
- Retrying a 5xx up to 3 times still triples worst-case latency and cost
  for a persistently-flaky-but-not-yet-tripped dependency, before the
  breaker's own `ConsecutiveFailures >= 5` threshold engages. Accepted as
  the same trade-off `order-management`'s `productclassification`
  breaker already makes for the fleet's one other retried GET-shaped
  dependency.

## Alternatives considered

- **Import `order-management`'s `internal/resilience` package directly.**
  Rejected: violates this fleet's bounded-context isolation — no repo
  imports another repo's Go packages, full stop, enforced here by
  `internal/architecture`'s dependency-rule tests for the five sibling
  bounded contexts and, more generally, by convention for every other
  repo in the org. A fresh, hand-ported package with the identical shape
  gets the proven design without the cross-repo coupling.
- **Wrap the whole `Reason` tool-use loop in the breaker instead of just
  the HTTP call.** Rejected: a multi-turn conversation legitimately
  makes several MCP tool calls (already resilient via each upstream
  context's own MODE fallback) interleaved with several Anthropic API
  calls; breaking on the *outer* loop would conflate an MCP tool
  failure (a different dependency, already handled) with an Anthropic
  API failure, and would make ONE multi-turn `Reason` call count as one
  breaker sample regardless of how many actual API calls it made.
  Wrapping `call` instead keeps the breaker's Counts an honest measure
  of the ONE dependency it is named after.
- **A higher retry ceiling (5+ attempts).** Rejected per the plan's own
  explicit guidance ("LLM calls are not free so don't over-retry") — 3
  total attempts matches the fleet's existing retried-GET precedent
  (`productclassification.BreakerClient`) and keeps worst-case added
  latency/cost bounded to roughly 3x a single attempt, not more.
