---
name: how-to-add-an-mcp-tool-call
description: Add a read-only MCP tool call to this agent following the fleet zero-write rule. Use when touching the outbound MCP client adapters.
---

# How to add an MCP tool call

**Renamed and rescoped from inventory-storage's `.claude/skills/
how-to-add-an-integration-event.md` (PR #75).** That guide covers
publishing/consuming Kafka integration events — this repo has **no Kafka
consumer or publisher of its own, and no AsyncAPI spec**. This
repo's actual cross-context integration mechanism is different in kind:
it is an MCP **Customer** of sibling contexts' published Open Host
Services (ADR 0001; eight outbound clients since ADR 0007), calling their read-only tools over Streamable HTTP.
This guide replaces the Kafka how-to with the equivalent real workflow
for this repo: **adding a new outbound MCP tool call to one of the
upstream contexts**, plus the read-only/zero-write guardrail that is this
repo's distinctive addition to the fleet's integration story.

Use when asked to consume a NEW published tool from wes-work-planning,
fulfillment-execution, inventory-storage, workforce-management,
facility-layout, labor-performance, order-management,
process-path-management, or warehouse-planning (read tools only — its server
also has write tools; the zero-write scan rejects any write verb, ADR 0013). This repo never publishes an integration event and never
consumes Kafka at all — if a task genuinely needs that, it belongs in one
of the upstream repos, not here.

This walks the exact addition `FacilityLayoutClient.EstimateTravelDistance`
took (ADR 0009, `internal/adapters/outbound/mcpclient/facility_layout.go`)
as the concrete worked example.

## 1. Never import the upstream's Go packages

This repo knows an upstream tool's name and payload shape only — never
its Go types. `internal/architecture/architecture_test.go`'s
`TestNoDirectDependencyOnBoundedContexts` fails the build the moment
`go.mod`/`go.sum` references `github.com/claudioed/{fulfillment-execution,
wes-work-planning, workforce-management, inventory-storage,
facility-layout}`. Hand-mirror the tool's response shape as a new
`ports.<Noun>` struct field-for-field (see `ports.TravelDistance`
mirroring facility-layout's own `travelDistanceDTO`) — this is this
repo's version of inventory-storage's "hand-mirror the payload struct
locally" rule, applied to MCP tool results instead of Kafka event
payloads.

## 2. Add the method to the outbound port

Every upstream context has exactly one `ports.<Context>Client` interface
in `internal/ports/clients.go` (or `clients_phase2.go` for the
second-wave clients — order-management, labor-performance,
process-path-management, per ADR 0007; `clients_planning.go` for
warehouse-planning, per ADR 0013). Add the new method there first:

```go
// FacilityLayoutClient is the outbound port for facility-layout's published
// read tools (list_sites, get_site_layout, get_zone_grid,
// estimate_travel_distance).
type FacilityLayoutClient interface {
    ListSites(ctx context.Context) (SitesResult, error)
    // ... existing methods ...
    EstimateTravelDistance(ctx context.Context, from, to string) (TravelDistance, error)
}
```

Ports are interfaces only in this repo too, same convention as the
bounded-context repos.

## 3. Implement it in the mcpclient adapter

In `internal/adapters/outbound/mcpclient/<context>.go`, add the method
calling the upstream's tool through the existing `Session`:

```go
func (c *FacilityLayout) EstimateTravelDistance(ctx context.Context, from, to string) (ports.TravelDistance, error) {
    var out ports.TravelDistance
    err := c.session.callTool(ctx, "estimate_travel_distance", map[string]any{"from": from, "to": to}, &out)
    return out, err
}
```

The tool name string (`"estimate_travel_distance"`) is the ONLY coupling
to the upstream — confirm it against that context's own published tool
list (its `internal/adapters/inbound/mcp/tools.go` or its own
`docs/docs/api-surface.md`-equivalent) before writing it; a typo here
fails silently as a normal tool-not-found MCP error at call time, not a
compile error.

## 4. The additive/read-only guardrail — this is the part that matters here

**Every outbound call this repo makes to any upstream context must
stay `GET`/read-only.** There is no Kafka-style "which consumer-group
pattern" decision to make here (this repo has no Kafka at all) — the
decision that matters is narrower and stricter: this repo has **zero
write capability by design (v1)**, enforced by
`internal/architecture/zerowrite/zerowrite_test.go`'s
`TestNoMutatingHTTPMethodInOutboundClients`, which statically scans every
non-test `.go` file in `internal/adapters/outbound/{mcpclient,restclient}`
for a mutating HTTP method literal (`http.MethodPost`, `"PUT"`, etc.) via
an AST walk — not a text grep, so it isn't fooled by the string appearing
in a comment, but it also can't be fooled by burying it in a
`map[string]any` argument either. Since MCP tool calls carry no HTTP verb
of their own (they're all POST-over-Streamable-HTTP at the transport
level, per the MCP spec), the discipline that actually matters is: **only
call tools the upstream itself has published as read-only.** Check the
upstream context's own tool registration
(`internal/adapters/inbound/mcp/tools.go` in that repo) for
`ReadOnlyHint: true` before wiring a new call here — if the tool you want
to call is a write tool (e.g. a future `assign_labor`), it does not
belong behind this repo's outbound port at all under the current
v1 scope; see `docs/docs/mcp/governance-note.md` for the two guardrails
(an authorization gate + explicit human confirmation) any future
write-capable slice must satisfy first.

## 5. If this tool call feeds a new use case's `Brief` for the LLM reasoner (ADR 0004)

If the new call is meant to be one of the tools the ADR-0004 reasoner can
invoke (rather than only the deterministic policy path), it is exposed to
the model the same way every other tool already is — generated from the
`mcpclient` sessions this repo holds, validated against its JSON schema
before the call and its enum fields re-validated after. Adding a NEW tool
call here does not by itself grant the model access to it; that is a
separate, explicit step in the reasoner's tool catalogue wiring — read
ADR 0004 in full before assuming a new outbound method is automatically
model-reachable.

## 6. Test

Unit test the new port method's call shape against a fake `Session`/tool
transport (see `mcpclient/*_test.go` in this package — e.g.
`facility_layout_test.go`), never a real MCP server in a unit test.
Unit test the use case that calls it against a fake implementation of the
port interface (see `usecases/fakes_test.go`'s `fakeFacility`), covering
the upstream-unreachable degradation path (ADR-0004's fallback
discipline: a missing/unreachable signal degrades to a partial result,
never a panic).

This repo has no Kafka, so there is no testcontainers-based integration
test category to add here the way inventory-storage's guide describes —
skip that whole section; it does not apply.

## Verify before opening the PR

```bash
make check-all    # includes arch-test — catches both a sibling-package
                   # import AND a mutating-method literal in the outbound
                   # adapter packages
```

`make arch-test` runs `internal/architecture/...` in full, which is both
the cross-context-import guard (`architecture_test.go`) and the
zero-write guardrail (`zerowrite/zerowrite_test.go`) in the same command.
