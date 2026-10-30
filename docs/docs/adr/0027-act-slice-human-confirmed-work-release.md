---
id: 0027-act-slice-human-confirmed-work-release
title: "0027 — The act slice: human-confirmed release of the next work unit, delegated over A2A"
sidebar_label: "0027 · act slice (release_next_work)"
description: "The first and only write this agent performs: a two-call propose then approve flow, gated by a short-lived HMAC confirmation token, that delegates release_next_work to the wes-work-planning A2A agent. REST-only, never MCP, never reachable by the LLM; the zero-write guarantee is narrowed to exactly this slice and re-pinned by tests."
---

# ADR 0027: The act slice — human-confirmed release of the next work unit, delegated over A2A

## Status

Accepted (2026-10-29). Narrows — does not remove — the v1 zero-write rule of
[ADR 0004](./0004-llm-reasoner-behind-the-policy-layer.md) and fulfils the
"future act slice" commitments of the
[governance note](../mcp/governance-note.md). Builds on
[ADR 0006](./0006-fleet-wide-auth-removal.md) (no authentication, fleet-wide)
and the wes-work-planning A2A agent
([wes-work-planning ADR 0046](https://iqvo.github.io/wes-work-planning/docs/adr/0046-a2a-inbound-adapter)).

## Context

This agent recommends. For flow balance it can already say, with an evidence
trail, "release the next work unit into path P". Until now the operator then
acted elsewhere. The governance note pre-committed the shape of any write
slice: it may only go through the owning context's *published* write tool, it
needs an authorization gate, and it needs explicit human confirmation between
"recommended" and "executed".

Two facts constrain the design:

- **Auth is removed fleet-wide** (ADR 0006), so the "authorization gate" the
  note asks for cannot be caller authentication. The note itself says the
  act-slice "picks the replacement mechanism when it lands".
- **A2A now exists as the fleet's delegation transport.** wes-work-planning
  publishes a `release_next_work` skill on its A2A agent: a goal-shaped
  delegation where *planning keeps every release decision and invariant*. That
  is exactly the "published write tool" the governance rule requires.

## Decision

### 1. One write, delegated, never a data write

`ports.WorkReleaser.ReleaseNextWork(pathId)` is the single write port. Its one
implementation is `internal/adapters/outbound/a2a`, an `a2a-go/v2` client that
discovers wes-work-planning's Agent Card and sends the `release_next_work`
skill. Planning's domain layer remains the backstop, as it is for a human
calling the same tool. `assign_labor`, `revoke_reservation` and
`complete_task` stay recommendation-only.

### 2. The gate is a confirmation token, not an identity

Two explicit calls, both REST:

| Call | Effect |
|---|---|
| `POST /act/release-next-work/proposals` `{pathId, buildingId, shiftId}` | Runs the existing flow-balance policy. Mints a token **only if the policy itself recommends `release_next_work` on complete (non-partial) evidence**. Writes nothing. Returns the token, its expiry, and the full recommendation + evidence the human is confirming. |
| `POST /act/release-next-work/approvals` `{token}` | Verifies the token, consumes it, delegates the release. The single write. |

The token (`internal/domain/approval`) is an HMAC-SHA256-signed positional
payload binding `action, pathId, decision source, rationale, nonce, expiry`.
It guarantees that an approval can only execute **a recommendation this agent
actually made, for the path the human saw, within `APPROVAL_TTL` (default
5 min, max 1 h)**. It is *not* caller authentication: anyone who can reach the
agent can propose and approve. The human-in-the-loop property is the
mandatory, explicit, evidence-bearing second call — a process gate, honestly
labelled as such.

**At most once.** The nonce is consumed *before* delegating. A non-idempotent
write that fails ambiguously (agent unreachable) is re-proposed by a human,
never silently retried. The used-nonce set is process-local and bounded by the
token lifetime; the chart keeps `replicaCount: 1` and the signer defaults to an
ephemeral per-process key (`APPROVAL_SIGNING_KEY` may pin one; if set it must
be ≥ 32 bytes). A second replica without a shared key simply fails closed
(`400 invalid token`).

### 3. Surface: REST only, never MCP, never the model

Approving is a human act, so it is **not an MCP tool** (an MCP caller is
typically a model, and a model approving its own recommendation defeats the
gate) and the LLM reasoner never holds the use case. With no auth, the REST
routes are additionally hardened at the transport:

- strict `Content-Type: application/json` (else `415`) — a cross-site HTML
  form can only send "simple" content types, so this forces a CORS preflight;
- CORS refuses the preflight for the write routes at **two layers**, because
  behind Kong the gateway's CORS plugin answers the preflight itself and never
  reaches the binary: (1) the binary's own policy stays **`GET`/`OPTIONS`
  only** (the existing `TestCORS_PreflightRejectsWriteMethods` stays green and
  a new test pins it for the `/act` paths); (2) the chart renders a dedicated
  `/act` HTTPRoute (`httproute-act.yaml`) carrying a `…-cors-readonly` Kong
  plugin that allows only `GET`/`OPTIONS`, while the main route keeps its
  all-methods plugin because a browser MCP client legitimately needs `POST` on
  `/mcp`. Chart tests pin both, including that the main route did **not** get
  the read-only plugin. This only constrains browsers; any non-browser caller
  that can reach the agent can still use the routes, which is what "no auth"
  means;
- bounded body (8 KiB), unknown JSON fields rejected, trailing data rejected;
- the token is only ever in the propose *response*, which a cross-origin page
  cannot read.

Statuses: `200`; `400` malformed body/token/input; `404` planning does not know
the path; `409` policy does not recommend release / evidence incomplete / token
already used / planning has nothing pending; `410` token expired; `415`; `413`;
`502` the agent is unreachable or answered off-contract; `503` act slice not
configured.

### 4. Opt-in

`WES_WORK_PLANNING_A2A_URL` unset (the default) means no write adapter is
built and both routes answer `503`: the agent is read-only exactly as before.

### 5. The guarantee is re-pinned, not dropped

`internal/architecture/zerowrite` keeps its two original tests unchanged
(upstream read clients GET/HEAD-only; every own MCP tool `ReadOnlyHint: true`)
and gains five (`act_slice_test.go`):

1. exactly the propose/approve routes register a non-GET method;
2. the A2A SDK is imported by exactly one adapter package;
3. `ReleaseNextWork` is declared only by its port and that adapter, and no
   `AssignLabor` / `RevokeReservation` / `CompleteTask` exists anywhere;
4. the MCP server, the LLM adapter and the reasoner port never reference the
   use case, the write port, the confirmation gate or the A2A adapter;
5. only the REST adapter and `cmd/agent` use `usecases.ReleaseWork`.

Each was mutation-checked (a stray `Delete` route, the MCP server importing the
use case, a sibling write verb) and fails as intended.

## Consequences

- **First write in this agent**, deliberately small: one lever, one path per
  confirmation, planning-enforced invariants.
- **Auditable**: every propose, execute, replay-refusal and delegation failure
  is a structured log line carrying `pathId`, `nonce` and decision `source`;
  every request is already an otelchi span, and the A2A hop is traced because
  the SDK transport reuses the otelhttp-instrumented client.
- **A model-sourced recommendation can be confirmed.** The proposal carries the
  decision `source` (`deterministic | llm | fallback`) and the full evidence so
  the human sees which it was; the human, not the model, decides.
- **Not authentication.** If the fleet re-adopts auth, this token composes with
  it rather than being replaced by it.
- **The console** is not wired to these routes (CORS blocks the browser by
  design); a same-origin/BFF integration is a separate, later decision.
- **Rejected**: exposing approve as an MCP tool; a persisted proposal table
  (violates "no persisted state"); retrying on ambiguous failure
  (at-least-once on a non-idempotent write).
