---
id: 0023-skip-mcp-clients-without-an-endpoint
title: "0023 — Build an upstream MCP client only when its endpoint is set"
sidebar_label: "0023 · Skip MCP clients without an endpoint"
description: "An empty *_MCP_ENDPOINT builds no client; every use case treats the missing client as an unavailable upstream, and explain-travel-factor answers 503 without facility-layout."
---

# ADR 0023: Build an upstream MCP client only when its endpoint is set

## Status

Accepted (2026-10-13). Amends [ADR 0007](./0007-second-wave-outbound-mcp-clients.md),
whose step 4 constructed the order-management, labor-performance and
process-path-management clients unconditionally. The optional-client rule
of ADRs 0013, 0019, 0020 and 0021 is unchanged; this record extends it to
every upstream.

## Context

The composition root (`cmd/agent/main.go`) treated its twelve upstream MCP
clients two ways. Four of them (warehouse-planning, product-master,
inbound-receiving, network-inventory-planning) were built only when their
endpoint was set, so a nil port meant "not configured". The other eight
(wes-work-planning, fulfillment-execution, workforce-management,
facility-layout, inventory-storage, labor-performance, order-management,
process-path-management) were built even when the endpoint was empty.
Every call then dialled `""` and failed with
`unsupported protocol scheme ""` at request time.

The fleet's ecosystem-gaps review recorded this as a trap: an unset
endpoint looked like an upstream outage in logs and traces, it cost an MCP
session attempt on every request, and `detect_stranded_reservation` had no
nil guard, so the only thing protecting it was that the client was never
nil.

## Decision

1. `newMCPClient` in `cmd/agent/main.go` builds an upstream's MCP client
   only when its endpoint is non-empty. Otherwise it returns the zero value
   of the port interface: a genuinely nil interface, never a non-nil
   interface around a nil pointer. All twelve clients go through it.
2. Every use case treats a nil port as an unavailable upstream, exactly
   like a failed call:
   - the daily brief marks the signal `unavailable` (already nil-safe);
   - the flow balance returns a partial decision (already nil-safe);
   - `DetectStrandedReservation` skips the missing reads and degrades to an
     undetected `hold` (new nil guards);
   - explain-travel-factor is not wired without facility-layout, so
     `GET /explain-travel-factor` answers `503` ("explain travel factor not
     configured") and the `explain_travel_factor` tool is not registered.
     This is the same answer every other upstream-specific feature already
     gives when its upstream is not configured. Before this record it was a
     `502` from a failed dial.
3. The startup log line reports `*_endpoint_configured` for all twelve
   upstreams, so an operator sees which ones are off.

## Consequences

- No request ever dials an empty URL. An unset endpoint is visible at
  startup instead of as a per-request transport error.
- `GET /explain-travel-factor` without facility-layout changes from `502`
  to `503`. A `502` now always means facility-layout was configured and
  failed.
- Tests: `TestNewOutboundClients_EmptyEndpointBuildsNoMCPClient`,
  `TestNewOutboundClients_SetEndpointBuildsEveryMCPClient`,
  `TestNewDecisionSupport_NoUpstreamsConfigured_DegradesWithoutPanicking`,
  `TestNewDecisionSupport_ExplainTravelFactorOnlyWiredWhenFacilityConfigured`
  (`cmd/agent/main_test.go`) and
  `TestDetectStrandedReservation_UnconfiguredUpstreams_DegradeToHold`
  (`internal/application/usecases`).
