---
id: 0033-act-problem-types-have-a-replay-class
title: "0033 — Every act problem type has a replay class; a RETRYABLE refusal is not stored"
sidebar_label: "0033 · replay classes on the act catalogue"
description: "The fleet replay-class rule (owner decision 2026-11-18) on the act slice: each problem type answerable on a keyed route declares FINAL, OUTCOME or RETRYABLE on its catalogue entry; the middleware stores 2xx, FINAL and OUTCOME answers and releases the key for RETRYABLE ones. The two approvals' post-consume peer refusals are the fleet's first real OUTCOME entries."
---

# ADR 0033: Every act problem type has a replay class

## Status

Accepted (2026-11-21). Adopts the fleet rule
`.claude/rules/fleet/idempotency-and-outbox.md` (owner decision 2026-11-18;
the standard is `docs/idempotency-replay-classes.md` in
warehouse-harness-template) on the four keyed POST routes of ADR 0030.
Supersedes the "2xx and 4xx answers are stored" sentence of
[ADR 0030](./0030-act-slice-idempotency-key-store.md) (its Decision text is
untouched; see its post-decision note). Reference test:
`internal/adapters/inbound/http/idempotency_replay_classes_integration_test.go`,
rendered verbatim from the harness template with the memory-store wiring in
`idempotency_replay_classes_wiring_integration_test.go`.

## Context

ADR 0030 made every act POST keyed and stored every answer that was not a
5xx, refusals included. The fleet rule changes that: a stored refusal that a
retry could pass pins a false answer for the whole retention window. The
propose routes refuse on live peer state (a recommendation that can change
between two identical requests); replaying such a refusal for 24 hours would
answer "not recommended" to a client that did the right thing and retried.

This service has no database: the key store is the process-local memory
store of ADR 0030 (`internal/adapters/outbound/memory/idempotency_store.go`),
serialised per key, retention 24 h, capacity 2048, oldest evicted first, and
expired answers are refused on replay and purged (`claim`/`makeRoom`). A
restart loses every stored answer and a second replica would not share them
(facts of ADR 0030/ADR 0027; the chart keeps `replicaCount: 1`). This ADR
does not change the store: the class rule governs WHAT is stored per answer,
not where.

## Decision

1. **Every problem type answerable on a keyed route declares exactly one
   replay class on its catalogue entry** — the act problem catalogue of
   `internal/adapters/inbound/http/act_problems.go` (the status switch
   `writeActErrorFor` held, reshaped into one literal per problem type; the
   wire answer is unchanged: same statuses, same titles, same details, the
   status-derived RFC 9457 type URIs of `problem.go`). The slug of an entry
   is the catalogue and ADR identifier, NOT a wire value. A problem type
   without a class fails a test; there is no runtime default.

2. **The classes:**

   | Problem type (slug) | Class | Status |
   |---|---|---|
   | `invalid-input` | FINAL | 400 |
   | `invalid-token` | FINAL | 400 |
   | `unsupported-action` | FINAL | 400 |
   | `proposal-mismatch` | FINAL | 400 |
   | `assignment-invalid` | OUTCOME | 400 |
   | `token-expired` | FINAL | 410 |
   | `not-recommended` | RETRYABLE | 409 |
   | `incomplete-evidence` | RETRYABLE | 409 |
   | `token-already-used` | FINAL | 409 |
   | `no-pending-work` | OUTCOME | 409 |
   | `not-understaffed` | RETRYABLE | 409 |
   | `no-reallocation-move` | RETRYABLE | 409 |
   | `assignment-rejected` | OUTCOME | 409 |
   | `unknown-work-planning-path` | OUTCOME | 404 |
   | `assignment-not-found` | OUTCOME | 404 |
   | `work-planning-unreachable` | RETRYABLE | 502 |
   | `workforce-agent-unreachable` | RETRYABLE | 502 |
   | `idempotency-key-required` | FINAL | 400 |
   | `idempotency-key-invalid` | FINAL | 400 |
   | `idempotency-key-reused` | FINAL | 409 |
   | `unsupported-media-type` | FINAL | 415 |
   | `malformed-body` | FINAL | 400 |
   | `request-body-too-large` | FINAL | 413 |
   | `idempotency-store-not-configured` | RETRYABLE | 503 |
   | `idempotency-key-wait-cancelled` | RETRYABLE | 503 |
   | `slice-not-configured` | RETRYABLE | 503 |
   | `upstream-unavailable` | RETRYABLE | 502 |

   A test holds this table equal to the catalogue
   (`replay_class_catalogue_test.go`).

