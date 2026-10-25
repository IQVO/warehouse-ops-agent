---
id: 0026-network-outlook-and-process-path-client
title: "0026 — Network order/node outlooks over the ADR-0025 clients; process-path-management client re-added"
sidebar_label: "0026 · network outlooks + PPM client"
description: "Two composed read-only agent tools — get_network_order_outlook and get_network_node_outlook — assemble one order's / one node's retail-network picture across the wired ADR-0025 clients with per-section fail-open degradation, and the thirteenth client (process-path-management, three tools, golden-pinned) returns so the endpoint the cluster already deploys renders again."
---

# ADR 0026: Network outlooks over the ADR-0025 clients; process-path-management re-added

## Status

Accepted (2026-10-24). Consumes the clients wired by
[ADR 0025](./0025-network-fleet-mcp-clients.md) ("wired, not yet
consumed"), applies [ADR 0023](./0023-skip-mcp-clients-without-an-endpoint.md)
(fail-open wiring) and the per-section honesty envelope of
[ADR 0021](./0021-inbound-receiving-mcp-client-and-inbound-outlook.md)
unchanged. Partially reverses [ADR 0024](./0024-slot-plan-watch-promise-health-and-ppm-client-removal.md)'s
gap-A9 deletion of the process-path-management client.

## Context

ADR 0025 wired twelve read-only MCP clients and deliberately consumed
none: they existed "so each read surface is reachable the moment a use
case does". Two retail-network questions now need several of those
surfaces at once, and no single upstream can answer either:

- **"Where does this order stand across the network?"** — the placed
  order and its shipment groups (customer-ordering), how each group is
  travelling (customer-ordering again), the promises each group must keep
  (delivery-promise), the assignments sourcing them (order-sourcing) and
  the customer-visible journeys behind the tracking numbers
  (shipping-and-tracking). Six facts, five upstreams, one order id.
- **"Where does this node stand across the network?"** — the node and its
  operational profile plus the lanes on either side of it (node-topology),
  its rolling scorecards (node-performance), what it sells and the
  inventory feed behind that (offer-availability), its vendor registration
  and vendor-relayed scorecards (vendor-connectivity), the linehaul
  departures still ahead of it (linehaul-transportation) and — for a
  delivery station — today's last-mile waves (last-mile-delivery). Ten
  facts, six upstreams, one node id.

Separately, `PROCESS_PATH_MANAGEMENT_MCP_ENDPOINT` is still configured in
warehouse-infra's terraform, but ADR 0024 (gap A9) deleted this repo's
client, so the variable is dead configuration the binary ignores and the
chart refuses to render. Since that deletion, process-path-management's
registry has grown `get_cpt_schedule` — a site's Critical Pull Time
schedule, which IS an ops signal — and ADR 0025 established
"wired, not yet consumed" as an accepted posture rather than a bug.

## Decision

1. **Order outlook.** `usecases.NetworkOrderOutlook`
   (`internal/application/usecases/network_order_outlook.go`) takes one
   order id and reports six sections, each an
   `OutlookSection` (the ADR-0021 envelope: `omitted` reason, `complete`
   flag, items):
   - **order** — customer-ordering `get_order` (lines and groups);
   - **tracking** — customer-ordering `get_order_tracking` (per-group
     promise verdict and packages);
   - **shipTo** — customer-ordering `get_ship_to` (the synthetic PII the
     outlook is the drill-down for);
   - **promises** — delivery-promise `list_promises_by_order`, with
     `get_promise` covering the two genuine gaps: a group whose
     `promiseId` the order list did not return, and — when the whole list
     call failed — every group's promise, one by one. A promise list that
     could not cover every group says `complete: false`;
   - **sourcing** — order-sourcing `list_assignments` filtered to the
     order (the list already returns the full assignment view;
     `get_assignment` stays the per-record drill-down, unconsumed here);
   - **journeys** — shipping-and-tracking `get_journey`, once per
     tracking number unioned from the tracking view's packages and the
     sourcing assignments (at most **20 reads** per call; more numbers say
     `complete: false`). A failed journey keeps its slot with an `error`
     field — unknown, never "delivered". When neither tracking source
     could be read the section is omitted with why; a read source with no
     numbers is the honest empty list.
2. **Node outlook.** `usecases.NetworkNodeOutlook`
   (`internal/application/usecases/network_node_outlook.go`) takes one
   node id and reports ten sections over the same envelope:
   - **node** — node-topology `get_node` (profile summarized: handling
     time, cutoffs, throughput cap; the full profile stays upstream);
   - **inboundLanes** / **outboundLanes** — node-topology `list_lanes` by
     destination / origin;
   - **scorecards** — node-performance `get_scorecard`;
   - **listings** — offer-availability `list_listings` filtered by node
     (500 per keyset page, at most 10 pages, `complete: false` at the
     bound);
   - **inventoryFeed** — offer-availability `get_inventory_feed` on the
     freshest feed id (`asOf`) the node's listings reference; no
     feed-bearing listing is the explicit "no listing carries an
     inventory feed" omission, an unreadable listings section is the
     different "listings unavailable" omission;
   - **vendor** / **vendorScores** — vendor-connectivity
     `get_vendor_node` + `get_node_scorecard`, keyed by the vendorRef
     node-topology publishes: a node without one is the explicit
     "node is not registered under a vendor" omission (both sections),
     never an error;
   - **departures** — linehaul-transportation `list_departures` with
     `origin=node` and `from=now` (UTC RFC 3339);
   - **waves** — last-mile-delivery `list_waves` for the node's local
     service date, **only** when the node is a `DELIVERY_STATION`
     (anything else is the explicit "not a delivery station" omission; an
     unknown timezone falls back to UTC rather than guessing a calendar).
3. **Ports.** Both use cases depend on NARROW port interfaces
   (`internal/ports/clients_network_outlook.go`: `PlacedOrderReader`,
   `OrderPromiseReader`, `OrderAssignmentReader`, `PackageJourneyReader`,
   `NetworkNodeReader`, `NodeScorecardReader`, `NodeListingReader`,
   `VendorNodeReader`, `NodeDepartureReader`, `StationWaveReader`) —
   method subsets of the existing ADR-0025 client interfaces the
   adapters already satisfy. No new outbound method was needed; the
   subsets state, per upstream, exactly which slice of the wired surface
   an outlook consumes.
4. **Degradation semantics** (ADR 0023 + the ADR-0021 envelope): a
   section whose client is unconfigured is omitted with
   `not-configured: <upstream> endpoint is unset`, never an error; a
   failing call omits its section with the error text; sections that
   genuinely do not apply (non-station node, unregistered vendor) are
   omitted with that reason. Only when **every attempted section failed**
   does Execute return an error, so an unreachable anchor is never
   rendered as an all-clear. The outlooks are wired only when their
   anchor client is configured — customer-ordering owns the order id,
   node-topology owns the node id — and the tools are then simply not
   registered (the `explain_travel_factor` precedent).
5. **Surface.** Two read-only MCP tools, `get_network_order_outlook`
   (`orderId`) and `get_network_node_outlook` (`nodeId`), in
   `internal/adapters/inbound/mcp/network_outlook.go`, reusing the
   inbound outlook's wire section (`omitted` / `complete` / `count` /
   `items`). No REST route: these are agent-facing picture tools; a REST
   surface would be a separate decision. No BDD feature file either — the
   godog suite (ADR 0022) drives REST routes, and these tools' behaviour
   is pinned by the use case table tests and the over-the-wire MCP
   server tests instead.
