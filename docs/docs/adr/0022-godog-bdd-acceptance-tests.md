---
id: 0022-godog-bdd-acceptance-tests
title: "0022 — godog/Gherkin acceptance tests as executable specification"
sidebar_label: "0022 · godog BDD tests"
description: "Express the agent's behaviour as black-box Gherkin scenarios driven through the real chi router over fake outbound adapters, gated in CI as a bdd job."
---

# ADR 0022: godog/Gherkin acceptance tests as executable specification

## Status

Accepted (2026-10-08). Applies to this repo the pattern
`inventory-storage` introduced in its ADR 0007 and the sibling contexts
already follow; nothing here supersedes an earlier record.

## Context

By this point the agent had a deep conventional suite: table-driven tests of
every pure policy rule (`internal/domain/policy`), use-case tests against
hand-written fakes (`internal/application/usecases/fakes_test.go`), `httptest`
tests per inbound route, golden-file tests of the wire shapes, contract tests
of every MCP client against its upstream's published tool registry, a
`gremlins` mutation pass over the domain and arch-go fitness tests. Coverage was
not the gap.

What was missing was a **statement of behaviour in the agent's own
vocabulary**. This service's value is in rules that are easy to state and easy
to regress: a path is only an exception when *two* independent signals agree; an
unrecognised enum from an upstream is rejected, never defaulted; the model can
replace the deterministic decision only with a plan the policy layer validates;
a dead reader costs the operator one dashboard panel, not the screen; "unknown"
is never rendered as "none". Each of those lives today as a Go test name
(`TestArbitrate_OnMode_InvalidPlan_FallsBack`) that someone who does not read Go
cannot review.

There was also a structural gap. The existing tests each exercise one layer, and
the layers where bugs hide in an agent like this are the joins: router → DTO
mapping → use case → policy → fake upstream. Nothing drove the **whole stack as
a black box**, the way the warehouse console or an agentic host does.

Two properties of this repo shape the design:

1. **Every outbound dependency is already behind a port.** The ten MCP clients,
   the seven report readers, the order-lifecycle REST clients, the telemetry and
   log readers and the `ports.Reasoner` are all interfaces the composition root
   (`cmd/agent`) fills in. A scenario can therefore wire the *same use cases and
   the same `inboundhttp.NewRouter`* production does and replace only the
   adapters.
2. **The one genuinely hostile dependency is the model.** ADR
   [0004](./0004-llm-reasoner-behind-the-policy-layer.md) and
   [0011](./0011-reasoner-path-circuit-breaker-timeout-retry.md) specify how the
   agent behaves when it is slow, failing or wrong. That behaviour is only
   observable by running the real `anthropic.Reasoner` (its retry, timeout and
   circuit breaker are inside the adapter), so the suite needs a stand-in for the
   Anthropic Messages API, not just a fake `ports.Reasoner`.

## Decision

**We will write executable specifications in Gherkin under `features/`, run them
with [godog](https://github.com/cucumber/godog) v0.16.0 (the official Cucumber
implementation for Go), and gate them in CI as a `bdd` job.**

1. **One feature file per capability, in the agent's vocabulary.** Each file
   opens with a `# Derived from:` comment citing the prose spec
   (`docs/docs/api-surface.md`, there being no OpenAPI document), the ADRs and
   the policy source it specifies, and every feature is tagged `@bdd`.

   | File | Covers |
   | --- | --- |
   | `features/http_conventions.feature` | `/healthz`, no credentials on any route (ADR 0006), 404/405, GET-only CORS (ADR 0016) |
   | `features/daily_brief.feature` | `GET /daily-brief`: two-signal correlation, severity ranking, per-fact degradation |
   | `features/flow_balance.feature` | `GET /flow-balance/{pathId}`: the closed lever vocabulary, partial/hold degradation, enum rejection, utilization overlay (ADR 0008) |
   | `features/llm_arbitration.feature` | `LLM_MODE` off/shadow/on and `policy.Arbitrate`'s validation bounds (ADR 0004), with a fake `ports.Reasoner` |
   | `features/reasoner_resilience.feature` | retry, 4xx-is-permanent, timeout, breaker open and recovery (ADR 0011), through the real adapter |
   | `features/explain_travel_factor.feature` | the 60 m threshold, 400 vs 502 vs 503 (ADR 0009, 0016, 0018) |
   | `features/order_lifecycle.feature` | console-bff stitch across four contexts, per-stage degradation, 404 (ADR 0002, 0016) |
   | `features/console_reports.feature` | WMS/WES dashboards: aggregation, wired vs unwired vs failing sections, freshness, windows (ADR 0003) |
   | `features/runtime_signals.feature` | severity thresholds, unavailable sources |
   | `features/master_data_gaps.feature` | gap kinds, bounded scan with resume cursor, 400/502/503 (ADR 0020) |
   | `features/transfer_watch.feature` | stuck-transfer triage, status, imbalance, 400/404/502/503 (ADR 0019) |
   | `features/inbound_outlook.feature` | overdue ASNs, horizon, stale receipts, `omitted` vs empty, partial failure (ADR 0021) |

