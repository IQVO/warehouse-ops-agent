---
id: subdomain-classification
title: Subdomain classification
sidebar_label: Subdomain classification
description: Why warehouse-ops-agent is a read-side decision-support mechanism, not a bounded context, and where that leaves it in the strategic map.
---

# Subdomain classification

`warehouse-ops-agent` is deliberately **not classified as a bounded
context** alongside the fleet's Core/Supporting/Generic subdomains
(`inventory-storage`, `wes-work-planning`, `fulfillment-execution` and
`warehouse-planning` as Core; `workforce-management`, `labor-performance`
and `network-fulfillment` as Supporting; `order-management` as
Generic/Supporting; `facility-layout` and `process-path-management` as
Generic). Strategic Design classification answers "whose aggregate is
this, and how differentiating is it" — and this repo owns no aggregate at
all.

Where the fleet-wide map has to put every repo in one of the three
buckets for comparison purposes, it files this agent as **Supporting**:
operationally valuable, but owning no domain aggregate of its own. The
[core domain chart](./core-domain-chart.md) plots it there, with the
caveat above attached.

## What it actually is

A **read-side / decision-support mechanism**: a Customer of the fleet's
Open Host Services, correlating their published facts through a pure policy
layer into ranked, human-gated recommendations. In DDD vocabulary this is
closest to a **CQRS read model that spans context boundaries** plus a
**policy** (in the tactical-pattern sense: a strategy object encoding a
business rule) — not a bounded context of its own.

## Why not just call it a sixth bounded context anyway

A bounded context is defined by the aggregate(s) and invariants it
protects. `warehouse-ops-agent` protects none:

- It has no aggregate root, no entity with a lifecycle it enforces.
- It has no invariant a domain-layer method rejects an operation over —
  every rejection in its policy layer (`ParseRebalanceAction`,
  `TaskType.Valid()`, `ParseLLMMode`, `ValidatePlan`) is *input validation
  at an untrusted boundary*, not a business invariant about a domain
  concept this repo owns.
- It persists no state. Restart it and it has forgotten nothing, because
  it never knew anything that wasn't re-derivable from its upstream
  reads.

If a future slice ever needs one of those three things — an aggregate, an
invariant, persisted state — [ADR 0001](../adr/0001-warehouse-ops-agent-placement.md)
is explicit that this is the signal that slice belongs in a different
repo (or is itself a new bounded context), not something to smuggle into
this one's policy layer.

## Relationship to the platform's Strategic Design work

The sibling services' own subdomain classification (see each
service's `docs/ddd/subdomain-classification.md`) is unaffected by this
repo's existence. `warehouse-ops-agent` sits *above* that map as a
cross-cutting Customer, the same relationship `order-management` has to
`fulfillment-execution` and `inventory-storage` — a new repo that
consumes two (there, several) existing contexts' published contracts with no
Go-level dependency on either.