6. **Boot log.** Every client the outlooks consume was already visible at
   boot (`customer_ordering_endpoint_configured` …
   `last_mile_delivery_endpoint_configured`, ADR 0025); the new
   `process_path_management_endpoint_configured` field covers the
   thirteenth client.
7. **The thirteenth client: process-path-management, re-added.**
   `internal/adapters/outbound/mcpclient/process_path_management.go`
   implements `ports.ProcessPathManagementClient`
   (`internal/ports/clients_process_path_management.go`): the whole
   current default surface — `get_process_path`, `list_process_paths`,
   `get_cpt_schedule`. That upstream still pins its registry in the older
   TSV convention (name + readOnly + destructive), so this repo's golden
   (`mcpclient/testdata/process_path_management_tools.golden.json`) was
   converted into the JSON registry convention every other client pins
   (names, descriptions, annotations, input and output schemas, rendered
   with the same go-sdk version the upstream serves with); the wire tests
   run against the same schema-faithful stand-in as the ADR-0025 clients,
   and `zerowrite.TestNetworkFleetClientsCallOnlyPinnedTools` pins the
   client to exactly the three names. **Why the reversal of ADR 0024 is
   honest:** ADR 0024 deleted a two-tool client whose content was
   catalogue configuration ("a planning input, not an ops signal") *and*
   unconsumed. The registry has since grown `get_cpt_schedule` — an ops
   signal — and ADR 0025 made wired-unconsumed an accepted posture. The
   client stays wired-unconsumed for now; a CPT-schedule consumer (e.g. a
   cutoff-countdown section in some future outlook) is the natural
   follow-up, and the endpoint the cluster already deploys is honest
   configuration again. Chart: `upstreams.processPathManagement.endpoint`
   renders `PROCESS_PATH_MANAGEMENT_MCP_ENDPOINT` in both the ConfigMap
   and the Deployment, with render assertions; ADR 0024's
   "must never come back" assertions are replaced accordingly.
