---
id: 0019-network-inventory-planning-transfer-watch
title: "0019 — Read-only transfer watch over network-inventory-planning"
sidebar_label: "0019 · NIP transfer watch"
description: "The agent consumes network-inventory-planning's read tools (get_transfer, find_stuck_transfers, simulate_transfer_options) and answers three advisory questions: where is this transfer, which transfers are stuck and why, and which sites are short. It never moves, approves or cancels a transfer."
---

# ADR 0019: Read-only transfer watch over network-inventory-planning

## Status

Accepted. Fourth upstream wave after [ADR 0013](./0013-warehouse-planning-mcp-client-and-capacity-outlook.md)
(warehouse-planning). Builds on [ADR 0018](./0018-mcp-tool-error-slug-classification.md)
for the rejected-input classification.

## Context

network-inventory-planning (NIP) owns inter-warehouse transfers: a saga from
DRAFT to RECEIVED across inventory-storage (allocation, destination receipt),
wes-work-planning and fulfillment-execution (floor work). Its MCP server
(NIP ADR 0008) publishes four READ tools: `get_transfer`, `list_transfers`,
`find_stuck_transfers`, `simulate_transfer_options`. Operators had no way to
ask the agent "where is transfer X", "what is stuck and why" or "which sites
are short" — the saga crosses four contexts and a stuck transfer is, in
practice, a missing message in one of them.

## Decision

1. **A new outbound port, read-only by construction.**
   `ports.NetworkInventoryPlanningClient` exposes exactly the four read tools.
   No method for approving, cancelling or otherwise moving a transfer exists,
   and the zero-write fitness test fails the build if the `mcpclient` adapter
   ever names a write tool. Approval stays an operator action in NIP itself.
2. **One use case, three questions.** `usecases.TransferWatch` answers
   `StuckTransfers`, `TransferStatus` and `NetworkImbalance`, each handing the
   NIP facts to a pure function in `domain/policy`:
   - `TriageTransfer` classifies by state alone (the saga's next step is a
     function of the state): ALLOCATING → inventory reply missing;
     ALLOCATED/PICKED → floor work not progressing; IN_TRANSIT/ARRIVED →
     destination receipt missing; finished states → nothing to triage; any
     other state → *unclassified*, with the raw state quoted. No cause is
     invented, and every non-finished reading carries the next READ-ONLY check
     an operator can make.
   - `ExplainImbalance` reads NIP's own `capacity_headroom` (negative = short).
     No threshold is invented, and it never proposes a quantity or a route.
3. **The staleness threshold is the caller's.** `find_stuck_transfers` needs a
   positive `olderThanMinutes`; the agent has no default and rejects a missing
   or non-positive one as invalid input (HTTP 400) before calling NIP. `state`
   must be a non-terminal state and `limit` is capped at 200 — untrusted model
   input never becomes an unbounded read.
4. **Fail-closed stays fail-closed.** NIP's simulation refuses to answer until
   its read models are fresh. The agent returns that as an upstream error (502),
   never as an empty "balanced" reading.
5. **Surfaces.** REST: `GET /transfer-watch/stuck`, `GET
   /transfer-watch/transfers/{id}`, `GET /transfer-watch/imbalance`. MCP:
   `triage_stuck_transfers`, `get_transfer_status`, `explain_network_imbalance`,
   all annotated read-only. Status mapping: 400 caller input (including an
   upstream validation slug, ADR 0018), 404 unknown transfer, 502 any other
   upstream failure.
6. **Optional and absent when unconfigured.** `NETWORK_INVENTORY_PLANNING_MCP_ENDPOINT`
   (chart: `upstreams.networkInventoryPlanning.endpoint`) empty means no client
   is built, the routes answer 503 and the MCP tools are not registered. Boot
   never depends on NIP, and the daily brief is unchanged.
7. **No network-fulfillment client.** network-fulfillment is the fleet's
   anti-corruption layer to an external retail network and stays a deliberate
   "Separate Ways" relationship in this agent's context map.

## Consequences

- An operator (or an LLM through MCP) can localise a stuck transfer to a saga
  stage in one call, with the next check to make, without leaving the agent.
- The triage is only as fine as the state: it says where the saga is waiting,
  not why the other context is silent. That is the operator's check.
- A new NIP state outside the classified set degrades to *unclassified*, not to
  a wrong cause.
- The agent still writes nothing to any context.
