---
id: 0002-micro-frontend-console-architecture
title: 0002 — Micro-frontend console architecture over per-service REST, with a thin BFF for cross-service reads
sidebar_label: 0002 · Micro-frontend console
description: Why the warehouse-systems fleet gets one cross-cutting operator console built as Module Federation micro-frontends over each service's own REST API, plus a Backend-for-Frontend hosted here rather than as a new bounded context.
---

# ADR 0002: Micro-frontend console architecture over per-service REST, with a thin BFF for cross-service reads

## Status

Accepted

**Date:** 2026-08-29

## Context

Every bounded context in the fleet (`order-management`, `inventory-storage`,
`wes-work-planning`, `fulfillment-execution`, `workforce-management`,
`facility-layout`) ships a REST API and, per ADR-0001 in each sibling repo,
an MCP Open Host Service for AI-ecosystem consumption. Nothing served a
human operator wanting to look at the system's state in a browser — no
UI existed anywhere in the fleet before this record.

The requirement (owner-stated): a single-page React application, and one
capability was explicit and non-negotiable — an operator must be able to
trace **one order's entire lifecycle** across every context that touches
it, from intake through pack. Order-management's own aggregate stops at
`Received`/`Allocated`/`Released`; whether that order's demand actually
got reserved, planned into a work unit, and picked/packed lives in three
other services' own data, each owned and stored by that service alone.

The forces:

- **No service reads another's database, ever.** This is the fleet's
  oldest and least negotiable rule (see each context's own hexagonal-
  architecture ADR-0001). A UI that answers "what happened to order X"
  cannot be built by querying inventory-storage's Postgres from an
  order-management screen, or by any shared read replica.
- **Bounded-context ownership must extend to the frontend, not stop at
  the API.** If one team (repo) owns `inventory-storage`'s domain and
  REST API, that same repo should own the *screen* that shows
  inventory-storage's own data — otherwise a change to inventory-storage's
  response shape becomes a cross-repo coordination problem for whichever
  team maintains a monolithic frontend, recreating the exact coupling
  the backend split was meant to avoid.
- **Six independently deployable backends means the natural UI shape is
  six independently deployable frontends.** A single frontend repo owned
  by nobody in particular (or by whichever team touched it last) inverts
  the fleet's entire organizing principle.
- **Exactly one screen is genuinely cross-cutting.** The Order Lifecycle
  view is not owned by any single context — it is a read model over facts
  from four of them. Somewhere has to assemble it, and "the browser calls
  four services directly and merges the results client-side" was
  evaluated and rejected (see Decision).
- **No service had ever needed a browser client before.** None of the six
  had CORS middleware; a same-origin assumption was implicit everywhere.
- **Three of the four order-lifecycle stages had no lookup-by-order-
  reference endpoint at all.** `inventory-storage`'s `Reservation`,
  `wes-work-planning`'s `WorkUnit`, and `fulfillment-execution`'s `Task`
  each already carry the join key in their own domain (`DemandRef`,
  `reference` from `EnqueueWorkUnit`, `OrderRef`), but none exposed a GET
  endpoint to look up "everything with this key" — every existing
  endpoint was scoped to a single stock unit, a single station, or a
  single reservation by its own ID.
- **The join key is not uniform across all three hops.** Reading the
  actual Kafka producer code (not assuming from the field names) showed
  `inventory-storage`'s `Reservation.DemandRef` is the plain order ID,
  and `wes-work-planning`'s `WorkUnit.reference` (from `EnqueueWorkUnit`)
  is also the plain order ID — but `fulfillment-execution`'s
  `Task.OrderRef` is actually stamped from the **WorkUnit's own composite
  id** (`<orderId>-line-<lineNo>`), because that is what flows through
  the `WorkReleased` Kafka payload as `work_unit_id`. A naive "join
  everything by the plain order id" design would have silently returned
  empty results for the fulfillment stage on every multi-line order.

## Decision

