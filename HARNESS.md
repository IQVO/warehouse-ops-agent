# HARNESS.md — warehouse-ops-agent

What harness `warehouse-ops-agent` carries, what each piece costs and where in the change lifecycle it runs, so an agent
can answer "what harness do I have, and why" without archaeology. Structure and generic sections follow the
fleet reference copy (`product-master`, from `warehouse-harness-template`); every sensor row below names a job in
this repo's `.github/workflows/ci.yml` or a target in its `Makefile`.

Reference: Birgitta Böckeler, "Harness engineering for coding agent users" (martinfowler.com, 02 Apr 2026).
Model: **guides** (feedforward) + **sensors** (feedback), each **computational** or **inferential**, across
**maintainability**, **architecture fitness** and **behaviour**.

---

## Guides (feedforward)

| File | Purpose |
|---|---|
| `CLAUDE.md` (`AGENTS.md` is a symlink to it) | Entry point every agent session reads first: identity, hard rules, commands, where things are written down. |
| `.claude/rules/architecture-guardrails.md` | Path-scoped rule (loaded by Claude Code when matching files are touched; read by hand in OpenCode/Codex). |
| `.claude/rules/architecture.md` | Path-scoped rule (loaded by Claude Code when matching files are touched; read by hand in OpenCode/Codex). |
| `.claude/rules/configuration-and-running.md` | Path-scoped rule (loaded by Claude Code when matching files are touched; read by hand in OpenCode/Codex). |
| `.claude/rules/docs-site.md` | Path-scoped rule (loaded by Claude Code when matching files are touched; read by hand in OpenCode/Codex). |
| `.claude/rules/events-cloudevents.md` | Path-scoped rule (loaded by Claude Code when matching files are touched; read by hand in OpenCode/Codex). |
| `.claude/rules/testing.md` | Path-scoped rule (loaded by Claude Code when matching files are touched; read by hand in OpenCode/Codex). |
| `.claude/rules/fleet/*.md` | Fleet-wide rules copied from the harness template; never edit them here. |
| `.claude/skills/<name>/SKILL.md` | Loadable skills: `architecture-review`, `code-review`, `domain-review`, `how-to-add-a-frontend-remote`, `how-to-add-a-rest-endpoint`, `how-to-add-an-mcp-tool-call`, `how-to-test`, `how-to-write-an-adr`. `.agents/skills` symlinks them for Codex. |
| `docs/docs/adr/` | Architecture decisions with rationale: the source of truth for "why". |
| `.claude/memory/MEMORY.md` | Durable repo-specific heuristics that are not incidents (repo-owned, never overwritten by the template sync). |
| `INCIDENTS.md` | Canonical fleet harness incident log (identical in every repo): each failure that escaped a sensor and the guard added for it. |

## Sensors (feedback) — CI jobs

From `.github/workflows/ci.yml`. "Required check" = listed in `develop` branch protection (arch-test, bdd, file-length, integration, lint, mutation-fast, test, vuln);
strict mode, so a PR that falls behind `develop` must be updated and re-run. `docker-publish` and `release` are
delivery jobs (push to `main` only), not sensors.

