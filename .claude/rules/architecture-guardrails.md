# Rule: architecture guardrails for warehouse-ops-agent

Source of truth: `internal/architecture/architecture_test.go`,
`docs/docs/adr/0001-warehouse-ops-agent-placement.md`,
`docs/docs/adr/0004-llm-reasoner-behind-the-policy-layer.md`,
`docs/docs/mcp/governance-note.md`.

## Non-negotiable, CI-enforced

1. **Never import a Go package from any upstream bounded context.**
   `TestNoDirectDependencyOnBoundedContexts` asserts it for the five
   original contexts (`fulfillment-execution`, `wes-work-planning`,
   `workforce-management`, `inventory-storage`, `facility-layout`) by
   scanning `go.mod`/`go.sum` for those module paths and fails the build if any
   appear — the check runs even if nothing today imports them, so it fails
   loudly the moment one is added. All cross-context integration must go
   through:
   - a published MCP tool call (`internal/adapters/outbound/mcpclient`), or
   - a plain REST call to the context's own OLTP/reports API
     (`internal/adapters/outbound/restclient`) — console-bff only.
2. **Hexagonal dependency direction is one-way inward**, enforced by
   `TestHexagonalDependencyRules`:
   - `internal/domain/policy` depends on nothing but itself.
   - `internal/application` depends only on `domain`, `application`, `ports`.
   - `internal/ports` depends on nothing internal.
   - `internal/adapters/inbound/**` never imports `internal/adapters/outbound/**`
     (and vice versa).
   - Nothing under `internal/**` imports `cmd/**`; only `cmd` wires every
     layer together.
   `TestMCPAdapterDependencyRule` and `TestNoAuthMiddlewareReintroduced`
   (`fitness_test.go`) add the MCP-adapter boundary and the auth-removal
   guard.
   Run `go test ./internal/architecture/... -v` (or `make arch-test`) after
   touching any adapter, port, or import statement — not just before push.
3. **This repo owns no aggregate, enforces no invariant, persists no
   state.** If a change starts to need one of those three things, that is
   a signal the change belongs in a different repo (a new or existing
   bounded context), per ADR 0001's Consequences — not something to add
   here.
4. **Zero write tools today, CI-enforced** by
   `internal/architecture/zerowrite/zerowrite_test.go`. Do not add an `AssignLabor`,
   `ReleaseNextWork`, `RevokeReservation`, or similar mutating method to
   any outbound client, and do not register a non-read-only MCP tool on
   this agent's own inbound server, without first re-reading
   `docs/docs/mcp/governance-note.md`'s "future act slice" guardrails
   (a re-introduced authorization gate + explicit human confirmation before
   execution are both required, not optional, for that slice).
5. **LLM reasoner (ADR 0004) actuator surface is MCP read tools only,
   allow-listed.** The model must never gain a raw HTTP client, database
   handle, or shell. Any change to what the model can call must go through
   `LLM_TOOL_ALLOWLIST` and the same schema-validated `mcpclient` sessions
   the deterministic path already uses — never a new, separate client. An
   unrecognized `LLM_MODE` must remain a startup error, never silently
   degrade to `off`.
6. **Untrusted input is rejected, never defaulted.** Any enum-like field
   this agent accepts (MCP tool args, REST query params, the LLM's own
   `submit_plan` output) must reject an unknown value explicitly (see
   `listOpenExceptionsInput.Severity`'s `severityRank` pattern and
   `policy.ValidatePlan`) — do not add a "fall back to a sensible default"
   branch for an out-of-vocabulary value on any of these paths.

7. **Domain types are hand-mirrored, not imported.** Enums like
   `RebalanceAction`/`TaskType` in `internal/domain/policy` are local copies
   of the upstream vocabulary, validated at the MCP tool-call boundary
   rather than type-shared.
8. **No OpenAPI/AsyncAPI spec, by design.** This repo produces no
   REST/event contract for other services to consume against a schema; its
   REST + MCP surface is documented by hand in `docs/docs/api-surface.md`
   and kept in sync with `internal/adapters/inbound/http` and
   `internal/adapters/inbound/mcp` by convention, not generation. If asked
   to "add OpenAPI docs" here, check first whether the ask belongs to one
   of the upstream bounded-context repos instead.

## Role in the fleet

Per `docs/docs/adr/0001-warehouse-ops-agent-placement.md` and
`docs/docs/ddd/subdomain-classification.md`, this agent is deliberately NOT
classified alongside the fleet's Core/Supporting/Generic bounded contexts.
It is a read-side/decision-support mechanism: a CQRS-style read model that
spans context boundaries, plus a policy layer. It is a Customer of the
upstream contexts' published MCP Open Host Services (plus plain REST for
console-bff). `TestNoDirectDependencyOnBoundedContexts` covers the five
original module paths plus warehouse-planning; the three second-wave clients
(ADR 0007) follow the same rule even though the test does not list them.
warehouse-planning's MCP server is read+write (ADR 0013), so its client is
held read-only by `zerowrite.TestMCPClientsCallOnlyReadTools`, which fails
the build if any `callTool` literal in `mcpclient` starts with a write verb
(`create_`, `publish_`, `register_`, `declare_`, …).

## Auth posture (as of ADR 0006, 2026-09-09)

Auth is fully removed fleet-wide, not just disabled. Do not reintroduce
`OIDC_ISSUER_URL`/`OIDC_CLIENT_ID`, `MCP_READ_KEY`/`MCP_READWRITE_KEY`, or
per-upstream `*_MCP_READ_KEY` env vars from old references you may find in
git history or stale docs — the implementations were deleted, not
feature-flagged off. Reintroducing identity later means redesigning from a
real requirement, per ADR 0006's Consequences.
