---
id: 0024-slot-plan-watch-promise-health-and-ppm-client-removal
title: "0024 — Slot-plan watch and promise health in the daily brief; process-path-management client removed"
sidebar_label: "0024 · slot-plan watch + promise health; PPM client removed"
description: "The agent consumes slotting-optimization (list_slot_plans, get_slot_plan) for a daily-brief slot-plan watch and order-management's get_promise_health for a promise-health section, both fail-open; the wired-but-unused process-path-management client is deleted."
---

# ADR 0024: Slot-plan watch and promise health in the daily brief; process-path-management client removed

## Status

Accepted (2026-10-17). Closes gaps A14, A8 and A9 of the fleet's
connect-all audit. Follows the client shape of
[ADR 0021](./0021-inbound-receiving-mcp-client-and-inbound-outlook.md)
(the previous upstream consumed here) and applies
[ADR 0023](./0023-skip-mcp-clients-without-an-endpoint.md) (a client is
built only when its endpoint is set) unchanged. Partially supersedes
[ADR 0007](./0007-second-wave-outbound-mcp-clients.md): its
"wired but unconsumed" posture for order-management ends here, and its
process-path-management client is deleted.

## Context

The fleet's connect-all audit found three wiring gaps in this agent:

- **A14** — slotting-optimization's MCP server is deployed and publishes
  four read tools (`get_slot_plan` with an execution view,
  `list_slot_plans`, `get_forward_slots`, `get_sku_velocity`), and nothing
  calls it. The operational question nobody answers in one place: an
  Approved forward-pick re-slot whose physical moves are still not fully
  executed a day after approval — half-migrated slotting that quietly
  erodes the travel-time win the plan promised.
- **A8** — order-management's `get_promise_health` tool (promise basis
  distribution, re-promise rate, split-shipment rate, promise-to-cutoff
  gap) has no client at all, while the client's one wired method,
  `get_order`, is called by no use case: the exact wired-but-unused shape
  the audit exists to close.
- **A9** — the process-path-management client (`get_process_path`,
  `list_process_paths`) is likewise wired but unconsumed. Its catalogue
  configuration (match prefixes, capabilities) is a planning input, not an
  ops signal; decision was taken to delete it rather than invent a
  consumer.

## Decision

1. **A14 — client.** `internal/adapters/outbound/mcpclient/slotting_optimization.go`
   implements `ports.SlottingOptimizationClient`
   (`internal/ports/clients_slotting.go`) with exactly **two** of the four
   published tools: `list_slot_plans` and `get_slot_plan` — the two the
   use case needs. `get_forward_slots` and `get_sku_velocity` are
   deliberately NOT wired: a wired-but-unused method is the bug class this
   change closes. Contract pinning mirrors ADR 0021:
   `mcpclient/testdata/slotting_optimization_tools.golden.json` is a
   byte-identical copy of slotting-optimization's published registry
   golden, DTO json tags are walked against its output schemas, the wire
   tests run against a schema-faithful stand-in server, and
   `zerowrite.TestSlottingOptimizationClientCallsOnlyPinnedTools` pins the
   client to exactly the two tool names.
2. **A14 — use case.** `usecases.SlotPlanWatch` scans Approved plans
   (`list_slot_plans`, `state=Approved`, 500 per page, at most 10 pages =
   5,000 plans), and for each plan older than the threshold with moves to
   execute, reads `get_slot_plan`'s execution view. A plan approved
   exactly `SLOT_PLAN_STALE_AGE` ago counts as "after the threshold" (the
   boundary is inclusive); a plan with no execution recorded reads as
   0/N executed; a plan with all moves executed, no moves, or a younger
   approval age is not flagged; young plans cost no `get_slot_plan` call.
   `SLOT_PLAN_STALE_AGE` (Go duration, default **24h** — unlike
   `INBOUND_STALE_RECEIPT_AGE` there IS a default, because "a day-old
   half-executed re-slot" is the operational question the section exists
   to answer) is read with the same `loadDuration` convention as
   `CAPACITY_OUTLOOK_HORIZON`.