| Job | What it runs | Lifecycle | Blocking? |
|---|---|---|---|
| `lint` | golangci-lint with `.golangci.yml` | PR + push (develop, main) | yes (required check) |
| `guide-lint` | agent-guide and config sensors: `scripts/harness/guide_lint.py`, `scripts/harness/repo_lint.py`, `scripts/harness/test_hook.py`, `scripts/harness/test_red_issue.py`, `scripts/harness/test_repo_lint.py` | PR + push (develop, main) | yes by fleet policy (never merge red); not a required check |
| `complexity` | gocyclo: blocking gate at 15, informational report of functions over 10 | PR + push (develop, main) | gate yes by fleet policy (not a required check); report informational |
| `file-length` | revive file-length-limit on the Go files a PR adds or touches; backlog report otherwise | PR into develop (blocking on touched files); push: backlog report | yes (required check) |
| `test` | build + vet, then `make coverage` (`go test -race`, 90% gate on `./internal/domain/...,./internal/application/...,./internal/adapters/inbound/...`) | PR + push (develop, main) | yes (required check) |
| `bdd` | godog/Gherkin acceptance tests (`go test ./... -run TestFeatures`, scenarios in `features/`) | PR + push (develop, main) | yes (required check) |
| `evals-tests` | agent evals: Agent MCP tool-selection evals (recording wire stubs) | PR + push (develop, main) | yes by fleet policy (never merge red); not a required check |
| `integration` | `go test -tags=integration ./...` (real MCP Streamable HTTP endpoints) | PR + push (develop, main) | yes (required check) |
| `arch-test` | architecture fitness tests (`go test ./internal/architecture/...`, listed below) | PR + push (develop, main) | yes (required check) |
| `drift` | continuous drift: deadcode, `go mod tidy -diff`, coverage-quality (high coverage + surviving mutant) | weekly schedule (Mon 06:00 UTC) + manual dispatch | no (advisory; opens a `harness:red` issue on failure) |
| `mutation-fast` | gremlins (`gremlins unleash ./internal/domain`), thresholds in `.gremlins.yaml` | PR + push (develop, main) | yes (required check) |
| `mutation` | gremlins over the full domain layer (`gremlins unleash ./internal/domain`) | weekly schedule (Mon 06:00 UTC) + manual dispatch | no (advisory; opens a `harness:red` issue on failure) |
| `api-lint` | Spectral (`.spectral.yaml`, `--fail-severity=warn`) over `apis/openapi.yaml` | PR + push (develop, main) | yes by fleet policy (never merge red); not a required check |
| `docs-api-drift` | regenerates the REST reference in `docs/` (`npm run clean-api-docs warehouse-ops-agent && npm run gen-api-docs warehouse-ops-agent`) and fails on any diff against what is committed | PR + push (develop, main) | yes by fleet policy (never merge red); not a required check |
| `vuln` | govulncheck over the module and its Go stdlib version | PR + push (develop, main) | yes (required check) |
| `helm-lint` | chart-testing `ct lint` over `charts/` | PR into main | yes by fleet policy (never merge red); not a required check |
| `chart-tests` | chart render/selector/Kong assertions: `charts/warehouse-ops-agent/tests/render_test.sh` | PR + push (develop, main) | yes by fleet policy (never merge red); not a required check |
| `trivy-scan` | Trivy image scan; blocks on CRITICAL/HIGH with a fix available, SARIF to the Security tab | PR into main | yes by fleet policy (never merge red); not a required check |

Other workflows:

| Workflow | What it runs | Lifecycle | Blocking? |
|---|---|---|---|
| `CodeQL` (`.github/workflows/codeql.yml`) | static security analysis | PR, push, schedule | review threads block merge (conversation resolution) |
| `Scorecard` (`.github/workflows/scorecard.yml`) | OpenSSF supply-chain posture | schedule | no |
| `Docs` (`.github/workflows/docs.yml`) | builds the Docusaurus site in `docs/` (broken links/ids/MDX) and publishes Pages | PR touching `docs/**` builds; push publishes | build must pass |
| `ai-review` (`.github/workflows/ai-review.yml`) | advisory LLM review (no-ops without `ANTHROPIC_API_KEY`) | PR into develop | no (inferential) |

Inferential sensors: the `code-review`, `architecture-review` and `domain-review` skills (and `.claude/agents/`),
invoked by hand before merge (local-only by user decision 2026-09-13); LLM cost per invocation, never blocking.

## Sensors — local (before CI)

- `lefthook` pre-commit: `make fmt-check`, `make lint`, `make vet`.
- `lefthook` pre-push: `make check-all`. Never bypass with `--no-verify`.
- Agent Stop hook: `make check-fast` (fmt-check, vet, arch-test, tests of changed packages) before "done".

| Target | Prerequisites |
|---|---|
| `make check-fast` | `fmt-check vet arch-test` + changed-package tests |
| `make check` | `fmt-check vet build lint test` |
| `make check-all` | `check coverage arch-test bdd` |

