---
id: 0025-network-fleet-mcp-clients
title: "0025 — Twelve more outbound MCP clients: the retail-network fleet and two WMS contexts, wired and golden-pinned"
sidebar_label: "0025 · Network-fleet MCP clients"
description: "Twelve read-only upstream MCP clients (customer-ordering, delivery-promise, last-mile-delivery, linehaul-transportation, node-performance, node-topology, offer-availability, order-sourcing, shipping-and-tracking, vendor-connectivity, task-interleaving, pick-face-replenishment), each built only when its endpoint is set and pinned to its upstream's published registry golden."
---

# ADR 0025: Twelve more outbound MCP clients, wired and golden-pinned

## Status

Accepted (2026-10-24). Extends [ADR 0007](./0007-second-wave-outbound-mcp-clients.md)
and follows [ADR 0023](./0023-skip-mcp-clients-without-an-endpoint.md)
(optional wiring) and the golden-pinned client pattern of
[ADR 0020](./0020-product-master-mcp-client-and-master-data-gaps.md),
[0021](./0021-inbound-receiving-mcp-client-and-inbound-outlook.md) and
[0024](./0024-slot-plan-watch-promise-health-and-ppm-client-removal.md).

## Context

The retail-network side of the fleet (customer-ordering, delivery-promise,
last-mile-delivery, linehaul-transportation, node-performance,
node-topology, offer-availability, order-sourcing, shipping-and-tracking,
vendor-connectivity) plus two more WMS contexts (task-interleaving,
pick-face-replenishment) now publish read-only MCP servers on the kind
cluster — the same Streamable-HTTP Open Host Service the original
upstreams expose. warehouse-ops-agent had a client for none of them, so
the endpoints the infrastructure already deploys were dead configuration:
no port, no adapter, nothing a future decision-support use case could
read. This is the same "wired, not yet consumed" gap ADR 0007 closed for
order-management and labor-performance, twelve times at once.

Every one of these upstreams publishes its tool registry golden at
`internal/adapters/inbound/mcp/testdata/tool_registry.golden.json` in its
own repo — the authoritative tool names, argument keys and result shapes.

## Decision

1. Add twelve outbound MCP clients, one per upstream
   (`internal/adapters/outbound/mcpclient/<context>.go`), each calling
   that upstream's WHOLE published read surface (46 tools in total:
   customer-ordering 3, delivery-promise 5, last-mile-delivery 3,
   linehaul-transportation 2, node-performance 2, node-topology 6,
   offer-availability 4, order-sourcing 2, shipping-and-tracking 1,
   vendor-connectivity 9, task-interleaving 4, pick-face-replenishment 5).
   Every tool in every one of these registries is annotated
   `readOnlyHint`; no write tool exists to skip.
2. Mirror the port interfaces and DTOs by hand in `internal/ports`
   (never importing the upstream's Go packages —
   `TestNoDirectDependencyOnBoundedContexts` keeps enforcing that). The
   retail-network upstreams publish camelCase JSON; pick-face-replenishment
   publishes snake_case; the mirrors keep each upstream's own casing
   verbatim.
3. Pin each client to its upstream's published registry golden: the
   golden is copied VERBATIM into
   `mcpclient/testdata/<context>_tools.golden.json`, and the client's
   tests fail when the upstream amends its surface (tool set, argument
   keys, required output properties) until this repo catches up. The
   twelve share one golden-driven stand-in double
   (`golden_upstream_test.go`), which serves each tool with its real
   published input/output schema so argument-key and DTO assertions are
   validated against the true contract.
4. Wire each client per ADR 0023: `newMCPClient` builds it only when its
   `<CTX>_MCP_ENDPOINT` is set; an empty endpoint is a genuinely nil
   interface and boot never depends on any of the twelve. The startup log
   reports `<ctx>_endpoint_configured` for each. No use case consumes any
   of them yet — they exist so each read surface is reachable the moment
   one does; the clients are NOT added to the reasoner's tool catalogue
   (that remains a separate, explicit step per ADR 0004).
5. Register every client in the zero-write scan
   (`internal/architecture/zerowrite`): each file's allow-list pins its
   exact tool set, and the package-wide prefix scan keeps rejecting any
   write verb.
6. Chart: `upstreams.<camel>Endpoint` values for the twelve, rendered to
   the same env names in both the ConfigMap and the Deployment
   (`CUSTOMER_ORDERING_MCP_ENDPOINT` … `PICK_FACE_REPLENISHMENT_MCP_ENDPOINT`),
   with render assertions covering every name. **Infra follow-up (out of
   scope here):** warehouse-infra currently sets these exact env names via
   `extraEnv` on the Deployment. Now that the chart renders them from
   values, infra must drop its `extraEnv` duplicates in a follow-up change
   so the Deployment does not carry the same key twice (an explicit `env:`
   entry also silently wins the Kubernetes merge over `envFrom`, which
   would mask an infra-side value change).

## Consequences

- The twelve upstreams' read surfaces are wired and contract-pinned; a
  future use case consumes a port without touching the adapter.
- Ten of the twelve upstreams speak camelCase JSON and one
  (pick-face-replenishment) snake_case; the golden-pinned DTO tests make
  a casing drift on either side a build failure here, not a silent
  misdecode.
- The kind cluster's deployed endpoints become live configuration the
  moment `upstreams.*` values (or the infra env vars) carry them — and
  ONLY then; every default stays `""` (fail-open), so local dev and CI
  boot without any of the twelve.
- Tests: per-client golden pin, port-DTO-vs-schema walk, wire-behaviour
  over a schema-faithful stand-in (decode, exact argument keys, only the
  pinned tools called, unreachable upstream, cancelled context,
  slug-classification), `TestNetworkFleetClientsCallOnlyPinnedTools`
  (zerowrite), the extended composition-root maps in
  `cmd/agent/main_test.go`, and the extended chart render assertions.
