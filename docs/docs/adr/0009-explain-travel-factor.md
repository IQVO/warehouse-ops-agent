---
id: 0009-explain-travel-factor
title: "0009 — explain_travel_factor: correlating a real travel-distance reading against a slow path, with no location-code auto-resolution"
sidebar_label: "0009 · explain_travel_factor"
description: A new read-only MCP tool + REST endpoint that calls facility-layout's estimate_travel_distance for two caller-supplied location codes and classifies the result as a significant or negligible contributor to a slow path -- deliberately never inferring the location codes itself.
---

# ADR 0009: explain_travel_factor — travel-distance advisory correlation

## Status

Accepted

**Date:** 2026-09-13

## Context

facility-layout's Phase B3 cross-context follow-on plan named this repo's
piece of the work plainly: "warehouse-ops-agent: use
`estimate_travel_distance` to explain slow paths." facility-layout's own
ADR 0017 shipped exactly that MCP tool — given two seven-segment location
codes, it returns the shortest route's length in metres and whether any
leg was graph-estimated rather than measured. `warehouse-ops-agent`
already had a `FacilityLayoutClient` outbound port (`list_sites`,
`get_site_layout`, `get_zone_grid`) wired into `DailyBrief`, so extending
it to also call `estimate_travel_distance` is a small, well-precedented
addition on its own.

The harder question is what this correlation should take as *input*. The
obvious shape — "given a slow process path, look up the two locations
its stuck work is traveling between and estimate the distance" — turns
out to have no live wire path anywhere in the fleet today:

- fulfillment-execution's `Task` aggregate has no location code at all
  (it carries `taskType`, `cpt`, `orderRef`, `requiredCapabilities`,
  `lease` — see that repo's `internal/domain/task/task.go`).
- fulfillment-execution's `Station` aggregate gained an *optional*
  `locationCode` field in that repo's own recent B3 item (ADR 0024
  there), validated against facility-layout's WorkCenter role — but that
  field is not surfaced by `diagnose_stuck_tasks`, `get_queue_status`, or
  any other published MCP tool. There is no `get_station` tool at all.
- wes-work-planning's `PathPlan`/`get_rebalance_recommendation` carry no
  location code either — its own B3 item (ADR 0017 there) added a
  `TravelDistanceLookup` for its *own* internal `CommitShiftPlan`
  computation, not a published read surface this agent could consume.

So there is no fact anywhere in the fleet today of the shape "process
path X's stuck work travels from location A to location B" that this
agent could read and forward automatically. Inventing one — guessing a
"representative" location pair for a path, or silently picking the first
claimed task's station — would violate this repo's own hardest
guardrail: `CLAUDE.md`'s "Never fabricate a number/fact `Decide()` or any
correlation function doesn't actually have evidence for." A distance
estimate computed from a made-up location pair is not evidence of
anything.

## Decision

**Build `explain_travel_factor` as a distinct, additive use case and
tool/endpoint that takes both location codes as required, explicit
caller input — never inferred, resolved, or guessed by this agent.**

1. **New outbound port method, mirroring the existing three methods'
   shape exactly** (the same pattern ADR 0007/0008 already established
   for extending a port): `ports.FacilityLayoutClient` gains
   `EstimateTravelDistance(ctx, from, to string) (TravelDistance, error)`;
   `ports.TravelDistance`/`ports.TravelNode` hand-mirror facility-layout's
   `travelDistanceDTO`/`travelNodeDTO` field-for-field (this repo's
   hand-mirrored-domain-types discipline, no cross-repo Go import, ever).
   `mcpclient.FacilityLayout.EstimateTravelDistance` calls
   `estimate_travel_distance` via the same `c.session.callTool(...)`
   pattern the other three methods already use.
