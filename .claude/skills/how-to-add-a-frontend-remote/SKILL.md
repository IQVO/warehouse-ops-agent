---
name: how-to-add-a-frontend-remote
description: Add or change a micro-frontend remote under web/ (vite federation config in object form, /mfes/<context>/ base, remoteEntry, Docker/nginx packaging, console integration). Use when touching web/.
---

# How to add a frontend remote — does not apply to this repo

**This file is a deliberate redirect, not a how-to.** inventory-storage's
`.claude/skills/how-to-add-a-frontend-remote/SKILL.md` (PR #75) documents adding
a Vite/React Module Federation remote under a bounded-context repo's own
`web/` directory. `warehouse-ops-agent` has **no `web/` directory and no
frontend remote of its own, by design** — it is not a bounded context
(ADR 0001) and it is not one of the fleet's twelve Module-Federation
remotes.

## Why there is nothing to adapt here

`warehouse-console` (the shell) does not lazy-load a `warehouse-ops-agent`
remote the way it lazy-loads `order_mgmt_mfe`, `facility_mfe`, and the
other seven bounded-context remotes. Instead, this agent backs
`warehouse-console`'s **cross-cutting screens** two different ways, both
plain REST, neither Module Federation:

- The **Floor screen** (`/`) reads this agent's `GET /daily-brief`
  directly from the browser.
- The **Order Lifecycle** and **WMS/WES report dashboard** screens read
  this agent's **console-bff** fan-out routes (`GET
  /console/orders/{id}/lifecycle`, `GET /console/reports/{wms,wes}` — see
  ADR 0002 and ADR 0003 in `docs/docs/adr/`), which THIS agent implements
  as a server-side aggregation layer over four/seven upstream contexts'
  REST endpoints.

In other words: where a bounded-context repo exposes its UI as a
lazy-loaded remote the shell hosts, this repo exposes its UI-facing data
as a REST API the shell's own React code calls — there is no bundle, no
`remoteEntry.js`, no Module Federation plugin config, and no `web/`
directory to add one to. Inventing a frontend remote for this repo would
contradict ADR 0001 and ADR 0002's explicit division of responsibility.

## Where the console-bff pattern THIS repo implements is actually documented

- `docs/docs/adr/0002-micro-frontend-console-architecture.md` — why a
  thin BFF living inside this agent, not a new service or a remote, was
  chosen for the Order Lifecycle screen.
- `docs/docs/adr/0003-console-bff-report-dashboards.md` — the WMS/WES
  report-dashboard extension of the same pattern.
- `docs/docs/api-surface.md` — this agent's own hand-documented REST
  surface, including the three console-bff routes.
- `.claude/skills/how-to-add-a-rest-endpoint/SKILL.md` — the actual
  how-to for adding a NEW route here, including a console-bff route.

## Where the shell-side half of this pattern is documented

For the Module-Federation-remote-adding workflow itself (which genuinely
does not apply here), or for how `warehouse-console` consumes this
agent's REST/console-bff surface from its own React code, see
`warehouse-console`'s own docs, not this repo's:

- `warehouse-console/docs/docs/architecture/module-federation.md` — the
  shell-side remote-hosting contract (`RemoteBoundary`, lazy-loading
  convention) for the twelve bounded-context remotes.
- `warehouse-console/docs/docs/architecture/cross-cutting-screens.md` —
  how the Floor/Order-Lifecycle/WMS/WES screens consume THIS agent's
  REST and console-bff routes from the browser side.
- `warehouse-console/CLAUDE.md` — the shell's own top-level orientation,
  which states this agent's relationship to it plainly ("this shell is a
  downstream Conformist to warehouse-ops-agent's console-bff").

If a future task genuinely needs this agent to ship its OWN Module
Federation remote (a plausible-sounding but currently nonexistent ask —
e.g. an embeddable "daily brief" widget), that would be a new
architecturally significant decision requiring its own ADR (see
`.claude/skills/how-to-write-an-adr/SKILL.md`) before any `web/` directory gets created here,
not a mechanical application of inventory-storage's guide.
