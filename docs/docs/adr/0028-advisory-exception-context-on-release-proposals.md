---
id: 0028-advisory-exception-context-on-release-proposals
title: "0028 — Advisory exception context on release proposals, asked of exception-management over A2A (read-only)"
sidebar_label: "0028 · advisory exception context"
description: "When the agent proposes a release it also asks exception-management, over A2A and read-only, what is open that a human should know before approving, and returns it as an exceptionContext object (ok, unavailable or disabled). The answer is advisory: it never gates, alters or delays the minting decision beyond a 3 s bound, and is not persisted."
---

# ADR 0028: Advisory exception context on release proposals, asked of exception-management over A2A (read-only)

## Status

Accepted (2026-10-30). Extends the act slice of
[ADR 0027](./0027-act-slice-human-confirmed-work-release.md) with a second,
**read-only** A2A client; it does not widen the write surface. Consults the
exception-management A2A agent (its skills `sla_status` and `find_exceptions`,
read-only by its own card).

## Context

[ADR 0027](./0027-act-slice-human-confirmed-work-release.md) lets a human
release the next work unit after a propose → approve flow. The evidence the
human sees at propose time is the flow-balance recommendation. It does not
include what the *exception hub* knows: cases nobody has picked up, some
already escalated past their SLA. That is precisely the "should I really
release more work right now?" context a person wants, and it lives in a
sibling bounded context.

The fleet's transport rule is: Kafka + CloudEvents carry **facts**, MCP
carries an agent's **tools**, A2A carries **delegation of a goal**, never data
sync. Asking another agent a *question* whose answer is shown once and thrown
away is delegation, not replication, so A2A is the right carrier.

## Decision

### 1. A read-only port with typed results

`ports.ExceptionContext.OpenExceptions(ctx, query) (ExceptionSummary, error)`
is the only method. The summary carries the **open count** and a **bounded
list** of cases (default 5, hard cap 10): `caseId`, `status`, `queue`,
`severity`, `escalated` and a short `reason` (the last segment of the
CloudEvents type that opened the case). The frozen source payload, the audit
trail and the free-text fields of the peer's case are never read into the
summary.

### 2. A second client in the existing outbound A2A adapter

`internal/adapters/outbound/a2a` now holds **two** clients over one shared
transport (`peer.go`): the ADR-0027 write client (`Client`, the single
`WorkReleaser` implementation) and `ExceptionClient`, which implements
**only** `ExceptionContext`. Both use the same `a2a-go/v2` SDK, the same
`otelhttp`-instrumented HTTP client (the trace crosses the A2A hop), the same
real Agent Card discovery, and the same decoding of the peer's in-band
envelope: exactly one of `data` or `problem`, anything else is off-contract.
The SDK stays confined to that one package.

The client sends one skill, `find_exceptions` with `{"status": "OPEN"}`.
`OPEN` means nobody has triaged or assigned the case yet, which is what an
approving human may not know about; triaged and assigned cases already have an
owner. Every failure (connection refused, card or transport error, timeout, a
`problem` answer of any status, an off-contract or inconsistent answer such as
a `count` that disagrees with its `cases`) is the single typed
`ports.ErrExceptionContextUnavailable`; the peer's detail goes to the log, not
to the caller. Ranking is escalated first, then the peer's own order (newest
opened first).

### 3. Wired so it can never change the decision

`ReleaseWork.Propose` runs the lookup **after** the policy decision and the
token are final, under its own **3 s** deadline (`context.WithTimeout`, on top
of the 3 s transport client timeout), and the result is a field on the
returned proposal that nothing reads back. Consequences, each pinned by a test:

- the token is minted or refused exactly as before; refusals (`409`
  not-recommended, `409` incomplete evidence, `400`, `502`) never consult the
  hub and never carry an `exceptionContext`;
- a peer that fails, hangs or answers nonsense yields
  `exceptionContext.status = "unavailable"`; the proposal still succeeds;
- `Approve` never calls the hub: the answer is advisory at propose time only;
- a table test proves the decision and the verified token payload (everything
  but the per-mint random nonce) are identical whether exceptions are present,
  absent, unavailable, timed out or the lookup is disabled.

The cost is bounded: a hung peer can delay a *successful* proposal by at most
3 s. Exception data does not enter the policy, the rationale, the token or the
used-nonce set.

### 4. Response shape

`POST /act/release-next-work/proposals` (200) gains:

```json
"exceptionContext": {
  "status": "ok",
  "summary": {
    "openCount": 17,
    "cases": [
      { "caseId": "case-90a1a2e0", "status": "OPEN", "queue": "problem-solve",
        "severity": "HIGH", "escalated": true, "reason": "CartonDiscrepancyRaised" }
    ]
  }
}
```

`status` is `ok` (summary present; `cases` is `[]` when nothing is open),
`unavailable` (peer failure or timeout; no summary) or `disabled` (no URL
configured; no summary).

### 5. Opt-in configuration

`EXCEPTION_MANAGEMENT_A2A_URL` (optional). Unset (the default) means
`disabled` and behaviour identical to ADR 0027. A set value must be an
absolute `http`/`https` URL with a host; **a malformed value aborts startup**
rather than silently disabling the lookup. It is independent of
`WES_WORK_PLANNING_A2A_URL` and has no effect while the act slice itself is
off (there is no propose route to attach it to).

### 6. The boundaries are re-pinned, not loosened

`internal/architecture/zerowrite/exception_context_test.go` (new) plus a
widened guard in `act_slice_test.go`:

1. the a2a adapter package holds exactly two client types;
2. `ExceptionClient` exports exactly `OpenExceptions`; `Client` exports
   exactly `ReleaseNextWork`;
3. `ports.ExceptionContext` declares exactly `OpenExceptions`;
4. the lookup client asks only allow-listed *read* skills
   (`find_exceptions`, `sla_status`) through a named constant, and never
   references the write skill, the write port or the planning errors;
5. the MCP server, the LLM adapter and the reasoner never reference the
   lookup's port, types, client or option; only the REST adapter, the use case
   and `cmd/agent` do.

All ADR-0027 pins are unchanged and green: one write route pair, the SDK in
one adapter package, exactly one `ReleaseNextWork` declaration beside its
port, no sibling write verbs, the use case reachable from REST and the
composition root only. Every new and every touched pin was mutation-checked
(see the PR).

## Consequences

- A human approving a release sees, in the same response, how many cases sit
  untriaged at the exception hub and the worst few. It is information, not a
  gate: an operator who wants exception data to *block* a release needs a new
  decision and a change to the policy, not this ADR.
- **Warehouse-wide, not per path.** Exception cases carry no process-path id,
  so the question cannot be scoped to the path being released. The block says
  "open exceptions at the hub", and the docs say so.
- No data sync: the summary is returned once and not stored, cached or
  republished. It is not an MCP tool either (an MCP caller is typically a
  model; the lookup exists for the human at the propose step).
- Adds up to one A2A round trip (card discovery once, then one `SendMessage`)
  to a proposal; the peer's response carries full case bodies, so a very large
  open caseload costs transfer time before the 3 s bound applies. Pagination or
  a counts-only skill (`sla_status`, already allow-listed) would be the
  follow-up if that bites.
- **Not authentication**, like every fleet endpoint ([ADR 0006](./0006-fleet-wide-auth-removal.md)).
- **Rejected:** gating or ranking the release on exception data (changes the
  minting rule); calling the hub from `Approve`; caching or persisting the
  answer (data sync); exposing the lookup as an MCP tool or to the reasoner;
  silently disabling the lookup on a malformed URL.
