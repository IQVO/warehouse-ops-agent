---
id: 0013-warehouse-planning-mcp-client-and-capacity-outlook
title: "0013. warehouse-planning MCP client (read tools only) and the fail-open capacity outlook in the daily brief"
sidebar_label: "0013 · warehouse-planning client"
description: "ADR 0013 — wires the new warehouse-planning bounded context's published MCP read tools into warehouse-ops-agent behind a port; consumes one of them (get_process_path_capacity) in an additive, fail-open capacity-outlook section of the daily brief, with every workload input operator-supplied and nothing invented; leaves the other three read tools wired but unconsumed (ADR 0007's third state); never names a write tool."
---

# 0013. warehouse-planning MCP client (read tools only) and the fail-open capacity outlook in the daily brief

## Status

Accepted. Third wave of outbound MCP clients, after the five original
contexts and [ADR 0007](./0007-second-wave-outbound-mcp-clients.md)'s
three.

## Context

`warehouse-planning` (repo `IQVO/warehouse-planning`) is a new bounded
context that owns capacity: registered per-step capacity constraints,
declared per-station standards, process paths and capacity plans. Its MCP
server (Streamable HTTP, `:8090`, no auth) publishes ten tools
(`.claude/rules/mcp.md` in that repo is the contract; this repo never
imports its Go code):

| Tool | R/W | Summary |
|---|---|---|
| `get_process_path_capacity` | read | a path's end-to-end capacity at a `location` over a window: `normalized_rate` (ORDER/hour), `bottleneck_step`, `step_breakdown[{step, normalized_rate, binding_constraint}]`, `warnings[]` |
| `get_capacity_plan` | read | a stored plan by id: path capacity, shortage, bottleneck |
| `get_storage_capacity` | read | storage positions and station counts per zone at a site |
| `list_station_standards` | read | the declared per-station throughput, optionally per site |
| `get_effective_process_capacity` | read | one step's effective rate for an EXACT window |
| `register_process_capacity_constraint`, `register_process_path`, `create_capacity_plan`, `publish_capacity_plan`, `declare_station_standard` | **write** | mutate planning state |

Three facts about its semantics shape this decision:

- `location` is a **site/building code** (e.g. `SIM1`), the same facility-layout
  site code this agent already carries as `PathTarget.SiteCode`.
- Capacity is resolved by window **coverage**: a registered window applies
  when it covers the requested one. An unplanned window is not a zero; it is
  a `missing-step-capacity` tool error.
- A path's capacity is only defined for a path **registered in planning**
  (`register_process_path`: an ordered list of steps such as PICK, REBIN,
  PACK). That id is planning's own vocabulary, not wes-work-planning's
  `PathId`, and `get_process_path_capacity` additionally needs
  `units_per_order` / `packages_per_order` whenever a step is measured in
  UNIT / PACKAGE.

Unlike every other upstream this agent reads, planning's MCP server is
**read+write**. The agent's v1 rule is zero write capability
(`internal/architecture/zerowrite`), so the read-only guarantee cannot rest
on the server for this context.

## Decision

1. **Add `mcpclient.WarehousePlanning` behind `ports.WarehousePlanningClient`**
   (`internal/ports/clients_planning.go`), shaped exactly like its siblings
   (embedded `*Session`, one method per tool, `var _ ports.X = (*X)(nil)`,
   hand-mirrored DTOs). Exactly four tools, all `ReadOnlyHint` upstream:
   `get_process_path_capacity`, `get_capacity_plan`, `get_storage_capacity`,
   `list_station_standards`.
2. **Read-only rule, enforced.** No `register_*`, `create_*`, `publish_*` or
   `declare_*` tool is named anywhere. A new fitness test,
   `zerowrite.TestMCPClientsCallOnlyReadTools`, scans every non-test file in
   `internal/adapters/outbound/mcpclient` for `callTool(ctx, "<tool>", …)`
   literals and fails the build if one starts with a write verb. The client
   test also serves a `create_capacity_plan` tool and fails if it is invoked.
3. **Configuration** follows the sibling convention:
   `WAREHOUSE_PLANNING_MCP_ENDPOINT` (Streamable-HTTP URL, unauthenticated,
   [ADR 0006](./0006-fleet-wide-auth-removal.md)); Helm
   `upstreams.warehousePlanning.endpoint`. Unlike the siblings, an **unset
   endpoint builds no client at all** (the composition root assigns a genuinely
   nil interface) and boot never depends on it; the outlook below is then not
   wired and the brief is byte-for-byte unchanged. A second optional variable,
   `CAPACITY_OUTLOOK_HORIZON` (Go duration, default `8h`, invalid or
   non-positive values fall back to the default like `LLM_TIMEOUT`), sets how
   far ahead the window extends.
4. **Resilience mirrors the closest sibling (`labor-performance`)**: the
   shared `Session` per-call timeout (10 s default), a fresh session per call,
   no retry, no circuit breaker (those exist only on the Anthropic call path,
   [ADR 0011](./0011-reasoner-path-circuit-breaker-timeout-retry.md)). Every
   failure is an ordinary returned error. The *consuming* use case chooses
   **fail-open** (below), the same choice `DailyBrief` already makes for each
   of its sources.
