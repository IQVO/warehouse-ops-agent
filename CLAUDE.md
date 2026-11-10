# CLAUDE.md — warehouse-ops-agent

<!-- harness-template: v4 -->

`warehouse-ops-agent` is a single Go binary: a **thin, read-side /
decision-support Customer** of the warehouse-systems fleet's bounded contexts,
reached only through their published MCP Open Host Services (plus plain REST
for console-bff, and Prometheus/Loki for runtime signals). It owns no
aggregate, enforces no domain invariant and holds no database; a pure
decision-**policy** layer correlates what it reads, optionally consulting an
LLM reasoner behind that layer (ADR 0004).

> ⚠️ **Study project.** A personal DDD/hexagonal learning exercise. Treat
> "production-grade" language as illustrating the pattern, not an operational
> claim.

## Hard rules (each one is CI-enforced or paid for by an incident)

- **WRITE CAPABILITY = the two act slices ONLY (ADR-0027, ADR-0029) — architecture rule, never

The act slice's propose response also carries an advisory, read-only `exceptionContext` from exception-management over A2A (ADR 0028, opt-in `EXCEPTION_MANAGEMENT_A2A_URL`); the a2a adapter holds exactly three clients (release, exception context, labor assignment), the second implements only `ExceptionContext.OpenExceptions`, and it never gates or alters minting. Every MCP tool declares a `com.warehouse/authority` level (L1 read, L2 recommend; ADR-0029). A2A carries delegation only; the fleet rule is `.claude/rules/fleet/a2a.md`.
  widen it.** The two writes are `release_next_work`
  (`POST /act/release-next-work/{proposals,approvals}`, delegated to
  wes-work-planning, opt-in via `WES_WORK_PLANNING_A2A_URL`) and `assign_labor`
  (`POST /labor-reallocation/proposals` and `.../{proposalId}/approvals`,
  ADR-0029, delegated to workforce-management, opt-in via
  `WORKFORCE_MANAGEMENT_A2A_URL`, which also needs
  `WORKFORCE_MANAGEMENT_MCP_ENDPOINT`). Both are human-confirmed (HMAC
  confirmation token), REST-only (never an MCP tool, never reachable by the LLM).
  `internal/architecture/zerowrite/act_slice_test.go` pins exactly those
  two slices; `RevokeReservation`/`CompleteTask` stay
  recommendation-only. Everything below about "zero write" still holds for
  every other surface, and the two original zerowrite tests are unchanged.
  The original v1 rule, kept for the surfaces it still governs: No
  mutating HTTP method literal in `internal/adapters/outbound/{restclient,mcpclient}`,
  and every tool on this agent's own MCP server is `ReadOnlyHint: true`.
  `internal/architecture/zerowrite/zerowrite_test.go` (part of `make arch-test`
  and CI's `arch-test` job) fails the build the moment either surface grows
  write capability. Outside the two act-slice ports (ADR-0027, ADR-0029) there is no
  write verb here and you must not add one. A future write slice
  needs an authorization gate + explicit human confirmation first
  (`docs/docs/mcp/governance-note.md`).
- **Never import a Go package from an upstream bounded context.** Only their
  published MCP tool contracts (and, for console-bff, plain REST) are valid
  integration points. Upstream domain types are hand-mirrored, not imported.
  `TestNoDirectDependencyOnBoundedContexts` enforces it.
- **Hexagonal direction is one-way inward** (`TestHexagonalDependencyRules`):
  domain depends on nothing, inbound never imports outbound, only `cmd/` wires
  layers. Run `make arch-test` after touching any adapter, port or import — not
  just before push.
- **No aggregate, no invariant, no persisted state.** If a change needs one,
  it belongs in a bounded-context repo (ADR 0001), not here.
- **The LLM reasoner's only actuators are allow-listed MCP read tools**
  (`LLM_TOOL_ALLOWLIST`). It must never gain a raw HTTP client, DB handle or
  shell. An unrecognized `LLM_MODE` is a startup error, never a silent
  fallback to `off`.
- **Untrusted input is rejected, never defaulted.** Unknown enum values (MCP
  args, REST params, the LLM's `submit_plan`) are rejected explicitly.
- **Auth is fully removed fleet-wide (ADR 0006), not disabled.** Do not
  resurrect `OIDC_ISSUER_URL`, `MCP_READ_KEY`, `*_MCP_READ_KEY` or any bearer
  logic seen in old commits/docs; they are gone by design.
- **Events: CloudEvents 1.0 (structured mode) is MANDATORY.** This service has
  no Kafka I/O today. Any future Kafka message must be a CloudEvents 1.0 event:
  no flat envelope, no dual-write/dual-read, no envelope toggle. Full standard:
  `.claude/rules/events-cloudevents.md` — read it BEFORE adding any Kafka code.
- **`apis/openapi.yaml` is the REST contract** (every router route, RFC 9457
  problem+json errors); no AsyncAPI, since there is no Kafka I/O. CI's
  `api-lint` lints it with `.spectral.yaml` (fleet REST guideline, no
  exemptions: every act POST takes an `Idempotency-Key`, ADR 0030; the
  deprecated `.../approve` alias is served but kept out of the spec, ADR 0031) and
  `docs-api-drift` fails when the generated `docs/docs/api-reference/rest/`
  is stale. A route change updates router, spec and the regenerated reference
  (`npm run gen-api-docs warehouse-ops-agent` in `docs/`) in one PR. MCP tools
  stay hand-documented in `docs/docs/mcp/tools.md`.
- **Tests: no env-var skip gates.** The `-tags=integration` tests boot real
  MCP Streamable HTTP endpoints in-process (never an env var or `t.Skip`);
  reasoner tests use a fake Anthropic server. A future live-Anthropic test
  must sit behind the `integration` build tag (ADR 0004); an integration test
  that needs Postgres or Kafka must use testcontainers.
  Unit tests use fakes — no DB, no live MCP. Coverage gate is 90%.

## Commands

```bash
make check-fast   # agent Stop-hook gate: fmt-check vet arch-test + tests of changed pkgs. Run before saying "done"
make check        # fmt-check vet build lint test (fast pre-commit bundle)
make check-all    # check + coverage (90% gate) + arch-test + bdd (pre-push gate)
make mutation-fast  # gremlins over ./internal/domain — CI's blocking job, NOT in check-all
make vuln           # govulncheck — also NOT in check-all
make api-lint       # Spectral over apis/openapi.yaml (CI job api-lint)
make guide-lint     # agent-guide sensor (this file, rules, skills)
lefthook install    # once, to activate git hooks
```

## Where detail lives

- `.claude/rules/architecture-guardrails.md` — the non-negotiable boundary
  rules in full (always loaded).
- Path-scoped rules, loaded when you touch matching files: `architecture.md`
  (package layout, the mcpclient vs restclient split, ADR 0004 reasoner, ADR
  0011 resilience), `configuration-and-running.md` (env vars, running locally,
  e2e harness), `testing.md`, `docs-site.md`, `events-cloudevents.md`.
- `.claude/skills/` — how-to guides (REST endpoint, MCP tool call, ADR, tests)
  and review commands. ADRs: `docs/docs/adr/`.

Fleet-wide rules: `.claude/rules/fleet/*.md` (canonical in IQVO/warehouse-docs `agents/fleet/`; never hand-edit).

Harness: `HARNESS.md` (what each CI job and local gate runs, and why) and `INCIDENTS.md` (the fleet incident log: every escaped failure and the guard that now catches it).

<!-- harness:scoped-rules:start (generated by tools/migrate_v3.py in warehouse-harness-template; do not hand-edit) -->
## Scoped rules and harness

Claude Code loads each rule below automatically when you touch the matching paths. OpenCode and Codex do NOT: read the rule BEFORE editing matching files.

| When touching | Read |
|---|---|
| `internal/**`, `cmd/**` | `.claude/rules/architecture.md` |
| `internal/config/**`, `cmd/**`, `charts/**` | `.claude/rules/configuration-and-running.md` |
| `docs/**`, `.github/workflows/docs.yml` | `.claude/rules/docs-site.md` |
| `go.mod`, `internal/adapters/**/kafka/**`, `internal/adapters/outbound/events/**` | `.claude/rules/events-cloudevents.md` |
| `**/*_test.go`, `Makefile`, `.gremlins.yaml` ... | `.claude/rules/testing.md` |

Hooks (`scripts/harness/hook.py`, wired for Claude Code, Codex and OpenCode) block pushes to develop/main, `--no-verify`, bare `rm -rf`, and edits to generated files, and feed gofmt/vet findings back after each edit. Before saying "done" run `make check-fast`; the full gate is `make check-all`. `HARNESS_OFF=1` disables the hooks when debugging the harness itself.
<!-- harness:scoped-rules:end -->
