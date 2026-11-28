---
id: 0034-wes-edge-watch-pick-face-replenishment-and-task-interleaving
title: "0034 — WES-edge watch: pick-face-replenishment and task-interleaving consumed in the daily brief"
sidebar_label: "0034 · WES-edge watch (PFR + TI)"
description: "The agent consumes the two remaining ADR-0025 WMS-edge clients: one daily-brief section reads pick-face-replenishment's open replenishment requests (count by priority, oldest pending age) and task-interleaving's hint lifecycle counts, each half fail-open."
---

# ADR 0034: WES-edge watch: pick-face-replenishment and task-interleaving consumed in the daily brief

## Status

Accepted (2026-11-28). Closes gap row 48 of the fleet's connect-all audit.
Follows the daily-brief section shape of
[ADR 0024](./0024-slot-plan-watch-promise-health-and-ppm-client-removal.md)
(the previous sections added here) and applies
[ADR 0023](./0023-skip-mcp-clients-without-an-endpoint.md) (a client is
built only when its endpoint is set) unchanged. Partially supersedes
[ADR 0025](./0025-network-fleet-mcp-clients.md): its "wired but
unconsumed" posture for task-interleaving and pick-face-replenishment ends
here — the ten retail-network clients and seller-inbound stay as they are.

## Context

The fleet's connect-all audit left one wiring gap in this agent
(gap row 48): the two WMS-edge clients that ADR 0025 added alongside the
retail-network fleet — `pickFaceReplenishment`
(`PICK_FACE_REPLENISHMENT_MCP_ENDPOINT`) and `taskInterleaving`
(`TASK_INTERLEAVING_MCP_ENDPOINT`) — were wired, golden-pinned and
fail-open, but no use case read them; `wire_clients.go` still said "No use
case consumes any of them yet".

Both contexts sit at the execution edge of the warehouse, exactly where
the daily brief's existing sections (backlog, staffing, queue, stuck
tasks, slot plans, promises) stop:

- pick-face-replenishment owns the forward pick faces and the
  replenishment requests that refill them from reserve. Its published
  `list_replenishments` answers the question nobody surfaces in one
  place: how many requests are still open, at which urgency, and how long
  the oldest one has been waiting.
- task-interleaving owns the hints that chain a second task onto a
  station's completed one. Its published `list_hints` answers the
  lifecycle question: how many hints are live, and how the terminal
  states (consumed, expired, superseded) are distributed — whether
  interleaving is actually being used or quietly rotting.

The ports already expose exactly these reads
(`PickFaceReplenishmentClient.ListReplenishments`,
`TaskInterleavingClient.ListHints`); no port change is needed, and none
is made.

## Decision

1. **Use case.** `usecases.WesEdgeWatch` (named for what it reports: the
   warehouse execution edge) is one read-only use case that reads BOTH
   clients — the gap row's exact ask. It feeds the daily brief as one
   optional top-level section, `wesEdgeWatch`, with two independently
   fail-open halves. There is no operator input, no threshold and no new
   env var: priorities, states and instants are the upstreams' published
   facts and "now" comes from the clock.
2. **Replenishment half.** One bounded scan of
   `list_replenishments` (`state = "Requested"`, pages of 500, at most
   10 pages = 5,000 requests) summarized by
   `policy.SummarizeOpenReplenishments`: the total, the count per
   `priority` value (a free string in the published schema — the summary
   invents no urgency ranking, it only orders the groups largest count
   first, ties by name), and the oldest pending request (id, site, SKU,
   priority, `requestedAt`, age). A Requested request published without a
   parseable `requested_at` breaks the contract: it is dropped with an
   unavailable line naming it, never defaulted to the epoch.
3. **Interleaving half.** One bounded scan of `list_hints` per lifecycle
   state (`Active`, `Consumed`, `Expired`, `Superseded` — four scans,
   pages of 500, at most 10 pages each), so each count is that state's
   own complete count rather than a skewed window of one newest-first
   list. A zero count is a fact ("no active hints"); a state whose scan
   failed is absent from `counts` and named on `unavailable`, never
   counted as zero.
4. **Degradation posture.** Each half NEVER fails the brief and never
   invents calm: an unreachable upstream, a failed scan, a broken
   published fact or the page bound lands on that half's explicit
   `unavailable` lines (the page bound says the counts are a lower
   bound). A nil port omits that half entirely; both ports nil (both
   `*_MCP_ENDPOINT` unset, ADR 0023) omits the whole section and the
   brief is byte-for-byte as it was before this ADR.
5. **Wiring.** `newDecisionSupport` wires the watch when EITHER client is
   built. `wire_clients.go`'s `taskInterleaving` and
   `pickFaceReplenishment` comments now name the use case per the fleet
   convention (like planning → capacity outlook, ADR 0013) instead of "No
   use case consumes it yet".
6. **Surface.** The section rides the existing daily brief:
   `wesEdgeWatch` on `GET /daily-brief` and `get_daily_brief`, camelCase
   JSON, mirrored in both inbound adapters' DTOs, omitted entirely when
   both upstreams are unconfigured. No new REST route and no new MCP
   tool — the same decision ADR 0024 took for `slotPlanWatch` and
   `promiseHealth`, and no authority-level change follows from it.

## Consequences

- The agent finally answers "how much replenishment is waiting on the
  pick face, how urgent, and how stale?" and "are interleaving hints
  being consumed or expiring?" with evidence per group, read-only.
- A pick-face-replenishment or task-interleaving tool-surface change
  still shows up as a failing contract test (the ADR-0025 goldens),
  before any decode of zero values here.
- One brief costs at most five bounded list calls (1 replenishment scan
  + 4 hint-state scans) when both upstreams are configured. Priority is
  a free string, so the per-priority groups are whatever the upstream
  publishes; an enum there would let a future change rank by urgency
  instead of by count.
- The daily brief's JSON gains one optional top-level key; consumers
  that decode strictly must tolerate it (it is `omitempty`-absent when
  both upstreams are unconfigured).
- Deployment: no new values.yaml, ConfigMap, env var or chart change —
  the section switches on per upstream the moment
  `PICK_FACE_REPLENISHMENT_MCP_ENDPOINT` or
  `TASK_INTERLEAVING_MCP_ENDPOINT` is set, exactly as ADR 0023 wired.
- Still unconsumed (out of scope here): the ten retail-network
  ADR-0025 clients, seller-inbound, returns-processing,
  exception-management, process-path-management and the ADR-0032 yard
  clients. Their `wire_clients.go` comments are unchanged.
