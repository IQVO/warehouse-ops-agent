---
id: governance-note
title: Governance note — this agent's write posture
sidebar_label: Governance note
description: warehouse-ops-agent only ever writes through published tools; v1 is read-only and CI-enforced; acting is a later slice behind a re-introduced authorization gate plus human confirmation.
---

# Governance note — this agent's write posture

This note records the governance decision specific to
`warehouse-ops-agent`, on top of the fleet-wide rules in the
[MCP Governance Charter](https://iqvo.github.io/fulfillment-execution/docs/mcp/governance-charter)
(`fulfillment-execution` is the charter's canonical home; every sibling
context, and this agent, follow it). Read that charter first — this page
only covers what is specific to being a cross-context *decision-support*
agent rather than a bounded context.

## The rule

**This agent writes to a bounded context only through that context's own,
already-published write MCP tool — never through a database, an internal
API, or any mechanism that bypasses that context's own invariant
enforcement.** A mistaken or hallucinated agent call is safe by
construction: the target context's existing domain layer is the backstop,
exactly as it is for a human operator calling the same tool.

Concretely, the only write tools this agent could ever call are:

| Tool | Owning context | What it would do |
|---|---|---|
| `assign_labor` | `workforce-management` | Assign heads to a path (E1's `assign_labor` recommendation) |
| `release_next_work` | `wes-work-planning` | Release the next work unit into a path (E1's `release_next_work` recommendation) |
| `revoke_reservation` | `inventory-storage` | Free a stranded reservation's stock back to usable (E2's `revoke_reservation` recommendation, now surfaced read-only via `detect_stranded_reservation` — see [api-surface](../api-surface.md)) |
| `complete_task` | `fulfillment-execution` | Not currently reachable from any of this agent's decision policies |

## v1 scope: read-only, recommendations-only

`warehouse-ops-agent` holds **zero write capability**. Every MCP tool this
agent's own inbound server exposes (`get_daily_brief`,
`list_open_exceptions`, `get_flow_balance_exception`,
`explain_travel_factor`, `detect_stranded_reservation`) is annotated
`ReadOnlyHint: true`, and its
outbound adapters (`internal/adapters/outbound/mcpclient/`,
`internal/adapters/outbound/restclient/`) implement only **read** ports
(see `internal/ports/clients.go`, `clients_phase2.go`) — there is no
`AssignLabor`, `ReleaseNextWork`, or `RevokeReservation` method anywhere
in this codebase to call even by mistake.

This is enforced the same way the "no direct bounded-context dependency"
rule is — statically, in CI, not by a runtime check that could be
bypassed: `internal/architecture/zerowrite/zerowrite_test.go`
(`TestNoMutatingHTTPMethodInOutboundClients`,
`TestNoMutatingToolAnnotationInMCPServer`) fails the `arch-test` job if an
outbound client gains a mutating HTTP method or an inbound tool is
registered without `ReadOnlyHint: true`. A future write-capable slice adds new outbound-client
methods and a new tool-registration entry deliberately; it cannot happen
by accident.

## The future act slice: authorization gate + human confirmation

When a write-capable slice does land, it inherits two guardrails already
decided, not deferred:

1. **A separate, re-introduced authorization gate.** The fleet-wide auth
   layer this section originally described (`ScopeRead`/`ScopeReadWrite`
   over `StaticKeyAuth`) was removed fleet-wide — see
   [ADR 0006](../adr/0006-fleet-wide-auth-removal.md), which supersedes
   [ADR 0005](../adr/0005-rest-identity-static-bearer-scopes.md). Per the
   charter (§7, §3), any write tool this agent's inbound MCP server ever
   exposes still MUST be gated by *some* explicit authorization mechanism
   distinguishing a read-only caller from a write-capable one — rejecting
   a read-only caller the way the old `403` did — even though the specific
   static-bearer implementation is gone; the act-slice picks the
   replacement mechanism when it lands.
2. **Explicit human confirmation before the write executes.** A
   recommendation (`assign_labor`, `release_next_work`,
   `revoke_reservation`) surfacing from this agent's read tools is not
   itself authorization to act. The act-slice's design — a human-in-the-loop
   confirmation step between "recommended" and "executed" — is a
   commitment recorded here ahead of the implementation, per
   [ADR 0001](../adr/0001-warehouse-ops-agent-placement.md)'s consequence
   that "human-in-the-loop and least-privilege" stay preserved as this
   agent's capability grows.

## Auditability

Every tool call this agent's own MCP server handles emits an OTel span
(`mcp.tool <name>`) carrying the tool name (`mcp.tool.name`), per the
charter's §9 auditability rule — the same instrumentation pattern the
sibling servers use, so a call here is traceable in Jaeger alongside
every upstream call it triggers.