8. **LLM tool allow-list: no new entries.** The
   `LLM_TOOL_ALLOWLIST`/`llm.toolAllowList` bounds the MODEL's actuators
   (upstream `<upstream>/<tool>` entries, ADR 0004). The new tools are
   this agent's OWN inbound surface, not model actuators, so no entry is
   added — the same reasoning ADR 0021–0025 applied to their tools. The
   ADR-0025 upstreams also stay off the list (per ADR 0025).
9. **Still wired-unconsumed, deliberately:** task-interleaving and
   pick-face-replenishment (ADR 0025) and the re-added
   process-path-management client. Task-interleaving's surface is keyed
   by WMS site/station codes, not network node ids — folding it into the
   node outlook would need a site↔node mapping nobody publishes;
   pick-face-replenishment is warehouse-internal replenishment state with
   no order- or node-picture role that would not be a fake section. No
   use case pretends otherwise.

## Consequences

- The agent answers "where does this order/node stand across the
  network?" with per-section evidence, read-only, degrading per upstream.
- A section is never silently absent: unconfigured, failed, inapplicable
  and capped each carry their own reason, and `complete: false` marks
  lower bounds (journey cap, listings page bound, partially covered
  promises).
- The journeys section makes up to 1 + 3 + 1 + 1 + N(≤20) upstream calls
  per order outlook — bounded, sequential, per-call timeouts, no retry
  (every client's standing posture).
- ADR-0025 surface drift still fails this repo's build the moment a
  golden is refreshed; the outlooks additionally pin, per upstream, which
  tools they consume via the narrow ports.
- `get_assignment`, `get_departure`, `get_wave_stops`, `get_route` and
  seven of vendor-connectivity's nine tools stay wired but unconsumed by
  the outlooks (drill-downs and vendor-facing views); they remain
  reachable for the next use case.
- warehouse-infra: `PROCESS_PATH_MANAGEMENT_MCP_ENDPOINT` (still in
  terraform) becomes live the moment the chart value carries it; infra's
  own follow-ups (dropping stale `extraEnv` duplicates per ADR 0025,
  and deciding whether to keep the terraform variable at all) are out of
  scope here.
