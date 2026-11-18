---
id: 0032-yard-mcp-clients-dock-scheduling-and-yard-management
title: "0032 — dock-scheduling and yard-management MCP clients, off by default"
sidebar_label: "0032 · yard MCP clients"
description: "Two read-only outbound MCP clients for the yard services (17 read tools of 35 published), pinned to their verbatim registry goldens, off until an endpoint is set, with the reasoner allow-list held to the pinned read set. Wired, not yet consumed; every yard write stays human-approved (D-Y4)."
---

# ADR 0032: dock-scheduling and yard-management MCP clients, off by default

## Status

Accepted (2026-11-17). Extends [ADR 0025](./0025-network-fleet-mcp-clients.md)
and [ADR 0027](./0027-seller-inbound-mcp-client.md) (the golden-pinned,
wired-not-consumed client shape), follows
[ADR 0023](./0023-skip-mcp-clients-without-an-endpoint.md) (optional wiring)
and [ADR 0004](./0004-llm-reasoner-behind-the-policy-layer.md) (the reasoner's
only actuators are allow-listed MCP read tools).

## Context

Step Y0.4 of the network/yard evolution plan: the ops agent had no MCP client
for either yard service, so neither could be listed, read or reasoned over.
Both services publish an MCP server (Streamable HTTP, `/mcp`, port 8090,
unauthenticated like the rest of the fleet), but **neither is read-only**:

| Service | Read tools (L1) | Write tools (L3) |
|---|---|---|
| dock-scheduling | 7: `list_appointments`, `get_appointment`, `get_door_availability`, `get_dock_calendar`, `list_carriers`, `get_carrier`, `list_at_risk_appointments` | 7: `book_appointment`, `reschedule_appointment`, `cancel_appointment`, `register_carrier`, `suspend_carrier`, `reinstate_carrier`, `report_eta` |
| yard-management | 10: `get_yard_board`, `list_trailers`, `get_trailer`, `list_moves`, `list_visits`, `get_visit`, `get_yard_policy`, `get_yard_dwell_report`, `get_inspection`, `list_trailer_inspections` | 11: `request_move`, `cancel_move`, `place_hold`, `release_hold`, `record_seal`, `secure_trailer`, `release_securing`, `open_inspection`, `record_inspection_item`, `complete_inspection`, `open_yard_check` |

Every tool declares its level in `_meta` `com.warehouse/authority` (L1 reads,
L3 writes). Decision D-Y4 keeps approval on every agent write, and the
zero-write guarantee (ADR 0027, ADR 0029) allows exactly two act slices, so
nothing here may add a write capability.

## Decision

Add the fifteenth and sixteenth outbound MCP clients in the ADR-0025/0027
shape, **read tools only**:

- `ports.DockSchedulingClient` (7 methods) and `ports.YardManagementClient`
  (10 methods), one per read tool, with result DTOs hand-mirrored
  field-for-field from the published output schemas (never an upstream
  import). Optional filters are omitted when zero so the upstream default
  applies. There is deliberately no method for any write tool.
- `mcpclient.DockScheduling` and `mcpclient.YardManagement`, with the exported
  pinned sets `DockSchedulingReadTools` and `YardManagementReadTools`.
- Each client is pinned to a VERBATIM copy of its upstream's published registry
  golden (`testdata/dock_scheduling_tools.golden.json`, dock-scheduling
  origin/develop `eecbee17`; `testdata/yard_management_tools.golden.json`,
  yard-management origin/develop `418feea9`). Because these registries MIX reads
  and writes, the pin is split: the golden must be exactly the read set (every
  tool `readOnlyHint` and `L1`) plus the write set (every tool a non-read-only
  `L3`), so a tool added or reclassified upstream fails until a person decides.
  Every read DTO is walked against its published output schema.
- The zero-write fitness test pins each client file to its exact read list and
  gains the yard write verbs (`request_`, `place_`, `secure_`, `complete_`,
  `reschedule_`, `suspend_`, `reinstate_`, `report_`) to its write-prefix
  scan; `Session.callTool` already stamps authority `L1` on every upstream
  call, which is now true by construction for these two upstreams.
- The reasoner gets a session per yard upstream, and `LLM_TOOL_ALLOWLIST` is
  held to the pinned read set for them (`mcpclient.CheckReasonerAllowList`):
  an entry naming a yard write tool, or any tool outside the pinned reads, is
  a startup error, never silently dropped. Legacy upstreams are untouched.
- **Off by default.** `DOCK_SCHEDULING_MCP_ENDPOINT` and
  `YARD_MANAGEMENT_MCP_ENDPOINT` default to empty; an empty endpoint builds no
  client and drops the reasoner session (ADR 0023). The default
  `LLM_TOOL_ALLOWLIST` is unchanged and names no yard tool. The chart renders
  both into the Deployment env and the ConfigMap from
  `upstreams.dockScheduling.endpoint` / `upstreams.yardManagement.endpoint`
  (default empty), with render assertions for the empty default and for a set
  value reaching the Deployment verbatim.

No use case consumes either client yet.

## Consequences

- The yard read surface is reachable, and with the reads allow-listed the
  agent's boot-time tool discovery lists exactly the 17 yard read tools and
  none of the 18 write tools (tested through the real `wireReasoner` and with a
  fake upstream publishing the whole verbatim registry).
- Switching it on is a values change in warehouse-infra
  (`upstreams.dockScheduling.endpoint:
  http://dock-scheduling-mcp.warehouse-systems.svc.cluster.local:8090/mcp`,
  `upstreams.yardManagement.endpoint:
  http://yard-management-mcp.warehouse-systems.svc.cluster.local:8090/mcp`),
  and requires each yard chart's `mcp.enabled=true` (cluster capacity, plan step
  Y0.2). Until then nothing changes.
- `get_yard_dwell_report` answers empty rows until yard-management wires its
  analytics reader in `cmd/mcp`; the client decodes that honestly.
- warehouse-infra's yard values comments call the yard MCP servers
  "read-only"; they publish L3 writes. Only this agent's clients are
  read-only; the writes stay behind human approval (D-Y4).
- Refreshing a golden is the one-command copy named in each test file followed
  by fixing whatever the DTO walk or the read/write split reports. A future
  consuming use case follows `.claude/skills/how-to-add-an-mcp-tool-call` and
  its ADR supersedes the "not yet consumed" wording here.
- Giving the model any yard tool is an operator choice (list the pinned read
  tools in `LLM_TOOL_ALLOWLIST`); there is still no path for it to reach a yard
  write, and the agent's own write slices stay the two act slices.
