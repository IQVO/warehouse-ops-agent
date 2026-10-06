---
id: 0016-rest-error-mapping-cors-and-strict-path-target-config
title: "0016 — REST error mapping for explain-travel-factor, GET-only CORS, and strict DAILY_BRIEF_PATH_TARGETS config"
sidebar_label: "0016 · REST errors, CORS, strict config"
description: Records three small behaviour corrections found by the 2026-10-05 docs audit — upstream facility-layout failures on GET /explain-travel-factor are 502 not 400, CORS allows only GET/OPTIONS, and a malformed DAILY_BRIEF_PATH_TARGETS fails startup instead of silently using the default.
---

# ADR 0016: REST error mapping, GET-only CORS, and strict path-target config

## Status

Accepted. Does not supersede any earlier record; it refines behaviour that
[ADR 0009](./0009-explain-travel-factor.md), [ADR 0002](./0002-micro-frontend-console-architecture.md)
and [ADR 0015](./0015-core-flow-balance-and-daily-brief-adoption.md) left
implicit.

## Context

The 2026-10-05 documentation refresh compared the code against its docs and
found three places where behaviour was wrong or surprising, and a fourth
where the code and an accepted ADR disagreed in the opposite direction.

1. `GET /explain-travel-factor` returned **HTTP 400** for every error from
   `usecases.ExplainTravelFactor.Execute`. That includes facility-layout
   being unreachable or failing, which is an upstream degradation, not a
   mistake by the caller.
2. The CORS middleware allowed `POST`, `PUT` and `DELETE`, although every
   route of this agent is a read-only `GET` (ADR 0006, governance note).
3. `config.loadPathTargets` silently fell back to the built-in default
   target when `DAILY_BRIEF_PATH_TARGETS` was set but not valid JSON. An
   operator typo therefore produced a healthy-looking brief about the wrong
   path.
4. `getOrderLifecycle` carried a `502 Bad Gateway` branch that could never
   run: `OrderLifecycle.Execute` returns only `ports.ErrNotFound`, and every
   other order-management error degrades that stage to `null`, as ADR 0002
   specifies ("one context being unreachable degrades that one stage to
   absent, never a … failure for the whole response").

## Decision

1. **Classify `ExplainTravelFactor` errors.** The use case wraps the
   missing-location-code rejection in a new sentinel,
   `usecases.ErrInvalidInput`. The HTTP adapter maps an error that wraps
   `ErrInvalidInput` to **400** and every other error to **502**. The MCP
   tool is unchanged (it returns the error as a tool error either way).
2. **CORS allows `GET` and `OPTIONS` only.** `CORS_ALLOWED_ORIGINS` and the
   allowed headers are unchanged.
3. **A set-but-malformed `DAILY_BRIEF_PATH_TARGETS` is a config error.**
   `config.Load` now returns `(Config, error)`; `cmd/agent` returns the error
   and the process exits non-zero before serving. An unset variable (and an
   explicitly empty array) still selects the built-in default target, and the
   Helm chart's empty default is unaffected.
4. **Keep ADR 0002's degradation for the order lifecycle** and remove the
   unreachable 502 branch rather than "surfacing" order-management failures
   as 502. The handler keeps a defensive 500 for any future widening of
   `Execute`'s error contract. A test pins that an unreachable
   order-management still yields a 200 with `orderManagement: null` and the
   other stages intact.

## Consequences

**Positive** — clients and dashboards can tell "you sent a bad request" (400)
from "facility-layout is down" (502) and alert on the latter. The CORS
surface matches the read-only posture. A bad path-target override is caught
at deploy time, in the rollout, instead of in a wrong brief.

**Negative / contract notes** — this is a REST contract change on an error
path only: a caller that treated *any* 4xx from `/explain-travel-factor` as
"my input is wrong" will now see a 5xx for upstream failures, which is the
point; success responses and all other routes are unchanged. Browsers can no
longer make cross-origin non-GET requests to this agent; none exist.
Deployments that carry a malformed `DAILY_BRIEF_PATH_TARGETS` will now crash
on start until it is fixed — intended, but visible in a rollout.

**Open product question (not decided here)** — whether an unreachable
order-management should instead fail the whole order-lifecycle response
(502) when it is the *only* stage that would otherwise be empty. ADR 0002
says no; this ADR leaves that rule unchanged.
