---
name: how-to-test
description: Write or review tests and diagnose a failing coverage, mutation, bdd or integration CI job: the four test layers, the 90% gate, gremlins threshold semantics, the testcontainers rule. Use when adding tests, killing a surviving mutant, or fixing a red check.
---

# How to test

Use when writing or reviewing tests in this repo, or diagnosing a failing
`coverage`/`mutation-fast`/`arch-test` CI job. **Adapted from
inventory-storage's `.claude/skills/how-to-test/SKILL.md` (PR #75)** — the
four-layer mutation-testing discipline below is fleet-wide and applies
here largely unchanged, with two repo-specific differences called out:
this repo's mutation scope is deliberately narrower than a
bounded-context repo's, and this repo carries a distinctive fifth
"layer" the bounded-context repos don't have — the zero-write fitness
test.

## The layers, in order of what they actually prove

1. **Unit tests** (`go test ./... -race`) — prove the code runs without
   panicking and returns SOMETHING. Table-driven, fake outbound-port
   implementations only (see `internal/application/usecases/fakes_test.go`'s
   `fakeFacility`, `fakeWes`, etc.) — never a real MCP/HTTP call in a unit
   test.
2. **Coverage** (`make coverage`, 90% gate) — proves lines executed. This
   repo's `COVERPKG` is
   `./internal/domain/...,./internal/application/...,./internal/adapters/inbound/...`
   (see `Makefile`) — note **`inbound` is included here**, unlike a
   typical bounded-context repo's narrower domain/application-only gate.
   That's because this repo's inbound adapters (the HTTP/MCP handlers)
   carry real degradation logic (nil-use-case 503s, missing-param 400s),
   not just marshalling, so they're held to the same bar.
3. **Mutation testing** (`make mutation-fast`, gremlins,
   `./internal/domain` ONLY) — proves the tests actually ASSERT. This
   repo's domain is `internal/domain/policy` alone (~2100 lines across 6
   files as of `.gremlins.yaml`'s own comment) — small enough that CI
   runs one `gremlins unleash ./internal/domain --workers 1
   --timeout-coefficient 30` pass over the whole thing every push,
   rather than the mutation-fast/mutation-full split some of the bigger
   bounded-context repos use. Check `.gremlins.yaml`'s header comment for
   the CURRENT measured baseline (efficacy/mutant-coverage) and when it
   was last re-baselined before assuming today's numbers.
4. **Architecture fitness tests** (`make arch-test`,
   `internal/architecture/...`) — this repo's nearest analogue to a
   bounded-context repo's `bdd`/godog layer, but structurally different:
   there is no `features/` directory or end-to-end HTTP behaviour suite
   here (see `how-to-add-a-rest-endpoint.md`'s step 5 for why). Instead,
   `arch-test` proves two static invariants that matter MORE here than
   behaviour tests would: no cross-context Go import
   (`TestNoDirectDependencyOnBoundedContexts`) and zero write capability
   (below).

## This repo's distinctive addition: the zero-write fitness test

`internal/architecture/zerowrite/zerowrite_test.go` is this repo's own
addition to the fleet's testing story — no sibling bounded-context repo
has an equivalent, because none of them has this repo's "zero write
capability, v1" constraint (ADR 0001 / governance note; ADR 0004 keeps the
LLM reasoner inside it). Two tests:

- `TestNoMutatingHTTPMethodInOutboundClients` — an AST walk (not a text
  grep) over every non-test `.go` file in
  `internal/adapters/outbound/{restclient,mcpclient}`, failing if a
  mutating HTTP method literal/constant (`http.MethodPost`, `"PUT"`,
  etc.) appears anywhere. Using the real AST rather than grep means a
  mutating method name mentioned only in a comment (explaining why it
  must NOT be used) doesn't produce a false failure — worth knowing
  before you "fix" what looks like a false positive.
