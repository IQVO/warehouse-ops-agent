---
id: 0015-core-flow-balance-and-daily-brief-adoption
title: "0015 — Adoption record: E1 flow-balance correlation and E3 daily brief (core use cases)"
sidebar_label: "0015 · E1/E3 core adoption"
description: An after-the-fact adoption ADR for this agent's two foundational read-side use cases, FlowBalanceAdvisory (E1) and DailyBrief (E3), which predate the ADR series entirely and have accumulated extensions (ADR 0004, 0008, 0013) without ever getting a record of their own original design.
---

# ADR 0015: Adoption record — E1 flow-balance correlation and E3 daily brief

## Status

Accepted (adoption record — both use cases predate this repo's ADR series;
see the 2026-10 fleet ADR-conformance audit, which found the core E1/E3
design undocumented even though six later ADRs — 0004, 0007, 0008, 0009,
0012, 0013 — all extend or depend on it).

## Context

`internal/application/usecases/dailybrief.go` (E3) and
`flow_balance_advisory.go` (E1) are this agent's two foundational use
cases — the reason `warehouse-ops-agent` exists at all per
[ADR 0001](./0001-warehouse-ops-agent-placement.md)'s placement decision
— yet neither has its own design record. Every later ADR in this series
assumes their shape without ever stating it:

- ADR 0004 (LLM reasoner) arbitrates `FlowBalanceAdvisory`'s deterministic
  `Decision` against a model `Plan`.
- ADR 0007 (second-wave MCP clients) and ADR 0008 (labor-utilization
  correlation) extend the signals `FlowBalanceAdvisory` gathers.
- ADR 0009 (`explain_travel_factor`) is explicitly scoped as a *separate*
  use case specifically because folding it into `FlowBalanceAdvisory`
  was rejected — a decision that only makes sense with `FlowBalanceAdvisory`'s
  original shape as context.
- ADR 0013 (warehouse-planning capacity outlook) adds an optional section
  to `DailyBrief`'s per-path output.

This record closes that gap retroactively, documenting the design as
built and as it stands today (after the extensions above), not a
historical snapshot of the original PR.

## Decision

Document both as accepted, as-built, with no behavior change from this
record.

### E3 — `DailyBrief`

**Purpose:** per configured `PathTarget` (a process path bound to a site,
building, and shift — deployment config, `internal/config`), gather
backlog telemetry (wes-work-planning), staffing gap
(workforce-management), queue status and stuck-task diagnostics
(fulfillment-execution), correlate via `internal/domain/policy`, and
assemble the result grouped by facility-layout site. Exposed as
`GET /daily-brief` and the `get_daily_brief`/`list_open_exceptions` MCP
tools.

**Guardrails (unchanged by any later ADR):**
- Never calls a write tool; never mutates upstream state.
- A missing/erroring upstream signal degrades that path's brief entry
  (an `Unavailable` list of sources) rather than failing the whole
  report — one unreachable context never blanks out every other site's
  brief.
- `ProcessPath`/path-identity binding across contexts is explicit
  deployment config (`PathTarget`), never inferred or fuzzy-matched
  across each context's own differing naming for "the same" path.

### E1 — `FlowBalanceAdvisory`

**Purpose:** given one process path (scoped to a building/shift), gather
wes-work-planning's rebalance recommendation, workforce-management's
staffing gap, and fulfillment-execution's stuck-task count, and correlate
them via `internal/domain/policy.Decide` into one ranked
`FlowBalanceException` recommendation (`assign_labor` / 
`release_next_work` / `hold`) plus a full evidence trail. Exposed as
`GET /flow-balance/{pathId}` and the `get_flow_balance_exception` MCP
tool.

**Guardrails (unchanged by any later ADR):**
- A missing/erroring signal degrades the `Decision` to `Partial: true`
  with `MissingSignals` naming what was unavailable, never a hard
  failure or a guessed-at recommendation.
- An unrecognized `RebalanceAction` enum value from wes-work-planning is
  the one case rejected outright as untrusted input — never defaulted
  (the same "reject, never default" rule ADR 0004 later states
  explicitly for the LLM path too).
- `Decision.Evidence` always carries the full trail of what was
  consulted and what it showed, so a Decision is independently
  auditable without re-querying every upstream.

### What later ADRs added, for cross-reference

| ADR | What it added to E1/E3 |
|---|---|
| [0004](./0004-llm-reasoner-behind-the-policy-layer.md) | `FlowBalanceAdvisory.arbitrate`: an optional model-backed `Plan` arbitrated against the deterministic `Decision`; `Decision.Source` records which won. |
| [0007](./0007-second-wave-outbound-mcp-clients.md) | order-management/labor-performance/process-path-management outbound clients wired (not yet all consumed). |
| [0008](./0008-labor-utilization-advisory-correlation.md) | `Decision.Utilization`: an additive labor-performance correlation overlay on E1, never replacing the base recommendation. |
| [0013](./0013-warehouse-planning-mcp-client-and-capacity-outlook.md) | `pathBriefDTO.CapacityOutlook`: an optional, fail-open warehouse-planning section on E3's per-path output. |

DailyBrief's own reasoner coverage (an LLM arbitration path analogous to
E1's) remains a documented next phase, not yet implemented — see
[ADR 0004](./0004-llm-reasoner-behind-the-policy-layer.md)'s Status line.

## Consequences

**Positive** — the ADR index now has a record for the two use cases every
other ADR in this series implicitly depends on; a new reader can start
here instead of reverse-engineering the original design from six later,
narrower extension ADRs.

**Negative / accepted** — none; this is a documentation-only record. No
code changed as a result of writing it.
