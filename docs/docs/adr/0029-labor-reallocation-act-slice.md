---
id: 0029-labor-reallocation-act-slice
title: "0029 — Labor reallocation act slice (act slice #2): ranked proposals, one human-approved move delegated over A2A"
sidebar_label: "0029 · labor reallocation act slice"
description: "The second and last write this agent performs: a pure policy ranks at most three moves that cover an understaffed path from workforce-management's own readings, a propose then approve flow (HMAC token bound to one move) delegates ONE move to workforce-management's assign_labor A2A skill with proposalId, approvedBy and agentId. REST-only, never an MCP tool, never reachable by the LLM; a token-less, read-only labor_reallocation_outlook MCP tool (authority L2) serves the same ranking."
---

# ADR 0029: Labor reallocation act slice (act slice #2)

## Status

Accepted (2026-11-02). Adds a **second** human-gated act slice beside
[ADR 0027](./0027-act-slice-human-confirmed-work-release.md) (release the next
work unit) and re-pins the zero-write guarantee as "exactly these two slices".
Builds on [ADR 0004](./0004-llm-reasoner-behind-the-policy-layer.md) (the LLM
reasoner's actuators stay allow-listed MCP reads),
[ADR 0006](./0006-fleet-wide-auth-removal.md) (no authentication) and
[ADR 0028](./0028-advisory-exception-context-on-release-proposals.md) (advisory
context never gates). Peer contract: workforce-management's A2A skill
`assign_labor` (its ADR 0039), which gains the optional attribution arguments
`proposalId`, `approvedBy` and `agentId` (fleet contract G4).

> **Amended by [ADR 0030](./0030-act-slice-idempotency-key-store.md) and
> [ADR 0031](./0031-approvals-route-replaces-the-approve-verb.md):** both act
> POSTs now take an `Idempotency-Key`, and the approval route is
> `POST .../{proposalId}/approvals`; `.../approve` is a deprecated alias until
> 2027-02-10. Where this ADR names `/approve`, it describes the state at the
> time of writing.

> Numbering: this repo already has two files numbered 0027. Nothing is renamed;
> this is simply the next free number.

## Context

The agent can already say "path `pack` is understaffed" (`get_staffing_gap`
inside the daily brief and the flow-balance recommendation `assign_labor`). It
cannot say **who** could cover it, and the operator then acts elsewhere. The
fleet's agentic standard asks the next step to be a proposal a human approves,
with the proposal and the approver carried into the event workforce-management
publishes (`initiated_by`).

Two facts constrain the design:

- **workforce-management exposes no list of associates or of their current
  assignments** over MCP (`get_staffing_gap`, `propose_path_heads`,
  `assign_labor`, `get_workforce_labor_report`). Staffing is only known per
  path, as `plannedHeads` / `activeHeads` / `understaffed`. This agent
  therefore cannot invent a candidate, and must not scrape one from elsewhere.
- **Nothing here may add state.** The agent owns no database and no Kafka
  producer ([ADR 0001](./0001-warehouse-ops-agent-placement.md)); proposal events
  (`ProposalIssued/Approved/Rejected/Expired`) are deliberately deferred. The
  trail is the confirmation token, the `proposal_id` carried to
  workforce-management, spans and logs.

## Decision

### 1. A pure policy ranks at most three moves

`policy.ProposeLaborReallocation(gap, perPathStatus, assignments, scope)`
(`internal/domain/policy/labor_reallocation.go`) returns up to
`MaxReallocationMoves` (3) moves `{associateId, fromPathId?, toPathId,
reasonCodes[], evidence[], nextCheck}`:

- It proposes nothing unless the **target** is reported understaffed **and**
  its `activeHeads` is below its `plannedHeads`; it never proposes more moves
  than the path is short.
- An associate may leave a path only while that path's `activeHeads` is
  strictly above its `plannedHeads`, and each accepted move spends one head of
  that surplus. A path at or below plan, an understaffed path, the target
  itself, an origin whose reading is unavailable, and a duplicate associate are
  all screened out **with a reason code**, never silently.
- Ranking is: origins above plan before associates with no stated origin,
  larger surplus first, then associate id (deterministic).
- No threshold is invented and no quantity is derived for display: the
  evidence is the `get_staffing_gap` readings verbatim. Every move states the
  next **read-only** check an operator can make.
- The candidates are **caller-supplied** (the human knows who is movable); an
  associate with no stated origin path is kept but ranked last and flagged
  `no_current_path_stated`, because nothing verifies the move leaves another
  path whole.

The policy is deterministic. The LLM is **not** in this loop (see 6).

### 2. Propose then approve, one token per move

| Call | Effect |
|---|---|
| `POST /labor-reallocation/proposals` `{siteCode, shiftId, toPathId, candidates[{associateId, fromPathId?}]}` | Reads `get_staffing_gap` (by `siteCode`) for the target and each distinct origin, applies the policy, and returns `proposals[]`: each ranked move with its own `proposalId` (`lrp-<nonce>`), short-lived HMAC `token` bound to **that exact move** (associate, from, to), `expiresAt`, `reasonCodes`, `evidence`, `nextCheck`; plus the `target` reading and the `skipped` candidates. **Writes nothing.** |
| `POST /labor-reallocation/proposals/{proposalId}/approve` `{token, approvedBy}` (now `.../approvals`, [ADR 0031](./0031-approvals-route-replaces-the-approve-verb.md)) | Verifies the token, checks it is the proposal named in the URL, consumes it, and delegates ONE move. |

The token reuses the ADR 0027 signer and its guarantees (HMAC-SHA256,
positional strict payload, `APPROVAL_TTL`, `APPROVAL_SIGNING_KEY`) with its own
**token kind**: the action in position 0 (`reallocate_labor`) means a release
token is not redeemable here and a reallocation token is not redeemable as a
release (tested both ways). It is a process gate, **not authentication**.

**At most once**, as in ADR 0027: the nonce is consumed *before* delegating, a
failed or ambiguous delegation is re-proposed by a human and never retried, and
the used-nonce set is process-local (`replicaCount: 1`).

`approvedBy` is required (non-blank, no control characters, at most 128
characters): it ends up in an event. A bad token, an expired token, a token of
another proposal and a missing approver are all refused **before** the peer is
called.

### 3. Delegation: A2A `assign_labor`, with attribution

`ports.LaborAssigner.AssignLabor` is the second write port; its only
implementation is `LaborClient` in `internal/adapters/outbound/a2a` (the third
client there, beside the release client and the ADR 0028 read-only lookup). It
sends the skill arguments `{associateId, pathId, proposalId, approvedBy,
agentId:"warehouse-ops-agent"}` — exactly the names pinned in the fleet
contracts. The SDK client sends the `A2A-Version` header on every call (tested
against a stand-in peer). Peer problems map to typed errors, surfaced as:

| Peer answer | Port error | REST status |
|---|---|---|
| business rule (409: certification, break, shift ended, ...) | `ErrAssignmentRejected` | `409` |
| unknown associate or path (404) | `ErrAssignmentNotFound` | `404` |
| refused arguments (400) | `ErrAssignmentInvalid` | `400` |
| anything else / unreachable / off-contract | `ErrWorkforceAgentUnreachable` | `502` |

workforce-management keeps every invariant (one active assignment per
associate, certification match, break and shift rules); this agent only asks.
The authoritative fact still comes back as the `LaborAssigned` CloudEvent on
Kafka, now carrying `initiated_by` (`via:"a2a"`, `proposal_id`, `approved_by`,
`agent_id`), stamped by the peer's A2A adapter — never by this agent.

> Until workforce-management ships the optional attribution arguments, its
> strict decoder rejects them as unknown: approving then answers `400`
> (`ErrAssignmentInvalid`) from the peer. The integration is verified here
> against an in-memory stand-in peer that enforces the pinned argument set.

### 4. Surface: REST for the act, MCP only for the token-less read

- The two POST routes are REST-only and carry the ADR 0027 transport
  protections (strict `application/json`, 8 KiB body, unknown fields rejected,
  GET/OPTIONS-only CORS in the binary). The chart adds the edge half: the
  dedicated read-only-CORS HTTPRoute now also matches `/labor-reallocation`.
- **`labor_reallocation_outlook`** (MCP, `ReadOnlyHint: true`, `Meta
  com.warehouse/authority = L2`) returns the same ranked list **without a
  token, a proposal id or an expiry** and has no write path. It is built from
  `usecases.LaborReallocationOutlook`, which holds no signer and no write
  port; the act use case (`usecases.LaborReallocation`) is wired into the REST
  adapter only. Registered when `WORKFORCE_MANAGEMENT_MCP_ENDPOINT` is set.
- Unset `WORKFORCE_MANAGEMENT_A2A_URL` (the default) = the act is off: no write
  adapter is built and both routes answer `503`. Setting it without the MCP
  endpoint is a startup error (a proposal without its readings would be a
  guess).

### 5. The guarantee is re-pinned for exactly two slices

`internal/architecture/zerowrite/act_slice_test.go` now pins: exactly the four
routes of the two propose/approve pairs register a non-GET method; the A2A SDK
stays in one adapter package, which holds exactly three clients and whose skill
constants are exactly `release_next_work`, `assign_labor` (writes) and
`find_exceptions` (read); `ReleaseNextWork` and `AssignLabor` are each declared
exactly twice (their port and their one adapter) while `RevokeReservation` and
`CompleteTask` stay forbidden everywhere; and neither use case, nor the
confirmation gate, nor the write ports are reachable from the MCP server or the
LLM reasoner. Because a guard that cannot fail is worthless, the file also
proves itself red on synthetic sources (a third POST route, a stray DELETE, a
`MethodFunc("PUT")`, a third write verb), and each mutation was additionally
checked against the real tree (a third `Post` route, a `RevokeReservation`
declaration, the MCP package naming `usecases.LaborReallocation` all fail).

### 6. Shadow only; no LLM in the loop

The slice is deterministic. The ADR 0004 `Reasoner` port is shaped around
flow-balance plan arbitration (`submit_plan`, allow-listed tools, arbitration
metrics), so mirroring it here is not a ten-line seam. A shadow-mode reasoner
for reallocation (agreement against the policy, per fleet contract G10) is
follow-up work; it would add an extension point without touching this slice's
write path.

### 7. Authority and telemetry

Every MCP tool of this server now declares exactly one authority level in
`Meta com.warehouse/authority` (reads `L1`, the outlook `L2`; a tool missing
from the table fails at registration, and a fitness test registers the full set
and pins the table to it). Existing span names are unchanged; spans gain
`gen_ai.operation.name` (`execute_tool` on `mcp.tool <name>` and on the new
`mcp.client <tool>` span around every upstream MCP call, `invoke_agent` on
`a2a.skill <id>`), `gen_ai.agent.name` (`warehouse-ops-agent`) and
`warehouse.authority` (the level of what the span runs: `L1` for upstream reads,
`L3` for the two write skills, which are human-gated here).

## Consequences

- **Second write in this agent**, deliberately the last for now: one associate
  per confirmation, workforce-management-enforced invariants, a named human
  approver recorded downstream.
- **Auditable without storing anything**: propose, replay refusal, delegation
  and failure are structured log lines carrying `proposalId`, `associateId` and
  the sanitized approver; the A2A hop is a traced `invoke_agent` span.
- **Candidates must come from the human.** There is no associate list to read;
  the outlook is only as good as the names offered. If workforce-management
  later publishes a read tool for assignments, the candidate input becomes
  optional without changing the policy.
- **An unknown path surfaces as a `502`**: workforce-management's
  `get_staffing_gap` rejects it with prose, not a `*-required`/`invalid-*`
  slug, so it cannot be classified as the caller's input.
- **Not authentication.** As in ADR 0027, anyone who can reach the agent can
  propose and approve; the property is the mandatory, evidence-bearing second
  call.
- **Rejected**: exposing approve as an MCP tool; a persisted proposal table
  (violates "no persisted state"); publishing proposal events now (deferred);
  retrying on ambiguous failure; reading the buildingId alias of
  `get_staffing_gap` (an unscoped count across every site) instead of
  `siteCode`; a general-purpose "any write" port.
