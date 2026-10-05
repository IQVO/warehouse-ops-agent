---
id: 0004-llm-reasoner-behind-the-policy-layer
title: "0004 — A real LLM reasoner behind the policy layer, with MCP tools as its only actuators"
sidebar_label: "0004 · LLM reasoner behind policy"
description: Why this agent gains a model-backed Reasoner port that proposes plans from the same facts the deterministic policy layer already gathers, why the model can only act through the fleet's published MCP tools, and why the deterministic rules remain the fallback and the arbiter.
---

# ADR 0004: A real LLM reasoner behind the policy layer, with MCP tools as its only actuators

## Status

Accepted — implemented for the flow-balance use case (PR #38, 2026-09-07; tool-call safety hardened in a later ADR-conformance pass — schema validation, enum re-check, `llm.tool_call` span and args-hash audit logging). DailyBrief (E3) reasoner coverage is a documented next phase, not yet implemented.

**Date:** 2026-09-07

## Context

The fleet's ecosystem assessment (2026-09-07) found that this service,
positioned in every document as the fleet's *agentic* surface, contains no
model in the loop: `internal/domain/policy` is a deterministic rules engine
(`policy.Decide`, `policy.DailyBrief`, `policy.StrandedReservation`) over
facts read from five bounded contexts' MCP tools. The seven MCP servers,
their curated intent-level tools, the governance charter and ADR-0008 in
each context were built for an AI consumer that does not yet exist. The
owner's decision: **wire a real LLM behind the policy layer, MCP tools as
its only actuators, deterministic rules kept as fallback.**

Two constraints from ADR 0001 are non-negotiable and shape the design:

1. This service owns no aggregate and enforces no new invariant. A model
   must not become a back door for either.
2. Every tool argument the agent accepts is untrusted input; unknown enum
   values are rejected, never defaulted. A model's output is *more*
   untrusted than a human operator's, not less.

## Decision

### 1. A `Reasoner` driven port, not a new domain concept

```go
// internal/ports/reasoner.go
type Reasoner interface {
    // Reason proposes a Plan for the situation described by Brief. It may
    // call the read tools listed in Brief.Tools to gather more facts.
    Reason(ctx context.Context, brief Brief) (Plan, error)
}
```

- `Brief` is assembled by the existing use case (`FlowBalanceAdvisory`,
  `DailyBrief`, …) from the signals it already gathers, plus the tool
  catalogue it is allowed to expose. It is a DTO in `ports`, not a domain
  type.
- `Plan` is a validated, **enum-typed** proposal: `RecommendedAction` uses
  the policy package's existing `ActionAssignLabor` /
  `ActionReleaseNextWork` / `FlowBalanceActionHold` vocabulary,
  `ProposedHeads` is bounded, `Rationale` is free text, `Evidence` lists
  the tool calls the model actually made. Anything outside the vocabulary
  fails `policy.ValidatePlan` and the whole plan is discarded.
- The domain `policy` package does not import the port. It gains one pure
  function, `policy.Arbitrate(det Decision, plan *PlanProposal, planErr error, mode LLMMode) Arbitration`,
  which is the only place the two sources meet. `Arbitration.Decision`
  carries a `Source` field (`deterministic` / `llm` / `fallback`) that the
  application layer copies onto the returned `Decision.Source` — surfaced
  on both the REST (`GET /flow-balance/{pathId}`) and MCP
  (`get_flow_balance_exception`) responses.

### 2. The model can only act through MCP

The Anthropic adapter (`internal/adapters/outbound/llm/anthropic`) uses
the Messages API with **tool use**. The tools it offers the model are
generated 1:1 from the `mcpclient` sessions this service already holds —
`get_rebalance_recommendation`, `get_staffing_gap`,
`diagnose_stuck_tasks`, `check_availability`, `get_site_layout`, … all
read tools; the model has no write tools today (see "Negative / accepted"
below). The model never sees an HTTP client, a database, or a shell.
Every tool invocation:

- is restricted to the explicit `LLM_TOOL_ALLOWLIST` (env var, default:
  exactly the read tools `FlowBalanceAdvisory` already consults —
  `wes-work-planning/get_backlog_telemetry`,
  `wes-work-planning/get_rebalance_recommendation`,
  `workforce-management/get_staffing_gap`,
  `fulfillment-execution/get_queue_status`,
  `fulfillment-execution/diagnose_stuck_tasks` — so shadow mode compares
  like with like) — never a bearer-scope distinction, since REST and MCP
  are unauthenticated fleet-wide (ADR 0006);
- is validated against the tool's JSON schema *before* the MCP call and
  its enum-constrained properties re-checked *again*, independently, by a
  small targeted pass — the same "reject, never default" rule already
  applied to human input;
- emits one structured audit log line (`llm.tool_call` with tool, a
  sha256 hash of the canonical JSON-encoded args — never the raw args —
  latency, outcome) and one OTel span (`llm.tool_call`) under the
  request's trace.

### 3. The deterministic policy remains the arbiter

`LLM_MODE` selects how `policy.Arbitrate` combines the two:

| mode     | behaviour |
|----------|-----------|
| `off`    | Reasoner never called; today's behaviour, byte-for-byte. Default. |
| `shadow` | Reasoner called with a timeout; its Plan is logged and compared to the deterministic Decision (`ops_agent_llm_agreement_total{use_case,mode,agree}` counter; every arbitration also increments `ops_agent_llm_arbitrations_total{use_case,mode,source}`). The deterministic Decision is returned. |
| `on`     | If the Plan validates and arrived within `LLM_TIMEOUT` (default 8s), it is returned with `Source=llm`; otherwise the deterministic Decision is returned with `Source=fallback` and the reason logged. A Plan may never widen the action vocabulary or exceed `ProposedHeads` bounds. |

`shadow` is the rollout gate: the cluster runs it until the agreement
metric and the logged disagreements have been reviewed, then flips to
`on`. Rollback is `LLM_MODE=off`.

### 4. Configuration and secrets

`ANTHROPIC_API_KEY` (Kubernetes Secret, never in Terraform state or git —
supplied via `TF_VAR_anthropic_api_key` / a git-ignored tfvars),
`LLM_MODEL` (default `claude-sonnet-4-5`), `LLM_MODE`, `LLM_TIMEOUT`,
`LLM_TOOL_ALLOWLIST` (comma-separated `upstream/tool` entries; defaults to
the read-only set listed in §2 above — there is no write-tool allow-list,
since the model has no write tools). A missing key with `LLM_MODE≠off`
fails startup loudly rather than silently degrading to `off`.

## Consequences

**Positive**
- The seven MCP servers and their governance finally have the consumer
  they were designed for, and the "agentic" claim becomes true.
- The blast radius is bounded by construction: the model's *only* effects
  are MCP tool calls, each authenticated, scoped, schema-validated, and
  audited — the same contract a human operator's REST call is held to.
- The deterministic policy is not discarded; it becomes the safety net
  and the benchmark. Disagreements are data.

**Negative / accepted**
- A hosted-model dependency on the request path of `/flow-balance` and
  `/daily-brief`. Mitigated by the timeout + fallback; those endpoints
  are decision-support, not operational hot paths.
- Cost per call. `shadow` doubles inference cost during the rollout
  window; acceptable for a study project, tracked via the
  `ops_agent_llm_arbitrations_total` counter.
- Testing a hosted model cannot use testcontainers. Unit tests use a
  fake `Reasoner` and recorded API responses (no network in CI); a
  single `-tags=integration` test hits the real API **only** when
  `ANTHROPIC_API_KEY` is set locally. This is the one legitimate use of
  an env gate in the fleet and is documented as such.

## Alternatives considered

- **Replace the policy layer with the model.** Rejected: violates ADR 0001
  (the model would be the de-facto invariant holder), loses the benchmark,
  and makes rollback a rewrite.
- **Let the model call REST directly.** Rejected: bypasses the curated,
  scoped, intent-level MCP surface the fleet built precisely so an AI
  consumer cannot reach raw CRUD.
- **A separate "agent" service.** Rejected: this service already *is* the
  Customer of the five OHS contexts (ADR 0001); a second one would
  duplicate every MCP client and key.
- **Local model (Ollama).** Deferred: the adapter is behind a port, so a
  second implementation is additive. Hosted first, for tool-use quality.

## Prerequisite (historical — resolved)

As of this ADR's authoring date **no MCP server was deployed in the
cluster**: no context's Dockerfile built `cmd/mcp`, no chart had an MCP
deployment, and this service's `*_MCP_ENDPOINT` values were all empty.
That prerequisite batch (Dockerfile+chart PRs for FE, WES, INV, WFM, FL,
plus the warehouse-infra endpoint/key wiring) has since landed: every one
of those contexts now ships `cmd/mcp`, and this service holds nine
outbound MCP-client adapters (`internal/config/config.go`'s
`*_MCP_ENDPOINT` list), not five. The Reasoner's tool catalogue is
therefore real today, gated only by `LLM_MODE` and `LLM_TOOL_ALLOWLIST`,
not by missing upstream servers.
