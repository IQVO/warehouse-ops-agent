---
id: 0018-mcp-tool-error-slug-classification
title: "0018 — Classify upstream MCP tool rejections by the fleet's slug convention"
sidebar_label: "0018 · Tool-error slug classification"
description: A validation rejection from an upstream MCP tool (error text "<slug>: detail" with a validation slug) is reported as invalid input (REST 400) on GET /explain-travel-factor; every other rejection, including slug-less text from an older facility-layout, stays a 502.
---

# ADR 0018: Classify upstream MCP tool rejections by the fleet's slug convention

## Status

Accepted (decided 2026-10-06 on the 2026-10-05 audit findings). Refines
[ADR 0016](./0016-rest-error-mapping-cors-and-strict-path-target-config.md)
decision 1 (400 for the caller's own input, 502 for everything upstream) by
giving the agent a principled way to tell the two apart when the *upstream*
is the one that rejected the input. ADR 0016 and
[ADR 0009](./0009-explain-travel-factor.md) are not edited.

## Context

`GET /explain-travel-factor` (and the `explain_travel_factor` MCP tool) hands
two caller-supplied location codes to facility-layout's
`estimate_travel_distance`. When facility-layout rejects them (a malformed or
blank code) its tool answers with an `isError` result. `mcpclient.Session`
turned every such result into a plain error and the REST adapter reported it
as **502**: a client mistake was presented as an upstream outage and misled
operators.

The fleet already has a convention for tool errors. warehouse-planning's MCP
adapter returns the text `<slug>: <detail>`, where `<slug>` is the same stable
slug its REST adapter uses as the last segment of the RFC 7807 `type`, and
unexpected errors are `internal-error: ...`. facility-layout is adopting the
same convention in its own repository (separate PR); until that ships and is
deployed it still returns slug-less prose (e.g. `from and to are both
required`).

## Decision

1. **The discriminator is the slug, never the prose.** `mcpclient` parses a
   leading `^[a-z][a-z0-9]*(-[a-z0-9]+)*: ` from the first text content of an
   `isError` result. No substring or message guessing.
2. **Explicit validation table** (`internal/adapters/outbound/mcpclient/tool_error.go`).
   A slug means *the request was invalid* only if it is:

   | Rule | Examples |
   |---|---|
   | prefix `malformed-` | `malformed-location-code` |
   | prefix `invalid-` | `invalid-site-code`, `invalid-capacity-window` |
   | suffix `-required` | `from-location-required` |
   | exact `validation-failed` | |
   | exact `missing-location-code` | facility-layout's blank/absent location code |

   Everything else is **not** invalid input and keeps today's behaviour
   (502): all `*-not-found`, `duplicate-*`, `already-*`, `*-not-active`,
   `internal-error`, the semantic families (`unknown-*`, `no-route`, ...), any
   unknown slug, **and text with no slug prefix** (an older facility-layout).
3. **Mechanics.** `callTool` returns a `*mcpclient.ToolError` whose `Error()`
   text is byte-for-byte what it was (`<upstream>: tool <tool> reported an
   error: <text>`), exposes `Slug`, and answers `errors.Is(err,
   ports.ErrUpstreamInvalidInput)` for validation slugs only. The new sentinel
   lives in `internal/ports`. `ExplainTravelFactor.Execute` wraps such an error
   as `usecases.ErrInvalidInput` (keeping the upstream error in the chain), so
   the existing REST rule (`ErrInvalidInput` → 400, else 502) and the MCP tool
   (tool error either way) need no other change. Transport failures, timeouts
   and non-error results are never classified as invalid input.
4. **Reuse by sibling clients is automatic and inert.** The helper sits in the
   shared `callTool`, so warehouse-planning's client (which already follows the
   convention) is classified too. No existing status code or message changes:
   its only consumer, the capacity outlook, records the full text as its
   `omittedReason`. Other siblings' text carries no slug and is unaffected.

## Consequences

**Positive** — a blank/malformed location code is a 400 with the upstream slug
in the body; a real outage stays 502 and remains alertable. The rule is
backward compatible with the facility-layout that is deployed today
(slug-less → 502), so the two repositories can ship in either order.

**Negative / contract notes** — the REST contract change is on an error path
of one endpoint only: some requests that returned 502 now return 400, with an
unchanged body shape (`{"error": "..."}`). Ecosystem check (origin/develop of
every fleet repo): no consumer of `/explain-travel-factor` or the
`explain_travel_factor` tool exists outside this repo. The validation table is
a hand-maintained mirror of the upstream slug vocabulary; a new upstream
validation slug outside these patterns is reported as 502 until the table is
extended. No event, migration or Kafka contract is touched.
