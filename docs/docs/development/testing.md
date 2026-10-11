---
id: testing
title: Testing
sidebar_label: Testing
description: The warehouse-ops-agent test pyramid as it exists in the repo — unit tests, godog/Gherkin acceptance features, MCP tool-selection evals, architecture fitness and zero-write tests, mutation testing — with the exact make targets, local hooks and CI jobs.
---

# Testing

Everything in this repo runs with plain `go test` and no external
service: there is no database, no Kafka and no live MCP server to start.
Upstreams are replaced by fakes (Go interfaces), by `httptest` servers or
by in-process Streamable HTTP MCP stubs. No test calls the real Anthropic
API.

## The pyramid

| Layer | Where | What it proves | Command |
|---|---|---|---|
| Domain unit tests | `internal/domain/policy/*_test.go` | the pure decision rules: flow balance, daily-brief exception correlation, stranded reservation, travel factor, utilization overlay, capacity outlook, master-data gaps, inbound outlook, transfer triage and imbalance, runtime-signal thresholds, LLM plan validation and arbitration | `make test` |
| Use-case unit tests | `internal/application/usecases/*_test.go` | fan-out and degradation with fake ports (`fakes_test.go`), including `end_to_end_resilience_test.go` and `flow_balance_llm_test.go` (shadow/on/off arbitration with a fake reasoner) | `make test` |
| Adapter tests | `internal/adapters/inbound/{http,mcp}/*_test.go`, `internal/adapters/outbound/{mcpclient,restclient,telemetry,logs,llm/anthropic}/*_test.go` | REST status mapping, CORS, MCP tool registration and errors; the outbound clients' wire format against `httptest` and in-process MCP servers; the Anthropic adapter's retry, timeout, breaker and tool-argument validation against a fake API | `make test` |
| Config and composition root | `internal/config/config_test.go`, `cmd/agent/main_test.go` | env parsing and its fail-fast cases; the wiring of optional upstreams | `make test` |
| Acceptance (BDD) | `features/*.feature`, driven by `features_test.go` and the `steps_*_test.go` / `fakes_*_test.go` files at the module root | black-box scenarios against the **real** chi router and the real use cases, with fake outbound adapters ([ADR 0022](../adr/0022-godog-bdd-acceptance-tests.md)) | `make bdd` |
| MCP tool-selection evals | `internal/adapters/outbound/mcpclient/agent_selection_eval_test.go` (`TestAgentEval_*`) | that the stranded-reservation use case calls exactly the right sibling tools, with the right arguments, in the right order, through the real `mcpclient` adapters against recording Streamable HTTP stubs; no LLM involved | `go test ./internal/adapters/outbound/mcpclient/... -race -run '^TestAgentEval' -v` |
| Architecture fitness | `internal/architecture/*_test.go` | the hexagonal dependency rules (arch-go), the MCP adapter rule, no auth middleware, no struct tags in the domain, CloudEvents-only and replay-consumer sensors | `make arch-test` |
| Zero-write guardrail | `internal/architecture/zerowrite/*_test.go` | no outbound client uses a mutating HTTP method, every MCP client calls only read tools (product-master and inbound-receiving pinned to their tool lists), and every tool this server registers carries `ReadOnlyHint` | `make arch-test` |
| Mutation | `gremlins` over `./internal/domain` | the domain tests actually kill mutants | `make mutation-fast` |

At the time of writing the repo holds 280 `func Test...` functions across
its `_test.go` files.

### Acceptance features

12 feature files with 102 `Scenario`/`Scenario Outline` blocks (11 of them
outlines). With the outline examples expanded, the local
`make check-all` run on this branch reported
`142 scenarios (142 passed)` and `1079 steps (1079 passed)`.

| Feature file | Scenario blocks |
|---|---|
| `console_reports.feature` | 11 |
| `flow_balance.feature` | 11 |
| `llm_arbitration.feature` | 10 |
| `transfer_watch.feature` | 10 |
| `daily_brief.feature` | 8 |
| `explain_travel_factor.feature` | 8 |
| `inbound_outlook.feature` | 8 |
| `master_data_gaps.feature` | 8 |
| `reasoner_resilience.feature` | 8 |
| `order_lifecycle.feature` | 7 |
| `http_conventions.feature` | 7 |
| `runtime_signals.feature` | 6 |

`TestFeatures` runs godog with `Strict: true`, so an undefined or pending
step fails the suite. It pins `CORS_ALLOWED_ORIGINS` to empty (the console
default) and discards the use cases' warning logs. To run one feature:

```bash
go test . -run 'TestFeatures/The_WMS_dashboard' -v
```

### What is absent, and why

| Fleet test type | Status here |
|---|---|
| Integration tests with testcontainers | none: nothing to containerise (no Postgres, no Kafka). `TestPostgresIntegrationTestsUseTestcontainers` is a fleet sensor that passes because no `-tags=integration` test exists. |
| Contract tests (schemathesis) | none: the repo has no `apis/openapi.yaml`. Every route is described by hand on [HTTP routes](../api/http-routes.md). |
| AsyncAPI catalogue | `TestEventCatalogueMatchesContract` skips with `no apis/asyncapi.yaml: nothing to compare`; the agent publishes no events. |
| Live-LLM evals | none: every reasoner test uses a fake Anthropic server (`fake_anthropic_test.go`, `internal/adapters/outbound/llm/anthropic/*_test.go`). |

## Coverage

