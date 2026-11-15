---
id: domain-vision
title: Domain vision
sidebar_label: Domain vision
description: Why warehouse-ops-agent exists, what it deliberately refuses to own, and the guardrails that keep it that way.
---

# Domain vision

> A decision-support mechanism (read-side, plus two opt-in human-gated act slices) that correlates signals from the
> fleet's bounded contexts into a single diagnosis and a ranked,
> human-gated recommendation. It owns no aggregate, enforces no new
> invariant, and persists no domain state — its "domain" is decision
> **policy**.

## What it owns, and what it refuses to own

| Concern | Owner |
|---|---|
| Correlating a rebalance recommendation, a staffing gap, and a stuck-task diagnostic into one ranked FlowBalanceException | **warehouse-ops-agent** |
| Correlating expired task leases with a usable-stock shortfall into a StrandedReservation recommendation | **warehouse-ops-agent** |
| Synthesizing a cross-path, cross-site daily operational brief | **warehouse-ops-agent** |
| Correlating queue depth with measured labor utilization (ADR 0008) and a travel-distance reading with a slow path (ADR 0009) | **warehouse-ops-agent** |
| Classifying services' runtime error rate / p99 latency against fleet-wide thresholds (`GET /runtime-signals`) | **warehouse-ops-agent** |
| Whether a rebalance is actually needed on a path right now | `wes-work-planning` |
| Whether a shift is actually understaffed | `workforce-management` |
| Whether a task is actually stuck, and why | `fulfillment-execution` |
| Whether stock is actually usable, reserved, or in a bin | `inventory-storage` |
| Whether a location physically exists and what site it belongs to | `facility-layout` |
| How long associates actually spent on tasks versus idle | `labor-performance` |
| **Executing** any recommendation this agent makes | a human; or, on a human's explicit approval through one of the two opt-in act slices ([ADR 0027](../adr/0027-act-slice-human-confirmed-work-release.md) release the next work unit, [ADR 0029](../adr/0029-labor-reallocation-act-slice.md) labor reallocation), the owning context, which receives the write over A2A |

The line is *correlation versus ground truth*. This agent never re-derives
or duplicates a fact an upstream context already owns — it reads that
fact through the context's published MCP tool and correlates it against
readings from the others. It has zero independent authority over any fact
it reasons about.

## The guardrails that make this safe by construction

1. **Reads everywhere, writes only through two human-gated act slices.**
   Every domain fact this agent reasons over crosses an MCP tool-call
   boundary to an upstream context's *existing* read tools, and it calls no
   write tool over MCP (`revoke_reservation` and `complete_task` are never
   called at all). The only writes are the two opt-in REST slices
   ([ADR 0027](../adr/0027-act-slice-human-confirmed-work-release.md),
   [ADR 0029](../adr/0029-labor-reallocation-act-slice.md)): a
   propose-then-approve pair, never an MCP tool and never reachable by the LLM,
   whose approval delegates exactly one A2A skill (`release_next_work`,
   `assign_labor`) to the owning context. Off until the peer's A2A URL is set. See the
   [Governance note](../mcp/governance-note.md). This is CI-enforced by
   `internal/architecture/zerowrite` (`act_slice_test.go`).
2. **Correlate, don't alert on one metric.** The E3 daily-brief rule flags
   a path as an open exception only when **two or more independent
   signals** fire together (see `internal/domain/policy.deriveExceptions`).
   A single understaffed reading, alone, is ordinary operating noise, not
   an exception.
3. **Untrusted input is validated, never defaulted.** Every enum value
   this agent reads back from an upstream tool response (a wes
   `RebalanceAction`, a fulfillment-execution `TaskType`) is checked
   against a closed set. An unrecognized value is rejected with an error —
   never silently coerced to a default.
4. **Partial upstream availability degrades to a typed partial result,
   never a hard failure.** If an upstream context is unreachable, the
   affected path's brief (or exception decision) is marked `Partial` with
   its `MissingSignals` listed, and the recommendation conservatively
   degrades toward `hold` rather than guessing.
5. **Every decision shows its work.** A `Decision`, `StrandedReservationException`,
   or `OpenException` this agent returns always carries a full evidence
   trail: which upstream tool call produced each fact, and what it showed.
   No recommendation is ever returned bare.
6. **A write recommendation is never returned without its blast radius.**
   E2's `revoke_reservation` recommendation is impossible to reach without
   `inventory-storage.get_bin_occupancy`'s reading already in hand — the
   blast-radius readout is a precondition of the recommendation existing
   at all, not an optional enrichment.

## Why a separate repo, not a sixth bounded context

See [ADR 0001](../adr/0001-warehouse-ops-agent-placement.md) for the full
rationale. In one sentence: there is no aggregate or invariant for this
capability to own, so a new bounded context would be a domain in name
only — a new, independently-deployable repo that is purely a Customer of
the existing contexts' Open Host Services keeps the dependency map
acyclic and every context's boundary honest.