3. **OUTCOME, the first real ones in the fleet.** An approval consumes the
   confirmation token's nonce BEFORE delegating — `ReleaseWork.Approve`
   (usecases/release_work.go: `consume(p)` wins, then
   `releaser.ReleaseNextWork` returns the refusal) and
   `LaborReallocation.Approve` (usecases/labor_reallocation.go: `consume(r)`
   wins, then `assigner.AssignLabor` returns the refusal). The nonce entry is
   in-process state that survives the request: after the refusal the token is
   spent, and a re-execution under a new key can only answer
   `token-already-used`. That is exactly the OUTCOME definition — the command
   committed state (here the used-nonce entry, not a database row) and then
   answered an error that must be remembered. The peer-refusal sentinels
   mapped after the delegation are OUTCOME: `no-pending-work`,
   `unknown-work-planning-path` (a2a/client.go maps both from the peer's
   in-band problem), `assignment-rejected`, `assignment-not-found`,
   `assignment-invalid` (a2a/labor.go `mapLaborProblem`). Proven on the real
   routes by `act_replay_classes_test.go`: after the refusal a second
   execution under a new key answers `token-already-used` (the state exists)
   and a replay under the original key answers the stored refusal byte for
   byte with `Idempotency-Replayed: true` while the peer is called once (a
   replay adds nothing). There is no transaction to join: the commit is the
   use case's own in-process step, so "the writes commit with the stored
   answer" is a fact of this design, not a middleware action.

4. **RETRYABLE releases the key.** The propose routes write nothing and
   refuse on live peer state (`not-recommended`, `incomplete-evidence`,
   `not-understaffed`, `no-reallocation-move`), and every 5xx (peer
   unreachable, a slice or the store not configured, the unmatched-error
   fallback) is never stored. The production error writer marks the
   middleware's capture with the entry's class (`replay_class.go`); the
   middleware stores 2xx, FINAL and OUTCOME answers and returns
   `ports.ErrResponseNotStored` for a RETRYABLE one — the exact path of its
   5xx branch — so the key is released, nothing is compared on the next use
   of that key, and the retry executes again. A refusal that reaches the
   capture with no class is released the same way: the safe fallback.

5. **FINAL answers** (request validation, token verification and expiry, the
   token replay refusal, the key problems, body decoding) are stored and
   replayed as before. `token-already-used` is FINAL, not OUTCOME: the
   request that answers it committed nothing itself (an earlier request
   consumed the nonce), and the answer is one-way stable for the token's
   life — a stored replay beats re-executing.

6. **Concurrent duplicates.** The store serialises per key. When the first
   request ends RETRYABLE (or 5xx), the waiter finds nothing stored, claims
   the key fresh and EXECUTES the request itself — it does not receive the
   first refusal. When the first request ends FINAL/OUTCOME/2xx, the waiter
   replays the stored answer.

## Consequences

- A client that retries a proposal refusal under the same key now gets a
  FRESH execution (and can get a 200 once the peer state changed) instead of
  the stored 409. A different body under a released key is a NEW request,
  not `idempotency-key-reused`.
- No caller in the fleet calls the act routes (ADR 0030's sweep), and no
  caller derives a key deterministically from an id, so no consumer change
  rides along; consumer-side retry behaviour is out of scope by the fleet
  rule.
- The MCP surface needs no class: every tool this agent serves is read-only
  (`internal/architecture/zerowrite`), and the A2A delegations are outbound
  calls to the peers' surfaces, where the peers' own rules apply.
- The store's limits are unchanged and remain those of ADR 0030:
  process-local, lost on restart, not shared across replicas, 24 h / 2048
  entries. The class table does not change them; it changes only what is
  stored per answer.

## Judgement calls

1. The approvals' post-consume peer refusals are OUTCOME (the commit is the
   in-process nonce consumption; no rollback exists to undo it, and
   re-execution is impossible by construction).
2. `token-already-used` is FINAL, not OUTCOME: the refusing request commits
   nothing; the answer is stable for the token's remaining life.
3. `unknown-work-planning-path` and `assignment-not-found` are OUTCOME even
   though the fleet 404 default is RETRYABLE: the nonce is spent before the
   peer answers, and the commit-then-refuse ordering dominates the 404
   heuristic (this service does not mint path ids either, which would have
   made them FINAL-only under the default rule).
4. `assignment-invalid` (400, the peer refusing the arguments) is OUTCOME:
   it is mapped from the peer's in-band answer after the delegation
   (a2a/labor.go), i.e. after the nonce was consumed — not a local
   pre-validation.
5. `token-expired` is FINAL: expiry only moves one way for a token.
6. The entry slugs are catalogue identifiers only; the wire keeps the
   status-derived problem type URIs (no new wire slugs, byte-identical
   answers).