- `TestNoMutatingToolAnnotationInMCPServer` — parses
  `internal/adapters/inbound/mcp/tools.go` and asserts every registered
  `mcp.Tool` has a matching `ToolAnnotations{ReadOnlyHint: readOnly}`
  reference (this repo's convention: one shared `readOnly := true`
  local variable reused per tool, never a bare literal per call site),
  and that `readOnly` is never reassigned `false` anywhere in the file.

When adding a new outbound port method or a new MCP tool (see
`how-to-add-an-mcp-tool-call.md`), run `make arch-test` locally before
opening the PR — this is the sensor that catches an accidental write
capability creeping in, and it fails fast/cheap (source-level, no running
process) the same way `TestNoDirectDependencyOnBoundedContexts` does.

## Mutation testing: `<=` fails, not `>=`

`.gremlins.yaml`'s `threshold.efficacy`/`threshold.mutant-coverage` are
floors gremlins fails on if the MEASURED value is `<=` the threshold —
read the comment at the top of this repo's `.gremlins.yaml` for the exact
current numbers (80/96 as of the 2026-09-14 baseline) and when they were
last re-baselined. If you deliberately lower a package's mutation score
(rare), lower the threshold in the SAME PR with a dated comment
explaining why.

## Two real pitfalls that generalize from the fleet's shared mutation-testing lessons

### 1. Boundary guards need the boundary value itself

A test for a threshold comparison (e.g.
`policy.TravelFactorDistanceThresholdMetres`'s significant/negligible
split in `travel_factor.go`) that only tries a value clearly above and a
value clearly below the threshold never exercises the threshold itself —
a `CONDITIONALS_BOUNDARY` mutant rewriting `>` to `>=` (or similar)
survives silently. Every threshold comparison in
`internal/domain/policy` needs an explicit test for the exact boundary
value.

### 2. Zero/origin-value fixtures hide arithmetic mutants

Same lesson as the fleet-wide guide: a test built around zero-valued
operands makes `a - b` and `a + b` produce the same result, hiding an
arithmetic-operator mutant. `.gremlins.yaml`'s own comment notes this
repo's current 18 lived + 3 not-covered mutants concentrate in
`utilization_correlation.go`'s threshold math (ADR 0008) — a live,
already-diagnosed instance of exactly this class, tracked as follow-up
hardening rather than fixed opportunistically; don't re-diagnose from
scratch if you land here again.

## Diagnosing a `mutation-fast` CI failure: diff against develop

```bash
gremlins unleash ./internal/domain --workers 1 --timeout-coefficient 30   # on your branch
git stash && git checkout origin/develop -- . && gremlins unleash ./internal/domain --workers 1 --timeout-coefficient 30   # baseline
```

Only entries NEW on your branch are your regression. This repo has no
`MUTATION.md` triage file today (unlike some sibling repos) — if you
accept a near-equivalent survivor (a tie-break mutant on an unreachable
branch, etc.), document it in `.gremlins.yaml`'s own header comment
alongside the existing baseline note, following that file's established
style, rather than inventing a new file.

## No Kafka/Postgres integration tests in this repo

This repo has no database and no Kafka consumer/publisher of its own, so
there is no `-tags=integration`/testcontainers category to maintain here
the way the bounded-context repos have — skip that whole section of the
fleet-wide guide; it doesn't apply. The nearest equivalent risk (an
unreachable upstream MCP server) is covered by unit tests against fake
port implementations (layer 1 above), not a real broker/DB in CI.

## Verify before opening the PR

```bash
make check-all   # check + coverage + arch-test
make mutation-fast  # not in check-all's default composition — run explicitly
make vuln           # govulncheck ./... — also not in check-all
```

Per this repo's own `Makefile`, `check-all` is `check coverage arch-test`
— `mutation-fast` and `vuln` are separate targets, not part of it. CI
runs all of them regardless (per `.gremlins.yaml`'s comment: "CI's
mutation-fast job runs exactly this command, every push/PR, blocking"),
so a PR can pass your local `make check-all` and still go red in CI on
mutation or vuln — run those two explicitly too before opening the PR,
not just check-all.
