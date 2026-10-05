---
id: 0001-warehouse-ops-agent-placement
title: 0001 — warehouse-ops-agent placement as a new, thin read-side repo
sidebar_label: 0001 · Placement as a new repo
description: Why warehouse-ops-agent is a new, independently-deployable repository rather than logic embedded in an existing bounded context.
---

# ADR 0001: warehouse-ops-agent placement as a new, thin read-side repo

## Status

Accepted

**Date:** 2026-08-25

## Context

The warehouse-systems fleet's five bounded contexts each publish an MCP
(Model Context Protocol) Open Host Service (ADR-0008 in each sibling repo,
plus the fleet-wide `docs/mcp/governance-charter.md`). A round-1 proposal
(`PROPOSAL-agentic-warehouse-ops.md`, kanban task t_9704c611) recommended
building an "agentic warehouse-ops" capability — cross-context
decision-support (flow-balance conflict detection, stranded-reservation
detection, a synthesized daily operational brief) — as a Customer of those
five OHS surfaces, reading via their existing published MCP tools and
telemetry rather than any new write path.

This record consolidates and supersedes the draft that circulated during
that proposal round, `ADR-warehouse-ops-agent-DRAFT.md` (kanban task
t_9704c611's workspace, titled "Warehouse Ops Agent as a read-side
decision-support context"). That draft and this record settle the same
question from two angles — "what kind of thing is this" and "where does it
live" — and the owner confirmed both together, so they are recorded here as
one Accepted decision rather than as two numbered ADRs repeating each
other. Nothing in the draft's Decision or Consequences sections is
inconsistent with what follows; see kanban task t_9a658799 (T6) for the
finalization record.

The open question was placement: where does this decision-support logic
live? Two options were considered:

- **Option A** — a new, independently-deployable repository
  (`warehouse-ops-agent`), thin, hexagonal, depending on the five contexts'
  published contracts only.
- **Option B** — embed the correlation logic inside one of the existing
  five services (e.g. as an extra internal package in
  `fulfillment-execution`).

## Decision

**Option A.** A new repository, `warehouse-ops-agent`, under
`~/warehouse-systems/`. Owner-confirmed: "let's follow the strategy."

## Rationale

- **Bounded-context integrity.** None of the five existing contexts is the
  natural owner of cross-context correlation policy — embedding it in any
  one of them (e.g. fulfillment-execution) would smuggle a second,
  unrelated concern into that context's module and blur its boundary.
- **Independent deployability and lifecycle.** This agent's release cadence,
  on-call ownership, and even its very existence are logically separate
  from any single bounded context's. A new repo keeps that separable.
- **Precedent.** `order-management` was built the same way: a new repo that
  is a pure Customer of two existing contexts' published HTTP APIs, with no
  Go-level dependency on either. `warehouse-ops-agent` is the same pattern
  one layer up, over MCP instead of REST.
- **Enforceability.** A separate Go module makes "no cross-repo Go imports"
  a property `go.mod` itself defeats by construction (there is nothing to
  import even by mistake, modulo an explicit go.mod edit) — and
  `internal/architecture/architecture_test.go`'s
  `TestNoDirectDependencyOnBoundedContexts` makes it an executable,
  CI-enforced guardrail on top of that.

## Consequences

- A sixth deployable joins the fleet, with its own repo, CI, and (later) its
  own Helm chart/Docker image once it has behavior worth shipping.
- Every fact this agent reasons over crosses an MCP tool-call boundary,
  never a Go function call — slightly higher latency and no compile-time
  type-checking against the source context's domain types, traded for a
  hard architectural boundary and zero coupling to any context's internal
  representation.
- This repo's own architecture must stay honest about NOT being a bounded
  context: no aggregate, no invariant, no persisted domain state. If a
  future slice finds itself wanting one of those, that is a signal the
  slice belongs in a different repo (or is itself a new bounded context)
  rather than smuggled into this one's "domain" layer.