2. **A small, pure correlation function in `internal/domain/policy`**
   (`travel_factor.go`, a new file — mirroring `utilization_correlation.go`'s
   own precedent of keeping each additive correlation in its own file).
   `CorrelateTravelFactor(reading *TravelDistanceReading)
   *TravelFactorCorrelation` classifies a resolved reading against
   `TravelFactorDistanceThresholdMetres = 60.0` (documented here, not
   derived from a live percentile — the same posture ADR 0008 already
   accepted for its own thresholds; 60m is roughly 15-20 aisle-widths of
   unloaded walking, enough to plausibly account for tens of seconds of a
   task's measured duration) into one of two named outcomes,
   `travel_significant` or `travel_negligible`. Unlike
   `CorrelateUtilization`, there is no "nothing observed" case: a
   resolved distance reading is never itself a nullable business fact the
   way a utilization percentage is, so the function returns `nil` only
   when it is given `nil` (the upstream call failed).
3. **A new, distinct use case (`usecases.ExplainTravelFactor`), not an
   extension of `FlowBalanceAdvisory`.** It takes `pathId` (context/
   logging only, never used to resolve anything) plus
   `fromLocationCode`/`toLocationCode` as REQUIRED explicit arguments —
   an empty value on either is rejected outright as untrusted/incomplete
   input, the same reject-never-default posture `FlowBalanceAdvisory`
   already applies to an unrecognized `RebalanceAction` enum. A
   transport/call error degrades to a zero-value result plus the
   propagated error (ADR-0004's fallback discipline: MCP tools are the
   only actuators, a missing/unreachable signal degrades rather than
   panicking), logged the same way every other upstream-unavailable path
   in this package already is.
4. **New read-only MCP tool `explain_travel_factor` and REST endpoint
   `GET /explain-travel-factor`**, both requiring `fromLocationCode`/
   `toLocationCode` as explicit parameters/query params — the tool's own
   description states plainly that the caller must already know both
   codes. This is a genuinely different shape from `get_flow_balance_exception`
   (which resolves everything itself from a `pathId`): it is closer to
   `get_zone_grid`'s existing "caller supplies the identifier, tool
   returns a fact about it" shape than to a correlation over
   automatically-gathered signals.
5. **Not folded into `FlowBalanceAdvisory.Execute`'s signature.** Adding
   optional `fromLocationCode`/`toLocationCode` parameters there was
   considered and rejected (see Alternatives): every existing caller of
   that use case would need new plumbing for two parameters that, today,
   nothing in the fleet can actually supply from a `pathId` alone. A
   separate use case keeps `FlowBalanceAdvisory`'s existing signature and
   every `TestDecide`/`TestExecute` case byte-for-byte unchanged, and
   composes naturally: a caller who *does* have both codes (a human
   operator who already knows a station's location and a stuck order's
   destination bin, say) can call `explain_travel_factor` and read its
   result alongside `get_flow_balance_exception`'s own evidence, without
   this agent inventing the connection between them.
6. **Zero write capability preserved.** `explain_travel_factor` is
   annotated `ReadOnlyHint: true`; it calls exactly one upstream read
   tool and computes nothing that mutates any state.

## Consequences

**Positive** — closes facility-layout's Phase B3 follow-on item honestly:
a caller who already has two real location codes (from a runbook, a
warehouse-console drill-down, or a human operator's own knowledge of the
floor) gets a grounded, evidence-backed travel-distance reading and
classification, with the same "estimated vs. measured" caveat
facility-layout's own tool surfaces. `FacilityLayoutClient` gains a
fourth, genuinely consumed method (unlike the "wired but unconsumed"
precedent ADR 0007 established for the second-wave clients).

**Negative / accepted** — this tool cannot answer "why is process path
PICK-A slow" on its own; it can only answer "how far apart are these two
specific locations" once a caller supplies them. There is no automatic
"pick the right two locations for this path" feature, and building one
honestly would require a published MCP tool somewhere in the fleet that
surfaces a task's or station's location code bound to a process path —
which does not exist today (fulfillment-execution's `Station.locationCode`
is persisted but not yet published on any tool; see ADR 0024 there).
Until that changes, this tool's real-world usefulness depends on the
caller already possessing that knowledge, which is a genuine limitation,
not merely a documentation gap. `TravelFactorDistanceThresholdMetres` is
a judgment call, exactly like ADR 0008's own thresholds, and may need
tuning once real fleet travel-distance data exists.

**No behavior change to any existing use case** — `FlowBalanceAdvisory`,
`DailyBrief`, and every existing REST/MCP response are byte-for-byte
identical before and after this change; `explain_travel_factor` is purely
additive.

## Alternatives considered

- **Extend `FlowBalanceAdvisory.Execute` with optional
  `fromLocationCode`/`toLocationCode` parameters, folding travel
  correlation into `Decision.Evidence`/`Decision.Utilization`-style
  additively.** Rejected: every existing caller (HTTP handler, MCP tool,
  the ADR-0004 reasoner's brief-building code) would need to plumb two
  new parameters through for a correlation that, without a live
  location-code source, would sit unused in the overwhelming majority of
  calls — a worse "wired but unconsumed" shape than a clean, separate
  tool that a caller reaches for only when it actually has the two codes
  in hand.
- **Infer the two location codes automatically** — e.g. from
  fulfillment-execution's `diagnose_stuck_tasks` output correlated
  against some assumed station-to-destination mapping. Rejected outright:
  there is no published fact anywhere in the fleet linking a stuck task
  to two location codes, and guessing one would fabricate evidence this
  agent's own `Decide()`/correlation discipline explicitly forbids (see
  Context).
- **Wait for fulfillment-execution to publish `Station.locationCode` on
  a tool before building anything here.** Rejected: the caller-supplied
  shape is independently useful today (a human operator investigating a
  specific slow path already knows the physical locations involved far
  more often than this agent could infer them), and nothing about this
  design blocks a future ADR from adding automatic resolution once a
  location-code-bearing tool exists — `usecases.ExplainTravelFactor`'s
  signature would simply gain an alternate path that resolves the codes
  itself, without changing the tool's existing required-parameters
  contract for callers who already have them.