**We will build one micro-frontend per bounded context (Module Federation
remote, owned and versioned inside that context's own repo), composed at
runtime by a separate shell repo, with the one genuinely cross-cutting
screen backed by a thin Backend-for-Frontend hosted inside
`warehouse-ops-agent` rather than as a new bounded context — and we will
add exactly the minimal additive GET-by-reference endpoint each upstream
service was missing, rather than route around the gap with mocks or a
shared read model.**

### One remote per bounded context, in that context's own repo

Each service repo gains a `web/` directory (e.g.
`order-management/web/`, `inventory-storage/web/`, and so on for all
six) containing a Vite + React Module Federation **remote**: `order-mgmt-
mfe`, `inventory-mfe`, `planning-mfe`, `fulfillment-mfe`, `workforce-mfe`,
`facility-mfe`. Each remote:

- Talks **only** to its own service's REST API (`SERVICE_API_BASE`), same
  as any other REST client — no new backend surface beyond the console-
  bff exception below.
- Ships its own screen(s) for that context's own operator workflows
  (e.g. `fulfillment-mfe`'s queue-depth dashboard and task-by-orderRef
  lookup) — decisions about what that screen shows belong to that
  context's own team/PR, exactly like its REST API does.
- Is built, tested, and released inside that repo's own CI, on that
  repo's own schedule. A change to `fulfillment-mfe` requires no
  coordination with any other repo.

### A separate shell repo, not embedded in any bounded context

`warehouse-console` is a **new**, seventh-ish repo (alongside
`warehouse-ui-kit`) — deliberately not folded into any of the six
contexts, for the same reason `warehouse-ops-agent` itself is a separate
repo (ADR-0001): composing six independently-owned things is not itself
any one of those six things' responsibility. It is the Module Federation
**host**: owns routing, top navigation, and lazy-loads each remote by
its published `remoteEntry.js` URL. It contains **zero business logic**
for any bounded context — if a screen needs new domain logic, that logic
belongs in the owning service's remote, not the shell.

### `@warehouse/ui-kit`: a shared design system, also its own repo

A third new repo, consumed by the shell and all six remotes via
`file:../warehouse-ui-kit` (no npm registry yet — pre-1.0, single
workspace). It is the **one place** that decides token values (color,
spacing, type) and, critically, how a domain status renders — the exact
same `Order.Status`, `Task.Status`, `Reservation` state, etc. that each
service's own domain enum defines, mapped once to a color/tone in
`StatusPill`, so an operator sees the same visual language moving between
`order-mgmt-mfe` and `fulfillment-mfe`. A remote hand-rolling its own
palette instead of consuming this is treated as a bug, not a style
choice — visual consistency across a micro-frontend boundary is the
single most common way this architecture fails in practice.

### The Order Lifecycle screen: a BFF, not a client-side fan-out

The browser does **not** call four services directly and merge results
client-side. Three alternatives were considered:

- **Browser fans out to 4 services directly.** Rejected: this is 4
  round-trips of network latency stacked in the client, requires every
  one of those 4 services to expose CORS to a public origin permanently
  (not just to the shell's dev/prod origin), and pushes the join-key
  translation (the WorkUnit-id detail above) into frontend code, which
  is the wrong layer for a fact about how two backend services relate.
- **A new bounded-context service** owning the order-lifecycle read
  model. Rejected for the same reason ADR-0001 rejected embedding
  correlation logic in an existing context: nothing about "assemble 4
  services' facts into one read model for a UI" has an aggregate,
  an invariant, or persisted domain state of its own — it fails the
  bounded-context test the same way `warehouse-ops-agent`'s own
  correlation logic did.
- **Host it inside `warehouse-ops-agent`.** Chosen. This repo already
  exists precisely as the fleet's cross-context correlation surface
  (ADR-0001), already has an established "Customer of everything, owns
  nothing" hexagonal shape, and already runs a CI/quality gate identical
  to the six contexts'. The BFF is a **second, separate use case family**
  inside it — `internal/adapters/outbound/restclient/` (plain REST HTTP
  clients) sits beside the pre-existing
  `internal/adapters/outbound/mcpclient/` (MCP tool-calling clients) —
  deliberately not unified, because they answer different callers'
  different questions (an LLM host's "what needs attention" vs. a
  browser's "what happened to order X").

`GET /console/orders/{id}/lifecycle` fans out to `order-management`,
`inventory-storage`, `wes-work-planning`, and `fulfillment-execution`
**sequentially** (v1: simple; four sub-second local calls — parallelizing
was deliberately deferred, not forgotten) and returns whatever it could
gather. One context being unreachable degrades that one stage to absent,
never a 500 for the whole response — mirroring `FlowBalanceAdvisory`'s
existing partial-tolerant orchestration pattern in this same repo.

### The join keys, resolved per hop, verified against the producer code

The BFF's fan-out uses the **actual** key each downstream service
expects, confirmed by reading the Kafka producer/consumer code, not
assumed from field-name similarity:

1. `order-management`: `GET /orders/{id}` — the order id itself.
2. `inventory-storage`: `GET /reservations?demandRef={id}` — same plain
   order id (`DemandRef` is set to `o.ID()` at allocation time).
3. `wes-work-planning`: `GET /work-units?reference={id}` — same plain
   order id (`Reference: data.OrderId` on `EnqueueWorkUnit`).
4. `fulfillment-execution`: `GET /tasks?orderRef={workUnitId}` — **not**
   the order id. Each WorkUnit's own composite id
   (`<orderId>-line-<lineNo>`), because `Task.OrderRef` is stamped from
   the `WorkReleased` Kafka payload's `work_unit_id` field. The BFF's use
   case therefore calls step 3 first to discover each line's WorkUnit id,
   then queries fulfillment-execution once per WorkUnit — an order with
   3 lines makes 3 fulfillment-execution calls, not 1.

### The three missing GET endpoints: additive, minimal, one per service

Rather than build the BFF against mocks or pause until a bigger read-
model project happened, each service gained exactly one new,
side-effect-free GET endpoint, each shipped as its own PR into that
service's own repo, following the same port → use case → adapter →
handler → OpenAPI → tests shape as every other endpoint in that service:

- `inventory-storage`: `GET /reservations?demandRef=` →
  `ports.ReservationRepo.FindByDemandRef`. Returns an array (a demandRef
  can have multiple reservations across its lifetime — revoked, then
  retried).
- `wes-work-planning`: `GET /work-units?reference=` → returns every
  WorkUnit enqueued against that reference, array-shaped, for the same
  reason.
- `fulfillment-execution`: `GET /tasks?orderRef=` →
  `ports.TaskRepo.FindByOrderRef`. Returns every task for that WorkUnit
  id, including retried legs.

No existing endpoint, domain type, or use case was modified to build
these — each is a new file alongside the existing repository-adapter
pattern in its own service.

### CORS: additive middleware, not a gateway

Each of the four services touched by the console gained a `go-chi/cors`
global middleware (`CORS_ALLOWED_ORIGINS` env var, default
`localhost:5173` plus that service's own remote's dev port; GET/POST/
PUT/DELETE; no credentials). This was added directly to each service's
existing HTTP adapter, not via a shared API gateway or reverse proxy —
consistent with "no new infrastructure layer for a browser client that a
plain per-service CORS header already solves."

## Consequences

### Easier

- **Ownership stays aligned with the backend split.** The team (or
  future contributor) who owns `inventory-storage`'s domain also owns
  its screen. A schema change to that service's response and the screen
  that renders it land in the same PR, same repo, same review.
- **Independent deployability, all the way to the UI.** Any one remote
  can ship, roll back, or go dark without touching the shell or any
  sibling remote — `RemoteBoundary`'s error boundary renders an inline
  "unavailable" card for a down remote rather than white-screening the
  whole console.
- **The BFF's partial-tolerance mirrors an already-proven pattern.**
  `FlowBalanceAdvisory` established "gather what you can, degrade
  per-stage, never fail the whole response" in this repo before the
  console existed; the Order Lifecycle use case reuses that shape rather
  than inventing a new failure model.
- **The three new GET endpoints are genuinely minimal.** Each is a
  single side-effect-free read, additive to its owning service's own
  hexagon, reviewed and tested to that service's own ≥90% bar — none of
  the four upstream domains changed.
- **Visual consistency is enforced by a shared package, not a style
  guide document.** `@warehouse/ui-kit`'s `StatusPill` makes "the same
  status renders identically everywhere" a compile-time consumption
  choice, not a convention someone has to remember.

### Harder

- **A ninth and tenth repo join the fleet** (`warehouse-console`,
  `warehouse-ui-kit`), neither of which is a bounded context and both of
  which needed their own CI/GitFlow setup from scratch — a real, if
  one-time, hygiene gap this record's own delivery had to catch and fix
  (both existed only as local, ungoverned checkouts for part of this
  work before being made real repos).
  `@warehouse/ui-kit`'s consumption via `file:../warehouse-ui-kit`
  (not an npm registry publish) means every consumer's CI must check out
  ui-kit as a sibling directory and build it first — a real coupling
  this decision accepts for now and defers past pre-1.0.
- **The order-lifecycle join logic is genuinely non-uniform** across the
  three downstream hops (plain order id for two, a derived WorkUnit id
  for the third) and that asymmetry lives in the BFF's use case, not
  anywhere self-evident from any one service's own docs — this record
  and the BFF's own doc comments are the only place that fact is written
  down.
- **CORS is now permanent surface on four services that never needed
  it before.** Each origin allow-list must be kept current as new
  remote dev ports or a real deployed console origin are added; a
  forgotten update is a silent "Failed to fetch" in the browser, not a
  loud backend error.
- **Six remotes plus a shell is six times the frontend build/release
  surface** of a single SPA — more CI to keep green, more places a
  shared-dependency version mismatch (`react`, `react-dom`,
  `@warehouse/ui-kit` as Module Federation shared singletons) can produce
  a runtime error that only appears when remotes are composed together,
  not when any one is built alone.
- **This BFF endpoint is sequential, not parallel**, by deliberate v1
  scope-cut — an order touching all four stages pays the sum of four
  (or more, for multi-line fulfillment lookups) round-trip latencies,
  not the max. Parallelizing the fan-out is a known, deferred follow-up,
  not an oversight.
