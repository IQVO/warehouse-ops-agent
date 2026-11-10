---
id: 0030-act-slice-idempotency-key-store
title: "0030 — Idempotency-Key on every act POST, kept in a process-local store"
sidebar_label: "0030 · act-slice Idempotency-Key"
description: "The four act-slice POST routes adopt the fleet REST guideline's Idempotency-Key contract (400 required/invalid, 409 reused, replay with Idempotency-Replayed: true, 2xx and 4xx stored, 5xx never). This agent has no database, so the key store is an in-process, time- and size-bounded map beside the confirmation token's used-nonce set, under the same single-replica constraint (ADR 0027). Removes the api-lint grandfathering of these routes."
---

# ADR 0030: Idempotency-Key on every act POST, kept in a process-local store

## Status

Accepted (2026-11-10). Applies the fleet REST guideline (warehouse-docs
REST standard, section 2; the same middleware contract as product-master
ADR 0013 and workforce-management ADR 0027/0042) to the act slices of
[ADR 0027](./0027-act-slice-human-confirmed-work-release.md) and
[ADR 0029](./0029-labor-reallocation-act-slice.md). Companion of
[ADR 0031](./0031-approvals-route-replaces-the-approve-verb.md) (the verb
route). Closes issue #211.

## Context

The `api-lint` job runs the fleet `rest-guideline` Spectral rule. The four act
POST routes took no `Idempotency-Key` (rules R3, R5), so `.spectral.yaml`
grandfathered them under per-path `overrides:`. The guideline says every POST
is idempotent: a client may retry after a timeout and the effect happens once.

What this agent did without a key:

- **Approvals** were already at most once: the confirmation token's nonce is
  consumed before delegating (ADR 0027). But a client that lost the response
  and retried got `409 token already used` instead of the answer it missed,
  so it could not tell "done" from "refused".
- **Proposals** write nothing, but a retry minted new tokens; the first ones
  stayed valid until they expired.

The guideline's persistence rule (2.5: the key row commits in the same
database transaction as the aggregate change) assumes a database. This agent
has none, by design: ADR 0001 and ADR 0004 say it owns no aggregate and
persists no state, and ADR 0027 rejected a persisted proposal table for the
same reason. The one piece of process state the act slice keeps is the
used-nonce set, held in memory, bounded by the token lifetime, with the chart
at `replicaCount: 1`.

## Decision

1. **Every act POST is keyed.** `POST /act/release-next-work/proposals`,
   `/act/release-next-work/approvals`, `/labor-reallocation/proposals` and
   `/labor-reallocation/proposals/{proposalId}/approvals` sit behind a
   route-scoped middleware (`internal/adapters/inbound/http/idempotency.go`,
   chi `r.With`). It runs before the handler, so a request without a key is
   refused before any body check:
   - missing header: `400` problem `idempotency-key-required`; a key that is
     not 1-128 printable ASCII characters with a visible first and last
     character, or a repeated header: `400` `idempotency-key-invalid`;
   - fingerprint = method + path + SHA-256 of the body;
   - same key, same fingerprint: the stored answer (status, headers, body) is
     replayed with `Idempotency-Replayed: true` and the handler does not run;
   - same key, other fingerprint: `409` `idempotency-key-reused`, "Idempotency-Key
     was already used with a different request";
   - 2xx and 4xx answers are stored. A 5xx (an unreachable peer, an
     unconfigured slice) is never stored, so the retry runs again. For an
     approval that retry still cannot delegate twice: the token was consumed
     before the first delegation, so it answers `409 token already used`.
2. **The store is process-local.** `ports.IdempotencyStore` is implemented by
   `internal/adapters/outbound/memory`: a map guarded by a mutex, serialised
   per key (a second request with an in-flight key waits for the first and
   replays its answer; another fingerprint is refused at once). Retention is
   24 hours (the guideline minimum) and capacity 2048 answers; when full, the
   oldest stored answer is evicted first. `cmd/agent` wires it; a router built
   without a store answers `503` on the keyed routes rather than skipping the
   check.
3. **Spec.** `components/parameters/IdempotencyKey` is `required: true` (the
   store is wired in every runtime mode). Its pattern describes the wire value,
   the key plus optional trailing SP/HTAB that the HTTP stack drops before the
   middleware reads it: `^[!-~]([ -~]{0,126}[!-~])?[ \t]*$`, `minLength: 1`
   (the workforce-management ADR 0042 rule; pinned here by
   `TestValidIdempotencyKey_SpecBoundary` and
   `TestIdempotencyKeyWire_TrailingOWSIsStrippedBeforeTheMiddleware`). Every 2xx
   documents `Idempotency-Replayed`; the 400 and 409 responses name the key
   problems. The four per-path `overrides:` are deleted from `.spectral.yaml`.

## Consequences

- A retried approval with its key gets the original `200` instead of `409`; a
  retried proposal gets the same tokens. A client must now send a key on every
  act POST. No caller in the fleet calls these routes (swept across every
  repo's `develop`/`main`: warehouse-console, e2e-tests, provisioning, the
  floor client and the rest), and CORS keeps browsers off them (ADR 0027).
- **Process-local, like the nonce set.** A restart forgets the keys, and a
  retry after a restart runs again. With the default ephemeral signing key that
  answers `400 invalid token` (the key changed with the process). With a pinned
  `APPROVAL_SIGNING_KEY` the restart has also forgotten the used nonces, so a
  token still inside its TTL could be approved a second time: that limit comes
  from ADR 0027 and this ADR neither adds nor removes it. With more than one
  replica keys are not shared, so the chart must keep `replicaCount: 1`, the
  constraint ADR 0027 already sets.
  A shared store needs a database, which this agent does not have (ADR 0001);
  that would be a new ADR.
- **Bounded memory.** At most 2048 stored answers of a few KiB each (request
  bodies are capped at 8 KiB). With no authentication fleet-wide (ADR 0006),
  a client sending many new keys can push an answer out before 24 hours; the
  only effect is that its retry runs again, still under the single-use token.
- Deviation from guideline 2.5, stated: there is no transaction for the key to
  share because nothing is persisted. The act's only effect is the delegated
  A2A call, and the token, not the key, is what keeps it at most once.
- Rejected: a Postgres key table (makes this agent stateful, contradicts ADR
  0001 and ADR 0027); keeping the grandfathering (the spec would keep
  describing a contract the fleet forbids); an optional key (the guideline
  requires it, and a half-keyed API leaves clients guessing).
