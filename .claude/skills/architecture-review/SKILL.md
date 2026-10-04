---
name: architecture-review
description: Bounded-context boundary and ADR-compliance review of a change (expensive, post-integration): hexagonal direction, cross-context coupling, contradicted ADRs. Invoke explicitly: /architecture-review [range].
disable-model-invocation: true
argument-hint: "[git range]"
---

Perform a bounded-context boundary and ADR-compliance review of the
current changes (or `$ARGUMENTS` if given, e.g. a branch/PR diff range).

This is EXPENSIVE relative to `/code-review` — it reasons about
cross-repo/cross-context implications, not just this diff's local
correctness. Use it post-integration (before merging a PR that touches
architecture, not on every small commit) or when asked explicitly to
check a design decision against this fleet's standing architecture.

## What to check, in priority order

1. **Hexagonal dependency direction.** Domain depends on nothing;
   application depends on domain+ports; adapters depend on
   application+domain; only `cmd/` wires every layer together. Run
   `go test ./internal/architecture/... -v` first — if it's already red,
   report that and stop; don't hand-review what a fitness test already
   caught.
2. **Customer direction toward upstream contexts, per
   `.claude/rules/architecture-guardrails.md` and
   `docs/docs/ecosystem/context-map.md`.** A new outbound call must go
   the direction the ADRs already established — check `docs/docs/adr/`
   (ADR 0001 placement, ADR 0007 second-wave clients) before assuming a
   new integration is fine. Flag any outbound call added to a context
   this repo doesn't already integrate with; that is a new architectural
   decision that needs its own ADR, not a code change slipped in
   silently.
3. **Zero-write guardrail (this repo's hard rule, v1).** No mutating HTTP
   method literal in `internal/adapters/outbound/{restclient,mcpclient}`
   and no MCP tool registered without `ReadOnlyHint: true` in
   `internal/adapters/inbound/mcp/tools.go`. Confirm
   `internal/architecture/zerowrite/zerowrite_test.go` is green (it runs
   under `make arch-test`); also flag by eye any new outbound method
   named like an action (`Assign...`, `Release...`, `Revoke...`) — see
   `docs/docs/mcp/governance-note.md` for the two guardrails a future
   write slice must satisfy first.
4. **MCP adapter boundary.** A change under `internal/adapters/inbound/mcp/`
   must depend only on application/domain, and nothing else in the
   codebase may depend on it. `TestMCPAdapterDependencyRule`
   (`internal/architecture/fitness_test.go`) enforces it — confirm it is
   green.
5. **No aggregate, no invariant, no persisted state (ADR 0001).** This
   agent holds no database and owns no aggregate. A change that adds a
   store, a cache that outlives a request, or a domain invariant means the
   work belongs in a bounded-context repo, not here.
6. **No Kafka I/O today.** This service neither produces nor consumes
   Kafka. A change that adds a Kafka client is a new architectural
   decision: it needs its own ADR, a row in the fleet subdomain table, and
   CloudEvents 1.0 structured mode (see CLAUDE.md, "Events"). Flag it even
   if the code itself is correct.
7. **A new cross-repo contract with no companion documentation.** If this
   change introduces or changes a cross-repo contract (a new REST call, a
   new MCP tool consumed from a sibling), check whether an ADR documents
   the decision — and whether a companion ADR should exist in the OTHER
   repo too, per this fleet's companion-ADR convention (see
   `.claude/skills/how-to-write-an-adr/SKILL.md`).
8. **Auth-reintroduction and sibling-call bans**, same as `/code-review`
   items 6-7, but reasoned about more thoroughly here — check not just
   "is there a Bearer literal" but "does this change's INTENT require
   re-litigating the fleet-wide auth-removal decision (ADR 0006)," which
   would need its own ADR, not a silent code change.

## Output format

State clearly: PASS (no architectural concerns), CONCERNS (list them,
each tied to the specific rule/ADR it would violate), or NEEDS-ADR (the
change is architecturally sound but undocumented — name what the ADR
should cover). Cite the specific file/rule/ADR for every finding; a
finding with no citation is not actionable.

This is advisory. It never blocks a merge on its own, and it never
modifies files. If a finding conflicts with a decision explicitly stated
in this repo's own AGENTS.md/CLAUDE.md, defer to that document and say
so — this command reasons about the fleet's general conventions, not a
higher authority than the repo's own explicit guidance.