5. **One consumer: a `CapacityOutlook` section in the daily brief**
   (`usecases.CapacityOutlook`, composed into `DailyBrief.Outlook`; pure
   shaping in `policy.SummarizeCapacityOutlook`). For each `PathTarget` that
   the operator has bound to planning, it calls `get_process_path_capacity`
   for `[now, now+CAPACITY_OUTLOOK_HORIZON)` and attaches to that path's
   `PathBrief.CapacityOutlook` (HTTP `GET /daily-brief` and MCP
   `get_daily_brief`, key `capacityOutlook`): `normalizedRate` (ORDER/hour),
   `bottleneckStep`, `bindingConstraint` (taken from the bottleneck step's
   `step_breakdown` entry), the per-step breakdown and planning's own
   `warnings`.
6. **No invented inputs.** The path binding and the workload factors are
   deployment facts only the operator knows, so they extend the existing
   `DAILY_BRIEF_PATH_TARGETS` JSON (the same place `buildingId`/`shiftId` bind
   a path to workforce-management): optional `planningPathId`,
   `unitsPerOrder`, `packagesPerOrder`. `location` is the target's existing
   `siteCode`. There is **no default** for any of them: an unset factor is
   omitted from the tool call (a pointer distinguishes unset from an invalid
   `0`, which planning rejects), and planning's own
   `missing-conversion-factor` error then surfaces as the section's reason.
   The built-in default target carries no planning binding.
7. **Fail-open and additive.**
   - Not configured (nil `Outlook`): no section, no `unavailable` entry;
     existing output and tests are unchanged.
   - Configured: a path with no `planningPathId`, an unreachable server, a tool
     error (including `missing-step-capacity` = no covering window), a
     non-`ORDER` unit or a negative rate each yield
     `capacityOutlook.omittedReason` with **no** rate fields (a missing rate is
     never rendered as 0; a real 0 is rendered as 0). The brief never fails
     and no other field changes.
   - The outlook is informational: it never feeds the exception rules, so
     `openExceptions` is identical with or without it.
8. **The other three tools stay wired but unconsumed** (ADR 0007's third
   state): `get_capacity_plan`, `get_storage_capacity`,
   `list_station_standards` have a port method, a client method and full
   tests, and no use case calls them. The port is reachable from the
   composition root as `clients.planning` (the same value the outlook uses).

## Consequences

**Positive.** The daily brief can show planned capacity and its binding
constraint next to live backlog/staffing/queue facts, from the context that
owns capacity, with no capacity math duplicated here. A later use case (plan
shortage vs. backlog, storage headroom, a station-standard explainer) needs
no new adapter, port or config.

**Negative / accepted.**
- Three more read methods have no caller yet (the ADR 0007 trade-off).
- The outlook is **inert until an operator maps paths**: with only the
  endpoint set, every path reports `omittedReason: "no planningPathId
  configured…"`. That is deliberate (no guessing wes-path → planning-path
  names), and it makes the missing configuration visible instead of silent.
- The window is `[now, now+horizon)` to the second. If planning's registered
  windows are shift- or day-aligned and do not *cover* it, the answer is
  `missing-step-capacity`, shown as the reason, not repaired by rounding.
- One extra upstream call per mapped path per brief, sequential like its
  siblings, each bounded by the 10 s session timeout.
- The read-only guarantee for this context is enforced by this repo's own
  static scan, not by the server; the scan is prefix-based, so a future
  write tool with an unlisted verb would need that list extended.

## Alternatives considered

- **Client only, wired but unconsumed (ADR 0007).** Rejected for the outlook,
  because the sibling config pattern (`DAILY_BRIEF_PATH_TARGETS`) already
  carries exactly the per-path operator facts the call needs, so a consumer can
  be added without defaulting or fabricating anything. Chosen for the other
  three tools.
- **Default `units_per_order` / `packages_per_order`.** Rejected: any value
  would be a made-up workload assumption that silently scales every reported
  rate. Planning already reports a missing factor precisely; we surface that.
- **Reuse `PathTarget.PathId` as planning's path id.** Rejected: contexts own
  their ubiquitous language and these ids are not guaranteed equal (the same
  reason `ProcessPath`/`BuildingId` are separate fields).
- **Expose `get_effective_process_capacity`.** Not added: it keys on EXACT
  windows, which an agent asking "the next N hours" would almost never match;
  `get_process_path_capacity` (coverage semantics) answers that question.
- **Add an exception rule (outlook below backlog ⇒ warning).** Rejected for
  now: comparing an ORDER/hour rate to a backlog count needs a unit and
  time-horizon policy no one has decided; the section stays informational.
- **A circuit breaker on the planning client.** Rejected: no sibling MCP
  client has one (ADR 0011 scopes it to the LLM path), and fail-open per call
  plus the 10 s timeout already bound the cost of a down upstream.
