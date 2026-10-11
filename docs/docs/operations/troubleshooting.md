---
id: troubleshooting
title: Troubleshooting
sidebar_label: Troubleshooting
description: Symptom, cause, check and fix for the failure modes visible in warehouse-ops-agent's code — startup errors, 503/502/400 answers, partial briefs, empty dashboards, zeroed runtime signals and the LLM reasoner.
---

# Troubleshooting

Every row below comes from a code path on this branch. The agent has no
database, no Kafka consumer and no `/readyz`, so the fleet's usual
"consumer lag", "DLQ growth" and "migration failed" rows do not apply
([Runbook](./runbook.md#what-does-not-exist-here)). Errors are a flat
`{"error": "<message>"}` body, not RFC 7807 problem documents, so there
are no `409`, `412` or `422` problem types and no idempotency keys to
conflict (the API is read-only `GET`).

Start every investigation with the startup line:

```bash
kubectl -n warehouse-systems logs deploy/warehouse-ops-agent | grep 'warehouse-ops-agent listening'
```

It lists each `*_endpoint_configured` flag and `path_targets`, which
answers most "why is this feature off" questions.

## Startup

| Symptom | Cause | Check | Fix |
|---|---|---|---|
| Pod in `CrashLoopBackOff`; log `warehouse-ops-agent exited with error` with `config: DAILY_BRIEF_PATH_TARGETS is not a valid JSON array of path targets` | the variable is set to something that is not a JSON array of objects | `kubectl get cm warehouse-ops-agent -o yaml` and look at `DAILY_BRIEF_PATH_TARGETS` | fix the JSON, or clear `config.dailyBriefPathTargets` to use the built-in default target |
| Same, with `DAILY_BRIEF_PATH_TARGETS is set but lists no path targets` | the value is `[]` or `null` ([ADR 0017](../adr/0017-empty-path-targets-is-a-config-error.md)) | as above | list at least one target, or unset the variable |
| Same, with `policy: unrecognized LLM_MODE "<x>" (want off\|shadow\|on)` | typo in `LLM_MODE` | `LLM_MODE` in the ConfigMap | set `off`, `shadow` or `on` |
| Same, with `LLM_MODE=shadow requires ANTHROPIC_API_KEY` (or `on`) | non-`off` mode without a key; in the kind cluster `var.anthropic_api_key` was empty (Terraform warns about this in its `check` block) | `kubectl get secret warehouse-ops-agent-anthropic -o jsonpath='{.data.ANTHROPIC_API_KEY}'` is empty | supply the key and restart, or set `llm.mode` to `off` |
| Same, with `LLM_TOOL_ALLOWLIST entry "<x>" is not <upstream>/<tool>` | an allow-list entry has no `/` or an empty half | `LLM_TOOL_ALLOWLIST` | use `<upstream>/<tool>`, e.g. `wes-work-planning/get_backlog_telemetry` |
| Log `http server failed` with `address already in use`, process keeps running but nothing answers | `AGENT_ADDR` port taken; locally the e2e harness puts workforce-management's MCP server on `:8095` | `lsof -i :8095` | run with `AGENT_ADDR=:8096` ([Configuration](./configuration.md)) |
| Startup takes up to 15 s longer in `shadow`/`on`, then `llm reasoner: tool discovery failed for an upstream` | an allow-listed upstream did not answer `tools/list` in time | the `tools` field of `llm reasoner configured` lists what was found | not fatal: the model is offered only the tools that answered. Fix the upstream endpoint and restart to restore the full set |

## Readiness and health

| Symptom | Cause | Check | Fix |
|---|---|---|---|
| Pod is `Ready` but every answer is degraded | `/healthz` checks no upstream; it is green whenever the listener is up | call `/daily-brief` and look at each path's `unavailable` list | fix the upstream or endpoint named there; the probe is working as designed |
| Pod never becomes `Ready` | the process exited during startup (see above) before the listener opened | `kubectl logs --previous` | as in the Startup table |

## Answers from the REST routes

| Symptom | Cause | Check | Fix |
|---|---|---|---|
| `503 {"error":"product-master not configured"}` from `/master-data-gaps` | `PRODUCT_MASTER_MCP_ENDPOINT` unset; `find_master_data_gaps` is also missing from `tools/list` | startup line `product_master_endpoint_configured: false` | set `upstreams.productMaster.endpoint` |
| `503 {"error":"inbound-receiving not configured"}` from `/inbound-outlook` | `INBOUND_RECEIVING_MCP_ENDPOINT` unset | `inbound_receiving_endpoint_configured: false` | set `upstreams.inboundReceiving.endpoint` |
| `503` on `/transfer-watch/*` (`transfer watch not configured (network-inventory-planning endpoint unset)`) | `NETWORK_INVENTORY_PLANNING_MCP_ENDPOINT` unset | `network_inventory_planning_endpoint_configured: false` | set `upstreams.networkInventoryPlanning.endpoint` |
| `502` from `/explain-travel-factor` | facility-layout unreachable, or it rejected the call with a non-validation slug or a slug-less message (`internal/adapters/outbound/mcpclient/tool_error.go`) | log `explain_travel_factor: facility-layout unavailable` with `error` | check `FACILITY_LAYOUT_MCP_ENDPOINT` and facility-layout's MCP pod. An empty endpoint fails with `unsupported protocol scheme ""` |
| `400` from `/explain-travel-factor` | a location code is missing, or facility-layout answered `malformed-*`, `invalid-*`, `*-required`, `validation-failed` or `missing-location-code` | the `error` text | send both seven-segment codes, e.g. `WH1-STOR-AMB-A07-01-01-A` |
| `400` from `/flow-balance/{pathId}`, message starting `flow_balance_advisory:` | wes-work-planning returned a rebalance action outside `NoActionNeeded`, `ThrottleUpstream`, `ReassignLabor` (untrusted input is rejected, never defaulted) | the `error` text names the value | a contract change in wes-work-planning; update `internal/domain/policy` to the new enum in a code change |
| `/flow-balance/{pathId}` always `hold` with `partial: true` | a required signal is missing; `missingSignals` names it, e.g. `workforce-management.get_staffing_gap` | logs `flow_balance_advisory: <upstream> unavailable` and `partial decision` | fix that upstream; also check that `buildingId` and `shiftId` were sent, since empty values make the staffing lookup fail |
| No `utilization` in the flow balance | `LABOR_PERFORMANCE_MCP_ENDPOINT` unset or failing, the wes signal is missing, or `pathId` has no `processPath` binding in `DAILY_BRIEF_PATH_TARGETS` | log `flow_balance_advisory: labor-performance unavailable` | set the endpoint; add a target binding `pathId` to its `processPath` |
| `502` from `/master-data-gaps` | product-master unreachable or a page failed; the report has no partial mode | the `error` text | retry once product-master is healthy |
| `400` from `/master-data-gaps` | unknown `kind`, or product-master rejected the `cursor` | the `error` text | use `unclassified` or `dimension-discrepancy`; restart the scan without `cursor` |
| `502` from `/inbound-outlook` | every attempted section failed | each section's `omitted` reason in a partial `200` usually shows which tool fails first | check inbound-receiving's MCP server |
| `staleReceipts` always omitted with `INBOUND_STALE_RECEIPT_AGE is not configured` | there is deliberately no default | `INBOUND_STALE_RECEIPT_AGE` | set `config.inboundStaleReceiptAge`, e.g. `6h` |
| `400` from `/transfer-watch/stuck` | `olderThanMinutes` missing or not positive, `limit` outside 0–200, or `state` unknown or terminal | the `error` text | send `olderThanMinutes=30` and a non-terminal state |
| `404` from `/transfer-watch/transfers/{id}` | network-inventory-planning answered a `*-not-found` slug | the id | use an existing transfer id |
| `502` from `/transfer-watch/imbalance` right after a NIP restart | network-inventory-planning fails `simulate_transfer_options` until its read models are fresh | log `transfer_watch: network-inventory-planning call failed` | wait for NIP's projections to catch up |
| `404 {"error":"order not found"}` from `/console/orders/{id}/lifecycle` | order-management answered `404` | the order id | expected for an unknown order |
| One lifecycle stage is `null` in a `200` | that context's REST call failed or its URL is wrong; each URL falls back to a `localhost` default when unset | log `order_lifecycle: <context> unavailable` | set `restUrls.*` in the chart |
| `fulfillment` stage empty although tasks exist | tasks are looked up by each work unit's id (`GET /tasks?orderRef=<workUnitId>`), so no work units means no task lookup | `planning.workUnits` in the same response | expected before the order is released ([ADR 0002](../adr/0002-micro-frontend-console-architecture.md)) |
| A dashboard section `available: false`, `error: "<context> reports not available"` | the `*-reports` reader failed or the URL points nowhere; `5 s` timeout | log `console_reports: section unavailable` | set `reportsUrls.*`; confirm the context's `*-reports` Deployment exists (analytics enabled) |
| `freshnessLagSeconds: null` but the section is available | only the `/freshness` call failed | log `console_reports: freshness unavailable` | not a degradation; check the reader's `/freshness` route |
| `400` from `/console/reports/wms` or `/wes` | `from`/`to` not RFC3339, or `to` not after `from` | the `error` text | send e.g. `from=2026-10-09T00:00:00Z` |
| Browser call from the console fails with a CORS error | the console's origin is not in `CORS_ALLOWED_ORIGINS` (default only `http://localhost:5173`); only `GET` and `OPTIONS` are allowed | browser devtools, `Access-Control-Allow-Origin` missing | add the origin via `extraEnv` (`CORS_ALLOWED_ORIGINS` has no chart value) |
| `405` on a `POST` to a REST route | every REST route is `GET` only | — | use `GET`; `/mcp` is the only path that accepts other methods |

## Daily brief

| Symptom | Cause | Check | Fix |
|---|---|---|---|
| A path lists `"wes-work-planning: ..."` (or another upstream) in `unavailable` | that MCP call failed; an unset endpoint fails at call time with `unsupported protocol scheme ""` | the startup line's `*_endpoint_configured` | set the endpoint in `upstreams.*` |
| `siteName` is empty | facility-layout `list_sites` failed; the brief continues without names | `facility_layout_endpoint_configured` | set `FACILITY_LAYOUT_MCP_ENDPOINT` |
| No exception although one signal is clearly bad | an exception needs **two** of: backlog over its alarm threshold, path understaffed, stuck tasks above zero. Queue depth is not a signal | the path's `backlog`, `staffing`, `stuck` | expected behaviour (`policy.deriveExceptions`) |
| The brief monitors the wrong path | the built-in default target (`WH1` / `pick-zone-a`) is in use | `path_targets: 1` in the startup line | set `config.dailyBriefPathTargets` |
| `capacityOutlook` has `omittedReason` | warehouse-planning unreachable, no capacity window covers the horizon, or the target has no `planningPathId` | the reason text | add `planningPathId` to the target; check warehouse-planning |
| Brief is slow (tens of seconds) | calls run in sequence, each up to 10 s, over a fresh MCP session per call | server span duration for `/daily-brief` | fix the slow upstream; reduce path targets |

## Runtime signals

| Symptom | Cause | Check | Fix |
|---|---|---|---|
| Every service `normal`, every metric `0`, `prometheus` not in `unavailableSources` | `PROMETHEUS_URL` unset: a stub reader returns no samples | ConfigMap `PROMETHEUS_URL` is `""` (the kind cluster does not set it) | set `prometheus.url` |
| `unavailableSources: ["loki"]` | `LOKI_URL` unset or the Loki query failed | ConfigMap `LOKI_URL` | set `runtimeSignals.lokiUrl` |
| `unavailableSources: ["prometheus"]` | at least one PromQL query failed (5 s timeout) | Prometheus reachable at `PROMETHEUS_URL` + `/api/v1/query` | fix the URL or Prometheus |
| A context is missing from `services` | it is not in `RUNTIME_SIGNALS_SERVICES`; the default lists eight contexts only | the variable | set `runtimeSignals.services` |

## LLM reasoner

| Symptom | Cause | Check | Fix |
|---|---|---|---|
| `source` is always `deterministic` in `shadow` | expected: shadow mode never returns the model's plan | log `flow_balance_advisory: llm arbitration` shows `llm_action` and `agree` | none; compare agreement before switching to `on` |
| `source: fallback` in `on` mode | the reasoner errored, timed out (`LLM_TIMEOUT`, default 8 s), produced no plan in 6 turns, or the plan failed `policy.ValidatePlan` | the `reason` field of the arbitration log, e.g. `policy: invalid plan: proposedHeads 60 outside [0,50]` | for timeouts raise `LLM_TIMEOUT`; for invalid plans no fix is needed, the deterministic answer is used |
| Every call falls back with `anthropic: circuit breaker open for anthropic-llm` | 5 consecutive failures, or more than half of at least 10 requests failed in a 30 s window; the breaker stays open 30 s | `circuit_breaker_state{dependency="anthropic-llm"} == 2` | fix the cause (key, network, Anthropic outage); the breaker half-opens after 30 s and closes on one success |
| Immediate fallback on every request, no retries | a `4xx` from Anthropic (bad key, bad model id) is permanent and not retried | the `reason` text | fix `ANTHROPIC_API_KEY` or `LLM_MODEL` |
| `llm.tool_call` logs show `refused: unknown tool`, `refused: schema validation failed` or `refused: enum re-check failed` | the model asked for a tool it was not offered, or sent arguments outside the tool's schema | the `outcome` field | none; refusals are by design ([ADR 0004](../adr/0004-llm-reasoner-behind-the-policy-layer.md)) |
| An allow-listed tool is never offered | its upstream is not one of the five with a reasoner session (`wes-work-planning`, `fulfillment-execution`, `workforce-management`, `inventory-storage`, `facility-layout`), or discovery failed for it | `tools` in `llm reasoner configured` | use one of the five upstreams; fix the endpoint |

## MCP server

| Symptom | Cause | Check | Fix |
|---|---|---|---|
| A tool is missing from `tools/list` | the optional upstream behind it is not configured | [MCP tools](../mcp/tools.md) "Registered when" column | set the endpoint |
| Tool result `isError: true` with `invalid severity "<x>"` | `list_open_exceptions` accepts only `info`, `warning`, `critical` | — | send a valid severity or omit it |
| Host expects a `<slug>: ` prefix and cannot classify errors | this server returns the use case's plain message, not an ADR 0018 slug | [MCP tools](../mcp/tools.md#tool-errors) | match on the message text |
