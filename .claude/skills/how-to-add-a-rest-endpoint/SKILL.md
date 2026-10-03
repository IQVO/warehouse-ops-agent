---
name: how-to-add-a-rest-endpoint
description: Add or change a REST endpoint in this service in the fleet's hexagonal order (policy rule, use case, port, HTTP adapter, hand-maintained docs/docs/api-surface.md, httptest). Use when touching internal/adapters/inbound/http or exposing a use case over HTTP.
---

# How to add a REST endpoint

Use when asked to add a new REST route to this repo's inbound HTTP
adapter. **Adapted from inventory-storage's `.claude/skills/` reference
(PR #75)** for this repo's very different shape: warehouse-ops-agent is
not a bounded context — it owns no aggregate and enforces no domain
invariant (ADR 0001), so there is no "domain first" step here the way
inventory-storage's guide has one. Every route here is either (a) a
read-only correlation over facts gathered from the eight upstream
contexts' MCP tools, or (b) a console-bff fan-out over plain REST reads
(ADR 0002/0003) — never a place a write can be introduced. This repo also
has **no OpenAPI spec**: its REST surface is hand-documented in
`docs/docs/api-surface.md`, not generated, so the contract step below is
"edit the markdown by hand," not "regenerate from a spec."

This walks the exact path `GET /explain-travel-factor` took
(`internal/application/usecases/explain_travel_factor.go` +
`internal/adapters/inbound/http/router.go`'s `getExplainTravelFactor`,
ADR 0009) as the concrete worked example — read those two files plus
`docs/docs/adr/0009-explain-travel-factor.md` alongside this guide.

## 1. There is no domain step — start at the use case

Check `internal/ports/clients.go` (or `clients_phase2.go`) for whether an
outbound port method already exists for the fact this endpoint needs. If
the endpoint needs a NEW upstream fact, add the method to the relevant
`ports.<Context>Client` interface first (e.g.
`FacilityLayoutClient.EstimateTravelDistance` was added alongside three
existing methods on that same interface for ADR 0009), then implement it
in `internal/adapters/outbound/mcpclient/<context>.go` (calling the
upstream's published MCP tool via `session.callTool`) or
`internal/adapters/outbound/restclient/` (for a console-bff REST fan-out
route). **Never add a Go import of any upstream bounded-context repo** —
`internal/architecture/architecture_test.go`'s
`TestNoDirectDependencyOnBoundedContexts` fails the build the moment
`go.mod`/`go.sum` references any of them. Hand-mirror the response shape
as a `ports.<Noun>` struct field-for-field instead.

If the endpoint needs a new pure correlation/classification rule over
gathered facts (this repo's nearest equivalent of a "domain invariant"),
add it as a small pure function in `internal/domain/policy/` — see
`policy/travel_factor.go`'s `CorrelateTravelFactor`, which classifies an
`EstimateTravelDistance` reading against a documented threshold constant.
Give it its own table-driven unit test before touching the application or
adapter layers, same discipline as a real invariant would get.

## 2. Application: define the use case

Add a new file in `internal/application/usecases/` (one file per use
case — see `explain_travel_factor.go`). Shape:

```go
package usecases

// <Verb><Noun> — one sentence: what read-side capability this
// correlates, and which upstream port(s) it calls. State plainly if it
// deliberately never infers an input the caller must supply (see
// ExplainTravelFactor's doc comment on fromLocationCode/toLocationCode
// — this agent's "never fabricate a fact you don't have evidence for"
// rule, applies to every new use case here).
type <Verb><Noun> struct {
    Upstream ports.<Context>Client   // outbound port(s) only, never a concrete adapter
    Logger   *slog.Logger            // structured warning on upstream failure; defaults to slog.Default()
}

func (uc *<Verb><Noun>) Execute(ctx context.Context, /* caller-supplied args */) (<Verb><Noun>Result, error) {
    // 1. guard: required args present? (reject, never silently default —
    //    see ExplainTravelFactor's empty-location-code check)
    // 2. call the upstream port(s)
    // 3. on error: log a structured warning and degrade to a zero-value
    //    result + the error (ADR-0004's fallback discipline — an
    //    unreachable upstream must never panic or 500 the whole daily
    //    brief); on success, hand the raw reading to a policy
    //    correlation function if one applies
    // 4. return the result
}
```

Write the use case's unit test against a fake port implementation (see
`fakes_test.go`'s `fakeFacility` pattern) — never a real MCP/HTTP call in
a unit test. Cover: the success path, the upstream-error degradation
path, and the missing-required-input rejection path.

## 3. Adapter: wire the HTTP handler

In `internal/adapters/inbound/http/router.go`:

1. Add the route inside `NewRouter` (`r.Get("/path", h.handle<Name>)` —
   this repo's whole surface is `GET`; there is no write route to model
   after, and `internal/architecture/zerowrite/zerowrite_test.go` fails
   the build the moment a mutating HTTP method literal appears anywhere
   in `internal/adapters/outbound/{restclient,mcpclient}`, so don't even
   reach for `POST`/`PUT` here without first re-reading that guardrail).
2. Add the field to `Handlers` (nil-safe — every use-case field in this
   struct is a valid nil, responding `503` rather than panicking; see
   `getExplainTravelFactor`'s `if h.ExplainTravelFactor == nil` guard).
3. Write the handler: decode query params/path params, reject a missing
   required param with `400`, call the use case's `Execute`, encode the
   result to a response DTO and `writeJSON`. DTOs live in `router.go`
   itself in this repo (no separate `dto.go` — small enough surface that
   one file holds router + DTOs; see `travelFactorDTO`).
4. Wire the new use case into the composition root (`cmd/agent/main.go`).

Write at least one `httptest` per endpoint (see
`explain_travel_factor_test.go` in this package): one success path, one
"use case not configured" (nil) path returning `503`, one missing-param
`400` path.

## 4. Contract: update `docs/docs/api-surface.md` by hand

This repo has **no OpenAPI spec and no `docs-api-drift` CI job** — add a
row to the REST table in `docs/docs/api-surface.md` yourself, matching
the existing rows' format (method+path, what it returns, one line on
error/degradation behavior, a link to the ADR if one exists). This is the
one step that most differs from inventory-storage's guide: there, missing
the OpenAPI regen fails CI; here, nothing enforces this file staying in
sync except this convention — treat it as load-bearing anyway (the file's
own intro says "if it drifts from the code, the code is authoritative,"
which is a statement of fact about drift risk, not permission to let it
drift).

## 5. Behaviour: no BDD/godog suite in this repo

Unlike the five bounded-context repos, this repo has no `features/`
directory or `bdd` CI job — its behaviour is proven by the `httptest`
handler tests (step 3) plus, for a console-bff route, `TestGetXxx` cases
covering each upstream's independent degradation (see
`internal/application/usecases/order_lifecycle_test.go` for the
"one context down never 500s the whole response" pattern this repo
expects of every console-bff fan-out).

## 6. Verify before opening the PR

```bash
make check       # fmt-check vet build lint test
make check-all    # + coverage (90% gate on domain/application/inbound) + arch-test
```

`make coverage` gates
`./internal/domain/...,./internal/application/...,./internal/adapters/inbound/...`
at 90% — note `inbound` is IN this repo's coverage gate (unlike a typical
bounded-context repo, since this repo's adapters carry real correlation
degradation logic, not just marshalling). `make arch-test` also runs the
zero-write fitness tests (`internal/architecture/zerowrite`) — a new
route that accidentally reaches for a mutating method fails here before
it ever reaches code review.