3. **A14 — degradation posture.** The section NEVER fails the brief and
   NEVER invents calm: an unreachable slotting-optimization, a failed
   list or per-plan call, an Approved plan published without a parseable
   `approved_at`, or the page bound all land on the section's explicit
   `unavailable` lines (the page bound says the list is a lower bound).
   A configured watch whose plan list comes back empty is the explicit
   `noApproved` state — one line of honesty, never a silently absent
   section and never "all clear". `SLOTTING_OPTIMIZATION_MCP_ENDPOINT`
   unset = not configured: no client, no section, brief unchanged.
4. **A8.** `OrderManagementMCPClient` gains `GetPromiseHealth`
   (`get_promise_health`, window sent as UTC RFC 3339 `from`/`to`, empty
   `pathId` omitted) and loses `GetOrder` together with its `Order` /
   `OrderLine` DTOs: no use case of this agent calls `get_order`, and
   keeping it wired is the same dead-wiring bug class. The REST
   order-lifecycle port (`restclient.OrderManagement`) is a separate
   concern and is untouched. `usecases.PromiseHealth` surfaces ONE
   section in the daily brief over today so far
   ([start of the calendar day, now), no path filter): exactly the KPIs
   the tool returned — the tool has no site dimension, so neither does
   the section. Unreachable order-management degrades to one explicit
   `unavailable` line; unset endpoint omits the section entirely.
5. **A9 — deletion.** The process-path-management client, its port and
   its DTOs are deleted (`git rm` of the client and its test;
   `ProcessPathManagementClient`, `ProcessPath`, `ProcessPathList` and
   the `ppm` wiring in `cmd/agent` removed; `PROCESS_PATH_MANAGEMENT_MCP_ENDPOINT`
   no longer read; the chart's env block and values entry removed with a
   render-test assertion that it never comes back). The endpoint stays
   configured in warehouse-infra's terraform for now — the orchestrator
   removes it in a later infra change; until then the agent simply
   ignores the variable.
6. **Surface.** Both new sections ride the existing daily brief:
   `slotPlanWatch` and `promiseHealth` on `GET /daily-brief` and
   `get_daily_brief`, omitted entirely when their upstreams are
   unconfigured, camelCase JSON, mirrored in both inbound adapters' DTOs.
   No new REST route and no new MCP tool.

## Consequences

- The agent finally answers "which re-slots are rotting half-executed?"
  and "how are today's promises?" with evidence per plan / KPI per
  window, read-only.
- A slotting-optimization or order-management tool-surface change shows
  up here as a failing contract test the moment the golden is refreshed,
  instead of as a silent decode of zero values.
- The slot-plan scan is N+1 by design (one list + one `get_slot_plan`
  per old-enough plan with moves). Approved plans should be few (one
  live per site; supersession moves predecessors to `Superseded`); the
  10-page bound caps the list side. If Approved plans ever grow large,
  an execution-progress field on `list_slot_plans` summaries would be
  the shape to ask slotting-optimization for.
- The promise-health section is network-wide, not per site: the tool
  publishes no site dimension and inventing one here would violate the
  "exactly what the tool returned" posture. A per-site split needs
  order-management to publish it first.
- The daily brief's JSON gains two optional top-level keys; consumers
  that decode strictly must tolerate them (they are `omitempty`-absent
  when the upstreams are unconfigured).
- Deployment: the binary reads `SLOTTING_OPTIMIZATION_MCP_ENDPOINT` and
  `SLOT_PLAN_STALE_AGE` (chart values `upstreams.slottingOptimization.endpoint`
  and `config.slotPlanStaleAge`, both rendered and render-test-asserted);
  warehouse-infra needs one endpoint line to switch the section on in
  the cluster. The process-path-management endpoint in terraform is dead
  configuration until the orchestrator's follow-up removes it.
