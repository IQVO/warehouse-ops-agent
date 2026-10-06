---
id: index
title: Architecture Decision Records
sidebar_label: About ADRs
description: Why these records exist, the template they follow, and how to propose a new one.
---

# Architecture Decision Records

An **Architecture Decision Record** captures one architecturally
significant decision: what was decided, what was going on at the time
that made the decision necessary, and what the team now has to live with
as a result. These follow the same convention every warehouse-systems
repository uses: **Michael Nygard's** lightweight format, one markdown
file per decision, numbered `0001-`, `0002-`, and so on, immutable once
accepted.

## The records

| # | Title | Status |
|---|---|---|
| [0001](./0001-warehouse-ops-agent-placement.md) | warehouse-ops-agent placement as a new, thin read-side repo | Accepted |
| [0002](./0002-micro-frontend-console-architecture.md) | Micro-frontend console architecture over per-service REST, with a thin BFF for cross-service reads | Accepted |
| [0003](./0003-console-bff-report-dashboards.md) | Console-BFF report dashboards aggregating every context's analytics endpoint | Accepted |
| [0004](./0004-llm-reasoner-behind-the-policy-layer.md) | A real LLM reasoner behind the policy layer, with MCP tools as its only actuators | Accepted (implemented for flow-balance) |
| [0005](./0005-rest-identity-static-bearer-scopes.md) | Fleet REST identity: static bearer keys with read/read-write scopes, no IdP | Superseded by [0006](./0006-fleet-wide-auth-removal.md) |
| [0006](./0006-fleet-wide-auth-removal.md) | Fleet-wide auth removal: REST OIDC and MCP static bearer keys withdrawn | Accepted (fleet-wide) |
| [0007](./0007-second-wave-outbound-mcp-clients.md) | Second-wave outbound MCP clients: order-management, labor-performance, process-path-management wired but unconsumed | Accepted |
| [0008](./0008-labor-utilization-advisory-correlation.md) | Labor-utilization advisory correlation: correlating queue depth with observed idleness | Accepted |
| [0009](./0009-explain-travel-factor.md) | explain_travel_factor: correlating a real travel-distance reading against a slow path, with no location-code auto-resolution | Accepted |
| [0010](./0010-standard-metrics-convention.md) | Standard metrics convention across the fleet | Accepted |
| [0011](./0011-reasoner-path-circuit-breaker-timeout-retry.md) | Reasoner-path resilience: circuit breaker, deadline-derived timeout, bounded retry | Accepted |
| [0012](./0012-horizontal-autoscaling-single-deployment.md) | HorizontalPodAutoscaler for the single Deployment, and switching the inbound MCP server to stateless mode | Accepted |
| [0013](./0013-warehouse-planning-mcp-client-and-capacity-outlook.md) | warehouse-planning MCP client (read tools only) and the fail-open capacity outlook in the daily brief | Accepted |
| [0014](./0014-runtime-signals-and-stranded-reservation-adoption.md) | Adoption record: GET /runtime-signals and detect_stranded_reservation (E2) | Accepted (adoption record) |
| [0015](./0015-core-flow-balance-and-daily-brief-adoption.md) | Adoption record: E1 flow-balance correlation and E3 daily brief (core use cases) | Accepted (adoption record) |

## Proposing a new one

1. Copy the most recent record and renumber it. Numbers are never reused,
   even if a record is later withdrawn.
2. Write the **Context** first, and write it neutrally.
3. Write **Consequences** honestly, including the ones you dislike.
4. Open it as `Proposed`. Flip it to `Accepted` when it is agreed, or
   leave it `Proposed` and supersede it later if the discussion goes
   elsewhere.
5. Add it to the table above and to `sidebars.ts`.

See [facility-layout's ADR index](https://iqvo.github.io/facility-layout/docs/adr)
for the fuller template rationale this convention is copied from.
(`facility-layout`'s `docusaurus.config.ts` now publishes under
`iqvo.github.io`, like this repo's.)
