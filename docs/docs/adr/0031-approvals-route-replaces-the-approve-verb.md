---
id: 0031-approvals-route-replaces-the-approve-verb
title: "0031 — POST .../proposals/{proposalId}/approvals replaces the /approve verb route"
sidebar_label: "0031 · approvals replaces /approve"
description: "The labor-reallocation approval moves from the verb route POST /labor-reallocation/proposals/{proposalId}/approve to the transition resource .../approvals (fleet REST guideline 1.3d/1.4, approve -> approvals). The old route keeps working, keyless and unchanged, as a deprecated alias that answers Deprecation, Sunset (2027-02-10) and Link rel=successor-version on every response, and is left out of the OpenAPI contract."
---

# ADR 0031: `POST .../proposals/{proposalId}/approvals` replaces the `/approve` verb route

## Status

Accepted (2026-11-10). Amends the route table of
[ADR 0029](./0029-labor-reallocation-act-slice.md) §2. Companion of
[ADR 0030](./0030-act-slice-idempotency-key-store.md) (the Idempotency-Key).
Closes the R2 finding of issue #211.

## Context

The fleet REST guideline forbids a verb as a path segment (1.2, Spectral rule
R2). A lifecycle transition is a POST that creates a transition resource named
by the fixed noun of the act (1.3d), and the fixed noun of `approve` is
`approvals` (1.4). ADR 0029's approval route ended in `/approve`; the release
slice of ADR 0027 already used `/act/release-next-work/approvals`.

A caller sweep over every repo's `develop` (or `main`) found no caller of the
`/approve` route outside this repo: warehouse-console, e2e-tests,
provisioning, warehouse-floor-client, warehouse-infra and warehouse-docs only
name the `/labor-reallocation/*` prefix (chart comments, configuration page),
never this path. CORS keeps browsers off the act routes (ADR 0027), so a
browser caller cannot exist. Human operators with `curl`, and scripts we cannot
see, can.

The guideline's cutover section prefers a hard cutover with no aliases. That
rule was written for the fleet-wide set that merged and deployed together. This
is a single route changing on its own, so a short announced window lets an
unseen caller move without a broken approval.

## Decision

1. **New route.** `POST /labor-reallocation/proposals/{proposalId}/approvals`
   is the approval. Same handler, same use case, same body
   `{token, approvedBy}`, same statuses and response body; operationId
   `postReallocationApproval` is unchanged. Like every act POST it takes an
   `Idempotency-Key` (ADR 0030).
2. **Deprecated alias.** `POST .../{proposalId}/approve` still answers,
   unchanged and **keyless**, so a caller that has not moved keeps working. It
   is wrapped by `deprecatedRoute` (`internal/adapters/inbound/http/deprecation.go`),
   which sets on every answer, errors included:
   - `Deprecation: @1794268800` (RFC 9745: deprecated since 2026-11-10T00:00:00Z);
   - `Sunset: Wed, 10 Feb 2027 00:00:00 GMT` (RFC 8594: may stop answering after);
   - `Link: <approvals>; rel="successor-version"`. The reference is relative,
     so it resolves to the new route behind any gateway prefix.

   The new route carries none of these headers.
3. **Not in the contract.** `apis/openapi.yaml` lists only the guideline
   route; the operation's description names the deprecated path and its Sunset.
   The `.spectral.yaml` exemption for the old path is deleted, not moved. This
   is the one router route the spec does not list, and it stays out until it is
   removed.
4. **Removal.** After the Sunset date a later change deletes the alias, its
   entry in `allowedWriteRoutes` (`internal/architecture/zerowrite/act_slice_test.go`)
   and its rows in the BDD feature, with no new ADR needed. The zero-write fitness
   test lists both paths until then: the alias is the same write, not a third
   one.

## Consequences

- `api-lint` passes with no exemption for any act route.
- A caller of `/approve` sees the deprecation headers now and has until
  2027-02-10 to move. Because the alias is keyless, it does not get
  Idempotency-Key replays; moving to `/approvals` is how a caller gets them.
- For the alias's lifetime the router serves one POST the guideline would
  reject (verb segment, no key). That is the cost of not breaking an unseen
  caller.
- The chart's `/labor-reallocation` HTTPRoute is a path prefix, so both paths
  stay behind the read-only CORS plugin with no chart change.
- Rejected: hard removal now (cheapest, but an unseen operator script would
  break with no warning); keeping the old path in the spec as
  `deprecated: true` (R2 would need its exemption back, which is what #211
  removes); `PUT .../approval` (approving is a transition, not setting an
  attribute; the guideline's decision order lands on a transition resource).
