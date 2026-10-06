---
id: 0008-labor-utilization-advisory-correlation
title: "0008 — Labor-utilization advisory correlation: correlating queue depth with observed idleness"
sidebar_label: "0008 · Utilization advisory correlation"
description: Consuming labor-performance's get_task_type_utilization tool to distinguish a claim/flow problem, starvation, and a corroborated staffing gap in FlowBalanceAdvisory, additively and with a deterministic fallback.
---

# ADR 0008: Labor-utilization advisory correlation

## Status

Accepted

**Date:** 2026-09-11

## Context

Phase 1 of the fleet's idleness/labor-utilization plan shipped
labor-performance's `get_task_type_utilization` MCP tool (PR #44, commit
`132f18b` on labor-performance's `develop`): given a task type and a
trailing window, it returns measured task time, measured idle time
(between-task waits), the still-running open gap for anyone currently
idle, and a derived `utilizationPct` that is `null` — never `0` — when
nothing was observed in the window.

`warehouse-ops-agent` already has a `LaborPerformanceClient` outbound port
(ADR 0007) wired into the composition root but consumed by no use case —
the "wired but unconsumed" precedent this repo has used since
`InventoryStorageClient`. Separately, `FlowBalanceAdvisory` (the E1
correlation use case) already gathers a queue-depth reading from
wes-work-planning's `get_rebalance_recommendation` (`BacklogDepth`) and a
staffing-gap reading from workforce-management, and correlates them via
`internal/domain/policy.Decide` into one of `assign_labor`,
`release_next_work`, or `hold`.

What `Decide` cannot yet tell apart is *why* a path's flow looks the way it
does: a deep queue with a confirmed staffing gap looks the same to that
correlation whether the path is genuinely understaffed or whether
associates assigned to it are sitting idle because of a claim/flow problem
(stuck tasks, lease churn) that assigning more heads would not fix. Labor
utilization is exactly the missing signal that discriminates those two
cases, and a third case besides: a *shallow* queue with idle associates,
which is neither a staffing problem nor a claim problem but starvation —
there simply isn't enough released work upstream.

## Decision

1. **Extend the outbound port and client, mirroring the existing three
   methods' shape exactly.** `ports.LaborPerformanceClient` gains
   `GetTaskTypeUtilization(ctx, taskType string, windowSeconds int64)
   (TaskTypeUtilization, error)`; `ports.TaskTypeUtilization` hand-mirrors
   labor-performance's `utilizationDTO` field-for-field, including
   `UtilizationPct *float64` as a pointer — this repo's hand-mirrored-
   domain-types discipline (no cross-repo Go import, ever) means the
   nullability must be preserved by hand, not coerced to a `float64`
   default. `mcpclient.LaborPerformance.GetTaskTypeUtilization` calls
   `get_task_type_utilization` via the same `c.session.callTool(...)`
   pattern the other three methods already use; `windowSeconds <= 0` is
   omitted from the call arguments so the tool applies its own default
   (1h), never a client-side guess.