Other gates you can run locally: `make integration`, `make mutation-fast`, `make api-lint`, `make vuln`, `make guide-lint`, `make harness-test`.

## Architecture fitness functions (`arch-test`)

Every rule in `internal/architecture/` encodes an incident already lived somewhere in the fleet (see
`INCIDENTS.md`), not a hypothetical. A `...SensorBites` / `...FailsOnBadFixtures` test proves its sibling sensor
can actually fail.

- `internal/architecture/architecture_test.go`
  - **TestNoDirectDependencyOnBoundedContexts**: asserts that no package in this module imports any Go package from the five bounded contexts it reads from via MCP.
  - **TestHexagonalDependencyRules**: enforces this module's inward-pointing dependency rule: domain (policy) is pure, application depends only on domain and ports, adapters never depend on each other across the inbound/outbound boundary, and only cmd...
- `internal/architecture/catalogue_fitness_test.go`
  - **TestEventCatalogueMatchesContract**
  - **TestEventCatalogueDetector**: The detector itself, so a refactor cannot quietly turn it into a test that always passes.
- `internal/architecture/events_fitness_test.go`
  - **TestCloudEventsOnly**
  - **TestReplayConsumersSetCommitInterval**
- `internal/architecture/fitness_test.go`
  - **TestMCPAdapterDependencyRule**: encodes this agent's own governance charter (docs/mcp/governance-charter.md, ADR-0008 fleet-wide): the MCP inbound adapter is additive, never load-bearing for the composition root.
  - **TestNoAuthMiddlewareReintroduced**: encodes ADR-0006 (superseding ADR-0005): this agent's inbound REST/MCP surfaces are deliberately unauthenticated, and its outbound MCP-client calls to the five upstreams carry no bearer key, as part of the fleet-wide...
  - **TestPostgresIntegrationTestsUseTestcontainers**: TestPostgresIntegrationTestsUseTestcontainers: a `-tags=integration` test MUST boot its own database via testcontainers-go/modules/postgres, never gate on DATABASE_URL / ANALYTICS_DATABASE_URL + t.Skip (a skip-gated...
  - **TestPostgresIntegrationSensorFailsOnBadFixtures**: proves the sensor above can actually fail: each fixture breaks the rule one way and must be reported, while a compliant fixture and a comment-only mention must not.
  - **TestNoStructTagsInDomain**: TestNoStructTagsInDomain: no json:"…" / db:"…" tags in non-test files under internal/domain/**.
  - **TestNoStructTagsInDomainSensorFailsOnBadFixtures**: proves the sensor can fail: tagged structs are reported; clean structs, comment mentions and non-serialisation tags are not.
- `internal/architecture/otelhttp_fitness_test.go`
  - **TestOutboundHTTPClientsUseOtelhttp**: scans every non-test Go file under internal/ and cmd/ (except the httpclient factory itself).
  - **TestOutboundHTTPClientSensorFailsOnBadFixtures**: proves the sensor can fail: raw clients are reported; httpclient.New, an http.Server and a comment mention are not.

## Mutation testing: what it catches that coverage doesn't

`go test -cover` measures whether a line EXECUTED; gremlins measures whether a test would FAIL if that line's
logic were subtly wrong (`>=` to `>`, a negated boolean, a swapped operand). 100% line coverage with a surviving
mutant means a test walks the path but asserts nothing about it — the failure mode the `drift` job's
coverage-quality check is built to catch.

In this repo: `mutation-fast` runs `gremlins unleash ./internal/domain --workers 1 --timeout-coefficient 30`; `mutation` runs `gremlins unleash ./internal/domain --workers 1 --timeout-coefficient 30`.
The domain (`internal/domain`, pure policy, no aggregate) is small enough that `mutation-fast` already covers all of it on every PR; the weekly `mutation` job runs the same scope on a schedule against `develop` and owns the `harness:red` loop.

**gremlins fails when the measured value is `<=` the configured threshold** (not `<`) — see `.gremlins.yaml`.
Always measure first, then set the threshold strictly below what you measured; never copy a sibling repo's
numbers. **Pitfall:** a tagless `switch { case ...: }` can under-report mutator coverage versus the equivalent
`if / if / return` chain with identical tests (warehouse-ops-agent PR #58: 89.91% as a switch, 97.2% as an
if-chain). Try the rewrite before assuming a missing test case.

## Credential handling

Never embed a real password in a `scheme://user:PASSWORD@host` URL in committed files, including local-dev
fixtures: some agent tooling silently redacts that shape on write and read-back. Use a password-less URL plus
`PGPASSWORD`, or the driver's own password API (`pgx.ParseConfig` + `.Password`).

## Agent runtime harness: one logic, three runtimes

| Piece | File(s) | Lifecycle position |
|---|---|---|
| Skills that load | `.claude/skills/<name>/SKILL.md`; `.agents/skills` symlink for Codex; OpenCode scans `.claude/skills` | on task match |
| Path-scoped rules | `.claude/rules/*.md` with `paths:`; CLAUDE.md carries the generated pointer table | on edit |
| Guard + feedback hooks | `scripts/harness/hook.py` (`pre` / `post` / `stop`) + adapters `.claude/settings.json`, `.codex/hooks.json`, `.opencode/plugins/harness.ts` | every tool call; end of turn |
| Guide freshness | `scripts/harness/guide_lint.py` / `make guide-lint` (CI job `guide-lint`) | PR |
| Config sensors | `scripts/harness/repo_lint.py` (CI job `guide-lint`) | PR |
| Red-sensor loop | `scripts/harness/red_issue.py` steps in the scheduled jobs -> `harness:red` issue | weekly |
| Fleet-rule drift | `scripts/harness/fleet_drift.py`: `.claude/rules/fleet/*.md` vs the canonical copy in `IQVO/warehouse-docs` | on demand |
| Subagents | `.claude/agents/` (code, architecture and domain review) | on demand |

Rules live ONLY in `hook.py`; the adapters translate events and cannot drift. The hooks block pushes to
develop/main, `--no-verify`, force-push, `rm -rf` on non-scratch paths, destructive git and edits to generated
files, and feed gofmt/vet findings back after each edit. Every message states WHAT / WHY / FIX.
`HARNESS_PROTECT_THRESHOLDS=1` additionally forbids editing gates (`.gremlins.yaml`, `.golangci.yml`,
`internal/architecture/**`, CI). `HARNESS_OFF=1` disables the lot.

The managed files (`scripts/harness/*`, `.claude/settings.json`, `.claude/agents/*`, adapters, `.claude/rules/fleet/`)
are owned by `warehouse-harness-template` and re-applied by its `tools/migrate_v3.py` / `tools/migrate_v4.py`
(idempotent; the engine of the template sync): change them there, not here.

## Config sensors (`repo_lint`) and the incident protocol

`scripts/harness/repo_lint.py` (stdlib only) checks the configuration mistakes that reached CI in practice: **R1**
workflow `working-directory`/`cache-dependency-path` that do not exist, **R2** `needs:` naming missing jobs, **R3**
Dependabot entries that cannot work, **R4** a stale GitHub owner in registry/docs/chart URLs, **R5**
unsubstituted `{{PLACEHOLDER}}`s. `# repo-lint: ignore` suppresses one line (say why).

**Incident protocol.** Every failure not caught before CI ends with: the fix, a sensor or guide that would have
caught it, that sensor rolled out to the whole fleet, and a row in [INCIDENTS.md](INCIDENTS.md). A row with no
guard is an open gap and says so. Sensor output is a prompt: it must state WHAT, WHY and FIX.

## Versioning

`harness-template: v4` (recorded in CLAUDE.md): v4 adds the `.claude/agents/*` review subagents,
`.claude/memory/MEMORY.md`, `red_issue.py --draft` and a normalized version marker, on top of v3 = agent runtime
harness (loadable skills, hooks for Claude Code / Codex / OpenCode, guide-lint, red-issue loop) and v2 =
CloudEvents 1.0 mandatory. The fleet audit (`scripts/harness-audit.py` in
`warehouse-harness-template`) reads that marker to tell which repos are behind.