2. **True black-box acceptance tests.** The step definitions live in
   `features_test.go` and its `steps_*_test.go` / `fakes_*_test.go` siblings at
   the repo root (`package main_test`). They build the composition root the way
   `cmd/agent` does — the same `usecases.*` values, the same
   `inboundhttp.NewRouter` — over **fake outbound adapters**, serve it with
   `httptest.NewServer` and drive it with plain `net/http` requests. No use case
   is called directly: a wrong status mapping, DTO field name or JSON null/absent
   distinction fails a scenario. Generic `the JSON field "a.b.0.c" equals "x"`
   steps assert on the wire body, so the scenarios read the contract the
   console actually sees.

3. **Nothing real is contacted.** Every MCP client, REST client, report reader,
   telemetry/log reader and the reasoner is an in-memory fake configured by Given
   steps. The only network used is loopback: the Anthropic stand-in is an
   `httptest` server answering the real `anthropic.Reasoner` (the pattern
   `end_to_end_resilience_test.go` already uses), so retry, timeout and the
   breaker are exercised for real while no model, API key or external host is
   involved.

4. **Fresh state per scenario.** A godog `Before` hook rebuilds every fake and
   the world; the server is started lazily on the first request so Given steps
   can still decide what is wired. Optional capabilities (`flow-balance`,
   `console-reports`, `master-data-gaps`, …) are left unwired with
   `Given the "<capability>" capability is not configured` to specify the 503
   convention, and individual report readers with `… reports reader is not
   configured` to specify the per-section degradation.

5. **Scenarios specify the correct behaviour, not the current one.** They are
   written from the spec and the ADRs. Where the implementation was found to
   contradict them, or to look wrong, the scenario is not bent to fit; the
   discrepancy is recorded in the pull request instead.

6. **No auth, by construction.** No scenario sends credentials, and one asserts
   that every route answers without asking for any (ADR
   [0006](./0006-fleet-wide-auth-removal.md)).

7. **Blocking in CI** as the `bdd` job (`go test ./... -run TestFeatures -v`),
   mirrored locally by `make bdd`, included in `make check-all` and a
   prerequisite of the image-publish job.

## Consequences

### Easier

- **The behaviour is reviewable by anyone.** "A path with one signal is operating
  noise; two is a warning; three is critical" is now a sentence that fails the
  build when it stops being true.
- **The joins are covered.** Status codes, `null` versus absent keys, RFC 3339
  window echoing, `omitted` reasons and error mapping — the layer that unit tests
  structurally cannot see — are exercised end to end.
- **Resilience is specified, not just implemented.** ADR 0011's numbers (three
  attempts, 4xx is permanent, five consecutive failures open the breaker, a
  recovered dependency closes it) are asserted through the real adapter.
- **A realistic second client of the ports.** The fakes make plain which port
  methods a route really consumes (`errNotUsed` marks the ones no scenario
  needs).

### Harder

- **A second vocabulary to maintain.** Step definitions are glue: a renamed DTO
  field or route means updating `features_test.go` and a feature file as well as
  the handler.
- **Step reuse needs discipline.** The generic JSON steps are deliberately the
  default; a new domain step should be added only when the generic ones cannot
  say it.
- **A little real time in the suite.** The breaker scenarios drive five failing
  calls (three attempts each, with the adapter's own backoff) and one scenario
  waits out a 300 ms breaker cooldown, so the suite takes a few seconds rather
  than milliseconds. The adapter exposes no injectable clock; sleeping a short,
  configured cooldown was judged better than weakening the production
  constructor.
- **Overlap with the `httptest` suite.** Some assertions now exist in both
  places. Accepted deliberately: the `httptest` tests are exhaustive per route,
  while the Gherkin scenarios are the readable specification of the important
  behaviours.
- **The scenarios do not replace the contract tests.** They prove the agent's
  behaviour over its ports; whether the real MCP clients speak each upstream's
  published schema stays the job of the `mcpclient` contract and eval tests.
