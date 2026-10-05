---
id: 0005-rest-identity-static-bearer-scopes
title: "0005 — Fleet REST identity: static bearer keys with read/read-write scopes, no IdP"
sidebar_label: "0005 · REST identity (fleet)"
description: The fleet-wide decision to protect every REST surface with the same static bearer key + scope posture the MCP adapters already carry, rolled out through an observe-then-enforce mode, and why an identity provider is deliberately deferred behind an OAuth-ready seam.
---

# ADR 0005: Fleet REST identity — static bearer keys with read/read-write scopes, no IdP

> **Superseded 2026-09-09 by [ADR 0006](./0006-fleet-wide-auth-removal.md).**
> The posture this record describes was removed fleet-wide; this file is
> kept in place as the historical record of why it existed. Do not follow
> this record's Decision/Rollout sections for new work.

## Status

Superseded by [ADR 0006](./0006-fleet-wide-auth-removal.md)

**Date:** 2026-09-07

**Scope:** **fleet-wide.** Recorded here because this service is the fleet's
  cross-context Customer and already documents the console/BFF and MCP
  posture (ADR 0002, ADR 0003). Each context records a one-paragraph
  adoption ADR pointing back to this one.

## Context

The 2026-09-07 ecosystem assessment found that none of the nine REST
surfaces authenticates anything. Every service lists `Authorization` in
its CORS allow-list and none reads it. workforce-management and
labor-performance expose associate shifts, breaks, certifications and
per-associate scorecards — HR-adjacent data — with no identity model at
all. The MCP surfaces, by contrast, have carried a static bearer key with
`read` / `read-write` scopes since each context's ADR-0008, behind an
`Authenticator` seam written to be replaced by an OAuth 2.1 resource
server later.

The owner's decision: **REST adopts the same scheme as MCP.** Static
bearer keys from a Kubernetes Secret, two scopes, no IdP, one middleware
per repository (copy-not-share, per the fleet's no-shared-code rule).

## Decision

1. **One `auth` package per repository**,
   `internal/adapters/inbound/auth`, holding the `Authenticator`,
   `StaticKeyAuth`, `Scope` and `Middleware` types. The MCP adapter
   imports it instead of carrying its own copy, so a repository has
   exactly one implementation serving both surfaces. (The fleet skill
   carries the template and its 98.7%-covered tests.)
2. **Route policy**: `GET`/`HEAD`/`OPTIONS` require `read`; every other
   method requires `read-write`. `/healthz` (and `/metrics` where exposed)
   stay open. `GET /reports/*` on the reports binaries require `read`.
   Failures are RFC 7807 problem details: 401 with `WWW-Authenticate`
   for a missing/invalid credential, 403 for insufficient scope.
3. **Keys**: `API_READ_KEY` / `API_READWRITE_KEY`, falling back to
   `MCP_READ_KEY` / `MCP_READWRITE_KEY`, so one Secret can serve a
   context's REST and MCP surfaces.
4. **`AUTH_MODE=enforce|log|off`**. `log` lets every request through but
   emits `auth: would-reject` with the reason; it is the rollout gate.
   Composition roots default to `enforce` when any key is configured and
   to `off` with a loud WARN when none is, so local development and unit
   tests are unaffected.
5. **Clients**: every outbound REST client adapter in the fleet gains an
   optional bearer (`<SERVICE>_API_KEY`; nil = no header). warehouse-infra
   generates one key per (service, scope) with `random_password` and
   injects each into exactly the callers that need it, following the
   coupling graph: order-management → inventory-storage; inventory-storage
   → facility-layout; wes-work-planning → inventory-storage;
   fulfillment-execution → inventory-storage; workforce-management →
   fulfillment-execution + labor-performance; this service → six reports
   endpoints + four REST endpoints; e2e-tests → everything. Browser MFEs
   call through this BFF where one exists; the two direct `fetch` calls
   in facility-mfe receive a build-time read key — acknowledged as the
   thing an IdP would replace.

### Rollout

1. Per-repo PRs (middleware, router wiring, outbound bearer, chart
   `auth.readKey`/`auth.readWriteKey` + `AUTH_MODE`, 401/403/200 router
   table tests).
2. warehouse-infra: keys generated, `AUTH_MODE=log` fleet-wide, apply,
   soak while grepping for `auth: would-reject`.
3. e2e-tests: bearer on every request.
4. warehouse-infra: `AUTH_MODE=enforce`, apply, e2e must stay 102/102,
   and an unauthenticated probe must return 401.

Rollback at any step is `AUTH_MODE=log` or `off` — configuration only.

## Consequences

**Positive** — every REST call in the fleet is attributable to a scope,
mutations require a distinct credential, and the posture is uniform with
MCP. The change is additive: no handler, use case, or domain type is
touched; the middleware is mounted once per router.

**Negative / accepted** — static keys are not identities: there is no
per-user attribution, no rotation story beyond re-applying Terraform, and
a key in a browser bundle is only as private as the bundle. These are
exactly the gaps an OAuth 2.1 resource-server `Authenticator` closes
later without touching any router; that seam is the reason this decision
is cheap to make now and cheap to supersede.

## Alternatives considered

- **OIDC/JWT now (Keycloak or Dex in warehouse-infra).** The right
  end-state, but it adds an IdP deployment, realm/client configuration
  and token plumbing in eleven repositories before a single request is
  protected. Deferred behind the seam; revisit when per-user attribution
  is actually needed.
- **Gateway-level auth only (Kong key-auth plugin).** Protects the
  north–south edge but leaves every in-cluster service-to-service call
  open, which is where most of the fleet's traffic is. Rejected as the
  sole mechanism; may be layered on top later.
- **A shared Go module for the middleware.** Rejected for consistency
  with the fleet's no-shared-code rule (order-management ADR-0002); the
  template in the fleet skill is the single source, copied per repo.
