---
id: 0006-fleet-wide-auth-removal
title: "0006 — Fleet-wide auth removal: REST OIDC and MCP static bearer keys withdrawn"
sidebar_label: "0006 · Fleet-wide auth removal"
description: The fleet-wide decision to remove REST OIDC/static-bearer identity and MCP static bearer keys from every service, superseding ADR 0005, and what that means for this agent's inbound REST/MCP surfaces and its outbound MCP-client calls to the five upstream contexts.
---

# ADR 0006: Fleet-wide auth removal — REST OIDC and MCP static bearer keys withdrawn

## Status

Accepted

**Date:** 2026-09-09

**Scope:** **fleet-wide.** Recorded here for the same reason ADR 0005 was:
  this service is the fleet's cross-context Customer, holding its own
  inbound REST/MCP surfaces *and* the outbound MCP-client bearer keys for
  all five upstream contexts. Each context records its own short adoption
  note pointing back to this one.

**Supersedes:** [ADR 0005](./0005-rest-identity-static-bearer-scopes.md)
  (Fleet REST identity: static bearer keys with read/read-write scopes, no
  IdP). ADR 0005 is left in place, unmodified, as the historical record of
  why the posture existed; this record is the fleet-wide decision to
  remove it.

## Context

The fleet's auth posture — OIDC on this repo's inbound REST API, static
bearer keys everywhere else (MCP servers fleet-wide, REST elsewhere per
ADR 0005) — added real operational weight (Kubernetes Secrets, key
rotation via Terraform re-apply, `AUTH_MODE` rollout gates, JWKS discovery
at startup) without a consumer that depended on it: every caller in this
fleet is either another in-cluster service or a developer running the
harness locally, and the fleet's own e2e-tests, kanban workflows, and
demo/study-project usage never needed per-caller attribution. The owner's
decision: strip auth from the entire fleet — REST and MCP, inbound and
outbound — down to open surfaces, and revisit identity if a real consumer
of it ever appears.

## Decision

1. **This agent's inbound REST API** (`internal/adapters/inbound/http`)
   drops the OIDC `Middleware` entirely. `NewRouter` no longer takes an
   `authn` parameter; every route it serves is open. `OIDC-AUTH-SPEC.md`
   and `internal/adapters/inbound/auth/` (the OIDC discovery/JWT verifier
   this repo originated) are deleted.
2. **This agent's inbound MCP server** (`internal/adapters/inbound/mcp`)
   drops `StaticKeyAuth`, `Scope`, and the `Authenticator` seam.
   `Handler` no longer takes an auth argument; `/mcp` is unauthenticated.
   `auth.go`/`auth_test.go` in that package are deleted.
3. **This agent's outbound MCP-client adapter**
   (`internal/adapters/outbound/mcpclient`) drops the
   `Authorization: Bearer` header injection (`bearerRoundTripper`) and the
   `BearerKey`/`ReadKey` config fields used to call the five upstream
   contexts' MCP servers (`wes-work-planning`, `fulfillment-execution`,
   `inventory-storage`, `workforce-management`, `facility-layout`). Those
   five servers drop their own inbound MCP auth in sibling PRs in the same
   effort, so the header this agent used to send has no verifier on the
   other end regardless.
4. **Composition roots** (`cmd/agent/main.go`, `cmd/agent/reasoner.go`)
   drop `OIDC_ISSUER_URL`/`OIDC_CLIENT_ID` parsing and validation,
   `MCP_READ_KEY`/`MCP_READWRITE_KEY` parsing, and the five
   `*_MCP_READ_KEY` env reads. `internal/config.Config` drops the
   corresponding fields.
5. **Helm chart** (`charts/warehouse-ops-agent`) drops the `oidc:` values
   block, the `credentials.*ReadKey`/`credentials.mcpReadKey`/
   `credentials.mcpReadWriteKey` values, and the matching Secret/ConfigMap/
   Deployment env entries. The Secret template now only ever carries
   `ANTHROPIC_API_KEY` (still required, still never logged) and renders
   nothing when that key is unset.
6. **Docs** stay in sync by hand: the API-surface page, the governance
   note's future act-slice guardrail paragraph, and the README's
   configuration table all drop the auth-specific language. ADR 0005
   itself is left unedited (immutable per the ADR convention this repo's
   index documents) but is marked superseded in the index table and at
   the top of this record.

## Consequences

**Positive** — every Secret, `AUTH_MODE` env var, JWKS discovery call, and
bearer-header round trip this repo carried is gone; `go build`,
`go vet`, and the full test suite are simpler with no auth fixtures to
maintain (`allowAuth{}` test doubles, OIDC test issuers, static-key
tables). Local development and the e2e harness need one fewer category of
generated secret to wire through Terraform.

**Negative / accepted** — every REST and MCP call in the fleet is now
unattributable to any caller, and the five upstream contexts' MCP servers
this agent depends on are equally open to anyone who can reach their
Service — this repo's own posture change is only half the picture, and it
was only safe to make because the sibling contexts made the matching
change in the same effort. There is no scope distinction, no rotation
story, and no audit trail beyond what OTel spans already record (tool
name only, no caller identity). Re-introducing identity later — for this
agent's inbound surfaces, for the five upstream MCP servers, or both —
means redesigning from a real requirement rather than restoring what
existed here, since the OIDC/static-key implementations are deleted, not
disabled.

## Alternatives considered

- **`AUTH_MODE=off` everywhere, code left in place.** Rejected: keeping
  dead auth code and Secret plumbing around "in case it's needed again"
  is exactly the operational weight this decision removes; ADR 0005's
  own `log`/`off` rollback path already proved the code could sit unused
  without deleting it, and the owner chose to finish the job instead.
- **Keep MCP auth, drop REST auth only (or vice versa).** Rejected for
  consistency: this repo is both a REST/MCP server and an MCP client of
  five other servers, so a partial removal would leave some calls
  authenticated and others not for no principled reason tied to any real
  threat model.