| Where | Command | `-coverpkg` | Gate |
|---|---|---|---|
| `make coverage` (local, also part of `make check-all`) | `go test ./... -race -coverprofile=coverage.out -coverpkg=$(COVERPKG)` | `./internal/domain/...,./internal/application/...,./internal/adapters/inbound/...` | 90 % |
| CI `test` job | same, but | `./internal/domain/...,./internal/application/...` | 90 % (`THRESHOLD: "90"`) |

The two scopes differ: the local gate also counts the inbound adapters,
CI does not. The local run on this branch printed
`Coverage: 97.9% (gate: 90%)`.

## Mutation testing

`.gremlins.yaml` runs the whole domain with `workers: 1` and
`timeout-coefficient: 30`, and fails below:

| Threshold | Value | Meaning |
|---|---|---|
| `efficacy` | 80 | killed / (killed + lived) |
| `mutant-coverage` | 96 | (killed + lived) / total |

The file records the baseline measured on 2026-09-14 with gremlins
v0.6.0: 97 mutants, 76 killed, 18 lived, 3 not covered, efficacy 80.85 %,
mutant coverage 96.91 %. The survivors sit in
`policy/utilization_correlation.go`. gremlins fails when the measured
value is **at or below** the threshold, so the thresholds sit just under
the baseline. Install the pinned version with
`go install github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0`.

## Make targets

| Target | Runs |
|---|---|
| `make build` | `go build ./...` |
| `make vet` | `go vet ./...` |
| `make fmt` / `make fmt-check` | `gofmt -w .` / fail if any file is not gofmt-clean |
| `make lint` | `golangci-lint run ./...` (CI pins v2.13.1) |
| `make test` | `go test ./... -race` |
| `make coverage` | coverage run plus the 90 % gate |
| `make bdd` | `go test ./... -run TestFeatures -v` |
| `make arch-test` | `go test ./internal/architecture/... -v` |
| `make mutation-fast` | `gremlins unleash ./internal/domain --workers 1 --timeout-coefficient 30` |
| `make vuln` | `govulncheck ./...` |
| `make check` | `fmt-check vet build lint test` |
| `make check-all` | `check coverage arch-test bdd` |
| `make check-fast` | `fmt-check vet arch-test` plus the tests of changed packages (agent Stop hook) |
| `make guide-lint` / `make harness-test` | lint the agent guides / test the agent hooks |

## Local hooks (lefthook)

`lefthook.yml`, activated once with `lefthook install`:

| Hook | Runs |
|---|---|
| `pre-commit` | `make fmt-check`, `make vet`, `make lint` |
| `pre-push` | `make check-all` |

## CI jobs

`.github/workflows/ci.yml` runs on push and pull request to `main` and
`develop`, weekly on Monday 06:00 UTC, and on manual dispatch.

| Job | Runs | When |
|---|---|---|
| `lint` | golangci-lint v2.13.1 | every push and PR |
| `guide-lint` | `scripts/harness/guide_lint.py`, `repo_lint.py`, `test_hook.py`, `test_repo_lint.py` | every push and PR |
| `complexity` | golangci-lint with only `gocyclo,gocognit,cyclop,funlen,nestif`, plus an informational gocyclo report (over 10; the gate is 15) | every push and PR |
| `test` | `go build`, `go vet`, `go test ./... -race` with coverage, 90 % gate, uploads `coverage.out` | every push and PR |
| `bdd` | `go test ./... -run TestFeatures -v` | every push and PR |
| `evals-tests` | `go test ./internal/adapters/outbound/mcpclient/... -race -run '^TestAgentEval' -v` | every push and PR |
| `arch-test` | `go test ./internal/architecture/... -v` | every push and PR |
| `mutation-fast` | gremlins v0.6.0 over `./internal/domain` | every push and PR (blocking) |
| `vuln` | govulncheck v1.1.4 | every push and PR |
| `drift` | deadcode, `go mod tidy -diff`, coverage-quality (`scripts/coverage-quality.py` over coverage plus gremlins output); opens or closes a `harness:red` issue on scheduled runs | schedule and manual dispatch only |
| `helm-lint` | chart-testing `ct lint` on `charts/warehouse-ops-agent` | PRs into `main` only |
| `trivy-scan` | builds the image and scans it; blocks on fixable CRITICAL/HIGH | PRs into `main` only |
| `docker-publish` | multi-arch image to GHCR, cosign signature, SPDX SBOM attestation | push to `main` only |
| `release` | next `vX.Y.Z` tag, image re-tag, Helm chart push to `oci://ghcr.io/iqvo`, GitHub release | push to `main` only, after `docker-publish` |

On a PR into `develop`, `drift`, `helm-lint`, `trivy-scan`,
`docker-publish` and `release` show as skipped by design.

Other workflows: `codeql.yml` (push and PR to `main`/`develop`, weekly),
`scorecard.yml` (push to `develop`, weekly), `ai-review.yml` (advisory
review on PRs into `develop`) and `docs.yml`, which builds and deploys
this site only on push to `main` or manual dispatch. A PR therefore never
builds the docs; run `npm run build` in `docs/` yourself.

## Writing a new test

- A new decision rule goes in `internal/domain/policy` with a table test;
  it raises the mutation baseline, so run `make mutation-fast`.
- A new route or tool gets a Gherkin scenario in `features/` and steps in
  the matching `steps_*_test.go`; wire any new fake in `fakes_*_test.go`.
- A new outbound MCP tool must be a read tool, or
  `TestMCPClientsCallOnlyReadTools` fails.
- A new inbound MCP tool must set `ReadOnlyHint: true`, or
  `TestNoMutatingToolAnnotationInMCPServer` fails.