2. **Add a small, pure correlation function to `internal/domain/policy`**
   (`utilization_correlation.go`, not folded into `flow_balance.go`, to
   keep this addition's diff isolated and reviewable on its own, mirroring
   this repo's existing convention of one file per correlation
   addition — see `dailybrief.go` alongside `flow_balance.go`).
   `CorrelateUtilization(queueDepth int, util *UtilizationSignal)
   *UtilizationCorrelation` is additive: it never changes
   `Decision.RecommendedAction`, `ProposedHeads`, or `Rationale` — it only
   ever sets the new `Decision.Utilization` field, which is `nil` unless
   both a queue-depth reading and a non-null `UtilizationPct` are
   available and their combination clearly matches one of three named
   outcomes:
   - **queue depth HIGH + idle share HIGH → `claim_flow_problem`**: work
     is available (deep queue) but labor-performance measures low
     utilization — this points at a claim/flow problem in
     fulfillment-execution (stuck tasks, lease churn), explicitly *not* a
     staffing recommendation, and the rationale text says so.
   - **queue depth LOW + idle share HIGH → `starvation`**: idle
     associates with nothing available to claim. The rationale
     recommends WES release pacing / upstream attention as *advisory
     text only* — this agent has zero write capability in v1 and this
     repo's fleet-wide design deliberately does **not** call any WES
     action tool to auto-trigger release pacing (see Alternatives below).
   - **queue depth HIGH + idle share LOW → `staffing_gap_confirmed`**:
     the existing `Decide()` staffing-gap recommendation (when reached)
     is now explicitly corroborated in prose by the observed utilization
     percentage (e.g. "staffing gap confirmed by observed utilization of
     N%"), rather than the two signals sitting side by side unremarked.
   - Every other combination (in particular a shallow queue with healthy
     utilization — ordinary operating noise) returns `nil`: no exception
     is invented where the evidence does not support one.

   **Thresholds** (documented here, not derived from a live percentile —
   this repo has no telemetry-backed per-path baseline today):
   `UtilizationQueueDepthHighThreshold = 50` (the same order of magnitude
   already used as "worth flagging" backlog depth in this package's
   existing daily-brief correlation), `UtilizationLowPctThreshold = 60.0`
   (below 60% busy time, more than two-fifths of the window was idle —
   past ordinary between-task noise), and
   `UtilizationHighIdleShareThreshold = 0.40`, independently computed as
   `idleSeconds / (taskSeconds + idleSeconds)` from the tool's raw
   seconds. **Both** the tool's own derived `utilizationPct` *and* this
   independently-computed idle share must agree before a "problem"
   outcome fires — a single noisy metric alone never triggers an
   advisory, which is intentionally more conservative than either metric
   read in isolation.
3. **Correlation branches are written as `if`/`else if`/`return`, never a
   bare `switch { case boolExpr: }`** — this fleet's known gremlins
   mutation-testing gap (an expressionless boolean switch can read 100%
   line-covered while mutation-uncovered).
4. **Wire `LP` and a `PathTaskTypes` map into `FlowBalanceAdvisory`.**
   `PathTaskTypes` reuses the *same* `pathId -> processPath` binding
   `DailyBrief` already consumes from `config.PathTarget.ProcessPath` (via
   `cmd/agent`'s existing `toUseCaseTargets`) — a new `toPathTaskTypes`
   mapping function in the composition root, not a second, independently
   invented resolution mechanism. `Execute` gathers the utilization signal
   only when a queue-depth reading (`wesSignal`) exists (the same
   anchor-signal reasoning `Decide()` already applies) and the path has a
   task-type binding; a missing binding, a nil `LP` client, an unreachable
   call, or a `null` `UtilizationPct` all degrade identically to a `nil`
   `Decision.Utilization` with the pre-existing `RecommendedAction`/
   `Rationale` completely unchanged (ADR-0004's stated discipline: MCP
   tools are the only actuators, a policy layer decides, with a
   deterministic fallback when a signal/tool is unavailable). When a
   utilization reading *is* available, it is also appended to
   `Decision.Evidence` (source `labor-performance.get_task_type_utilization`),
   with a `null`-rendering helper so an absent percentage never prints as
   a fabricated `0.0`.
5. **This is the moment `LaborPerformanceClient` graduates from "wired but
   unconsumed."** `cmd/agent/main.go`'s `_ = lp` line is removed; `lp` is
   now passed into `FlowBalanceAdvisory.LP`.
6. **Zero write capability preserved.** Nothing in this change calls a
   write tool on any upstream, including WES — the starvation outcome is
   advisory prose surfaced through the existing read-only
   `GET /flow-balance/{pathId}` REST response and MCP tool, never an
   automated release-pacing call.

## Consequences

**Positive** — an operator (or the ADR-0004 reasoner, via its existing
facts map, left unextended in this change — see Alternatives) reading a
flow-balance exception can now tell a genuine staffing gap apart from a
claim/flow problem that assigning labor would not fix, and gets an
explicit starvation signal instead of a silent "nothing to see here."
`LaborPerformanceClient` stops being dead code in every use case that
matters.

**Negative / accepted** — the correlation's thresholds
(`UtilizationQueueDepthHighThreshold`, `UtilizationLowPctThreshold`,
`UtilizationHighIdleShareThreshold`) are judgment calls, not derived from
live fleet telemetry; they may need tuning once real utilization data
exists in the cluster. `PathTaskTypes` requires the same deployment-time
configuration correctness `DailyBrief` already depends on — an
operator who adds a new monitored path to `DAILY_BRIEF_PATH_TARGETS`
without a correct `processPath` silently gets no utilization correlation
for that path, exactly like `DailyBrief`'s existing degrade-to-partial
behavior.

**No behavior change to the base decision** — `Decision.RecommendedAction`,
`ProposedHeads`, and `Rationale` are byte-for-byte identical before and
after this change for every existing `TestDecide` case; the utilization
overlay only ever adds the new `Utilization` field and (when available) an
extra evidence entry.

## Alternatives considered

- **Fold the utilization overlay into `Decide()` itself, replacing/
  changing `RecommendedAction`.** Rejected: `Decide()`'s existing action
  vocabulary (`assign_labor`, `release_next_work`, `hold`) is the set of
  levers this agent's write-capable future slice could eventually pull;
  the three utilization outcomes here are diagnostic/advisory prose, not
  a fourth lever. Keeping them additive and separate (`Decision.Utilization`)
  avoids conflating "what to do" with "why the situation looks the way it
  does," and keeps every existing `TestDecide` case's expectations
  unchanged.
- **Auto-trigger WES release pacing on a starvation outcome.** Rejected,
  per the fleet plan's explicit statement: "WES release-pacing reaction to
  starvation is a DELIBERATE non-integration for now (documented, not
  built)." This agent has zero write capability in v1 (every tool is
  `ReadOnlyHint: true`); building a release-pacing trigger here would
  violate that guardrail for a benefit ADR 0004's policy-layer-decides
  discipline says should wait for a properly gated write-capable slice,
  not be smuggled in through an advisory correlation.
  and
- **Require only one of `utilizationPct` or the idle-share ratio to be
  low/high, rather than both.** Rejected: `utilizationPct` and the
  independently-computed idle share are close to inverses of each other
  by construction, so requiring both to agree is cheap insurance against
  one metric reading noisy in a way the other does not corroborate,
  before naming a specific operational outcome an operator will act on.
- **Extend `flowBalanceFacts`/the ADR-0004 reasoner's tool allow-list to
  include `get_task_type_utilization`.** Deferred, not rejected: today's
  reasoner allow-list and brief-building code are scoped to the three
  signals `Decide()` itself consumes (wes/wfm/fe); wiring a fourth tool
  through the LLM-facing path is a larger, separately-reviewable change
  than this correlation-only slice, and the deterministic
  `Decision.Utilization` field is already visible to every consumer of
  `FlowBalanceAdvisory.Execute` (REST, this agent's own MCP tool) without
  it.
