---
id: 0027-seller-inbound-mcp-client
title: "0027 — seller-inbound MCP client, wired and golden-pinned"
sidebar_label: "0027 · seller-inbound client"
description: "A fourteenth read-only upstream MCP client for seller-inbound (get_seller_scorecard, get_inbound_plan, get_shipment_labels, get_inbound_shipment), built only when SELLER_INBOUND_MCP_ENDPOINT is set and pinned to seller-inbound's published registry golden. Wired, not yet consumed."
---

# ADR 0027: seller-inbound MCP client, wired and golden-pinned

## Status

Accepted (2026-10-25). Extends [ADR 0025](./0025-network-fleet-mcp-clients.md)
and follows [ADR 0023](./0023-skip-mcp-clients-without-an-endpoint.md)
(optional wiring) and the golden-pinned client pattern of
[ADR 0020](./0020-product-master-mcp-client-and-master-data-gaps.md).

## Context

seller-inbound (retail network, partner tier) published its read-only MCP
server in its ADR 0006 (IQVO/seller-inbound#5): four tools over the same
read use cases as its REST GETs — `get_seller_scorecard` (an optional
rolling window), `get_inbound_plan`, `get_shipment_labels` and
`get_inbound_shipment`. warehouse-infra renders
`SELLER_INBOUND_MCP_ENDPOINT` from `upstreams.sellerInbound.endpoint`
(held values PR, merged only after the seller-inbound app is rebuilt), so
without a client here that endpoint is dead configuration — the same
"wired, not yet consumed" gap ADR 0025 closed for the twelve and ADR 0020
for product-master. Nothing on the decision-support side reads the
partner-inbound surface yet; the natural first consumers (a seller
compliance watch, an inbound-shipment tracker for the floor) have not been
built.

## Decision

Add the fourteenth outbound MCP client, exactly in the ADR-0025 shape:

- `ports.SellerInboundClient` with four methods, one per published tool;
  the result DTOs (`SellerScorecard`, `InboundPlan`, `ShipmentLabels`,
  `InboundShipment` and their nested types) are hand-mirrored
  field-for-field from seller-inbound's published output shapes — this
  repo never imports an upstream's Go packages (the architecture fitness
  test forbids it).
- `mcpclient.SellerInbound` calls the four tools through the shared
  `Session`; an empty scorecard window is omitted from the arguments so
  seller-inbound applies its own default (30d), exactly like every
  optional filter on the sibling clients.
- The client is pinned to a VERBATIM copy of seller-inbound's published
  tool registry golden
  (`testdata/seller_inbound_tools.golden.json`, pinned at
  seller-inbound origin/develop 60eb7262): the tests fail the moment the
  upstream amends its surface, and walk every port DTO against the
  published output schemas.
- Wired per ADR 0023: no endpoint, no client (fail-open); no use case
  consumes it yet, so the agent's behaviour is unchanged until one does.
- The chart renders `SELLER_INBOUND_MCP_ENDPOINT` into the Deployment env
  and the ConfigMap from `upstreams.sellerInbound.endpoint` (default
  empty), with render-test assertions for both paths, verbatim.

## Consequences

- The partner-inbound read surface (seller compliance, inbound plans,
  cartons with GS1/SSCC labels, shipment states) is reachable the moment a
  use case needs it, with its contract locked by the golden.
- Refreshing the golden when seller-inbound amends its tools is a
  one-command copy (named in the test file) followed by fixing whatever
  the DTO-walk test reports.
- When the first consuming use case arrives, wiring it follows
  `.claude/skills/how-to-add-an-mcp-tool-call` and its ADR supersedes the
  "not yet consumed" wording here, exactly as ADR 0026 did for ten of the
  ADR-0025 clients.
