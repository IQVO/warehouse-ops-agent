---
id: runbook
title: Runbook
sidebar_label: Runbook
description: How warehouse-ops-agent is deployed and operated — the Helm chart, the single Deployment, probes, scaling, the LLM key, and the routine procedures. No database, no Kafka, no migrations.
---

# Runbook

warehouse-ops-agent is one stateless Go process (`cmd/agent`). It owns no
database and no Kafka topic. Every answer is computed at request time from
the sibling contexts' MCP servers, their REST and `*-reports` APIs,
Prometheus, Loki and, when enabled, the Anthropic Messages API. Most
"operations" for this service are therefore about configuration and about
the health of its upstreams. Every variable named here is described on
[Configuration](./configuration.md).

## What does not exist here

Several sections of a fleet runbook do not apply to this repo. They are
listed so nobody goes looking for them:

| Fleet concept | In this repo | Evidence |
|---|---|---|
| Postgres, migrations, `MIGRATIONS_DATABASE_URL` | none | no `migrations/` directory; no `database/sql` or `pgx` import; chart `values.yaml` comment: "no database, no Kafka" |
| Kafka producer or consumer, consumer groups | none | no Kafka client import; `internal/observability/telemetry.go` says the agent "never produces or consumes a Kafka message" |
| CloudEvents types, outbox relay, DLQ topics | none | nothing is published; see [Domain events](../ddd/domain-events.md) |
| Sweepers, housekeeping jobs, schedulers | none | `cmd/agent/main.go` starts only the HTTP server |
| `/readyz` | none | the router registers only `/healthz` (`internal/adapters/inbound/http/router.go`) |
| A second binary (`cmd/mcp`, `*-reports`, projector) | none | `cmd/` holds only `agent`; the MCP server is mounted at `/mcp` on the same listener |

## Deployment

| Item | Value | Source |
|---|---|---|
| Image | `ghcr.io/iqvo/warehouse-ops-agent`, tags `latest` and the short SHA on every push to `main`, plus `vX.Y.Z` from the `release` job | `.github/workflows/ci.yml` (`docker-publish`, `release`) |
| Helm chart | `charts/warehouse-ops-agent` (chart `version` and `appVersion` `0.1.0` in the repo; the `release` job packages it with the release version and pushes it to `oci://ghcr.io/iqvo`) | `charts/warehouse-ops-agent/Chart.yaml`, `ci.yml` |
| Workload | one `Deployment` named after the release fullname, one container, `replicaCount: 1` unless the HPA is on | `templates/deployment.yaml`, [ADR 0012](../adr/0012-horizontal-autoscaling-single-deployment.md) |
| Container port | `http`, `8095` (`service.targetPort`). Every REST route and `/mcp` share it. | `values.yaml`, `templates/deployment.yaml` |
| Service | `ClusterIP`, port `80` to target port `8095` | `values.yaml` (`service`) |
| Edge | Ingress (`ingress.enabled`) or Gateway API `HTTPRoute` (`gatewayApi.enabled`); both off by default in the chart | `templates/ingress.yaml`, `templates/httproute.yaml` |
| Configuration | a `ConfigMap` loaded with `envFrom`, plus explicit `env:` entries for the listener, the MCP endpoints, `PROMETHEUS_URL`, the OTel variables and the API key | `templates/configmap.yaml`, `templates/deployment.yaml` |
| Secret | `<fullname>-credentials` with key `ANTHROPIC_API_KEY`, created only when `credentials.anthropicApiKey` is set and `credentials.existingSecret` is empty | `templates/secret.yaml`, `_helpers.tpl` (`credentialsSecretName`) |
| Restarts on config change | the pod template carries `checksum/config` and `checksum/secret` annotations, so a changed value rolls the pods | `templates/deployment.yaml` |
| Resources | requests `100m` / `128Mi`, limits `500m` / `256Mi` | `values.yaml` |
| Security context | non-root user `1000`, all capabilities dropped, no privilege escalation | `values.yaml` |

### In the kind cluster (warehouse-infra)

warehouse-infra's `terraform/ops-agent.tf` computes the Helm values and
ArgoCD owns the release (`terraform/argocd-apps.tf`); there is no
`helm_release` for it any more. That overlay:

- pulls `ghcr.io/iqvo/warehouse-ops-agent:latest` with `pullPolicy: Always`;
- sets every MCP endpoint from `terraform/mcp.tf` when `deploy_mcp_servers`
  is true, and every `restUrls.*` and `reportsUrls.*` to the in-cluster
  Services;
- runs `llm.mode = "shadow"` with `credentials.existingSecret =
  "warehouse-ops-agent-anthropic"`, a Terraform-managed Secret filled from
  `var.anthropic_api_key`;
- routes `/api/warehouse-ops-agent` through Kong with the prefix stripped,
  so `http://localhost:8000/api/warehouse-ops-agent/daily-brief` reaches the
  router as `/daily-brief`.

It sets neither `prometheus.url` nor `runtimeSignals.lokiUrl`, so
`GET /runtime-signals` reports zeros there (see
[Troubleshooting](./troubleshooting.md)).

## Probes and startup

Both probes call `GET /healthz` on the `http` port (`values.yaml`):

| Probe | Initial delay | Period |
|---|---|---|
| liveness | 5 s | 10 s |
| readiness | 3 s | 5 s |

`/healthz` returns `200 {"status":"ok"}` as soon as the listener is up. It
checks no upstream, so readiness flips immediately after the process
starts and stays green even when every sibling context is down. That is
deliberate: each use case degrades on its own (an `unavailable` entry, a
`null` stage, a `hold`) instead of failing the pod.

Startup order in `cmd/agent/main.go` (`run`):

1. Build the JSON logger at `LOG_LEVEL`.
2. Install the OTel tracer and meter providers. An unreachable Collector
   does not block startup; the exporters dial lazily.
3. `config.Load()`. A malformed or empty `DAILY_BRIEF_PATH_TARGETS` returns
   an error and the process exits with status 1.
4. Build every outbound client. Nothing is dialled yet: each MCP call opens
   its own session later.
5. `wireReasoner`. An unknown `LLM_MODE`, a non-`off` mode without
   `ANTHROPIC_API_KEY`, or a malformed `LLM_TOOL_ALLOWLIST` entry exits with
   status 1. In `shadow` or `on` mode it then calls `tools/list` on the
   allow-listed upstreams, bounded by 15 s. A failed discovery is logged
   (`llm reasoner: tool discovery failed for an upstream`) and is not fatal.
6. Log `warehouse-ops-agent listening` with one
   `*_endpoint_configured` boolean per MCP upstream and the number of path
   targets, then serve.

On `SIGINT` or `SIGTERM` the server stops accepting connections and gives
in-flight requests 10 s to finish (`serveAgent`), then flushes the OTel
providers with a 5 s timeout.

## Scaling

The agent keeps no state between requests, and the MCP server runs in
stateless Streamable HTTP mode, so any replica can answer any request
([ADR 0012](../adr/0012-horizontal-autoscaling-single-deployment.md)).

| Setting | Default | Notes |
|---|---|---|
| `autoscaling.enabled` | `false` | when `true`, `templates/hpa.yaml` renders an `autoscaling/v2` HPA and `replicas` is omitted from the Deployment |
| `autoscaling.minReplicas` / `maxReplicas` | `1` / `3` | |
| `autoscaling.targetCPUUtilizationPercentage` | `70` | the only metric wired by default; `targetMemoryUtilizationPercentage` is commented out |

There is no connection pool to size. What a new replica adds is load on
the upstreams: each daily brief makes one `list_sites` call plus four MCP
calls per path target (five when warehouse-planning is configured), each
over a fresh MCP session with a 10 s timeout.
In `shadow` or `on` mode each flow-balance request also makes up to six
Anthropic calls, and the circuit breaker is per process, so every replica
trips independently.

## Upstream dependencies at a glance

| Dependency | Needed for | If it is down |
|---|---|---|
| wes-work-planning, fulfillment-execution, workforce-management MCP | daily brief, flow balance | brief entries marked `unavailable`; flow balance partial or `hold` |
| facility-layout MCP | explain-travel-factor; site names in the brief | explain-travel-factor `502`; brief keeps working without `siteName` |
| inventory-storage MCP | `detect_stranded_reservation` | the missing reading is left out and the policy cannot recommend `revoke_reservation`, so the tool returns `hold` |
| labor-performance MCP | the flow-balance `utilization` overlay | the overlay is omitted |
| warehouse-planning, product-master, inbound-receiving, network-inventory-planning MCP | optional features | see [Integration](../ecosystem/integration.md) |
| four OLTP REST APIs | order lifecycle | that stage is `null` (order-management `404` gives a `404`) |
| seven `*-reports` REST readers | WMS and WES dashboards | that section is `available: false` |
| Prometheus, Loki | runtime signals | `unavailableSources` lists the source |
| Anthropic Messages API | `LLM_MODE` `shadow` or `on` | deterministic decision returned; breaker opens after repeated failures |

The full per-edge contract is on [Integration](../ecosystem/integration.md).

## Routine procedures

### Deploy a new version

Merging to `main` publishes `:latest` and a short-SHA tag and, through the
`release` job, a `vX.Y.Z` image tag and chart. With `pullPolicy: Always`
on `:latest`, a rollout restart picks up the new image:

```bash
kubectl -n warehouse-systems rollout restart deployment/warehouse-ops-agent
kubectl -n warehouse-systems rollout status deployment/warehouse-ops-agent
```

The Deployment name is the chart fullname; with release name
`warehouse-ops-agent` it is `warehouse-ops-agent`. Check it with
`kubectl -n warehouse-systems get deploy`.

### Turn an optional feature on or off

Set or clear the matching chart value (for example
`upstreams.productMaster.endpoint`). The checksum annotation rolls the pod.
Confirm in the startup log line: `product_master_endpoint_configured`
should read `true`, and `GET /master-data-gaps` should stop answering
`503`.

### Change the LLM mode

1. For `shadow` or `on`, make sure the Secret has a real
   `ANTHROPIC_API_KEY` first; otherwise the pod crash-loops with
   `LLM_MODE=shadow requires ANTHROPIC_API_KEY`.
2. Set `llm.mode`. Watch the `llm reasoner configured` log line: its
   `tools` list shows which allow-listed tools were discovered.
3. In `shadow`, compare `ops_agent_llm_agreement_total{agree="true"}` with
   the total for `use_case="flow_balance_advisory"` before moving to `on`
   ([Observability](./observability.md)).
4. To stop calling the model immediately, set `llm.mode` back to `off`.
   The deterministic policy is unchanged by the mode.

### Rotate the Anthropic API key

Update the key in the Secret the chart reads: `credentials.anthropicApiKey`
(chart-managed Secret) or the external Secret named by
`credentials.existingSecret` (in the kind cluster, `TF_VAR_anthropic_api_key`
then `terraform apply` rewrites `warehouse-ops-agent-anthropic`). The key is
read once at startup, so restart the Deployment afterwards. A chart-managed
Secret change rolls the pod by itself through `checksum/secret`; an
external Secret does not.

### Replay, re-publish, rebuild

Not applicable. The agent stores nothing, so there is nothing to replay
or rebuild. Re-asking the same question (`GET /daily-brief` again)
recomputes the answer from the upstreams' current state.

## Related pages

- [Configuration](./configuration.md) — every variable and chart value.
- [Observability](./observability.md) — metrics, spans, logs, alerts.
- [Troubleshooting](./troubleshooting.md) — symptoms and fixes.
- [HTTP routes](../api/http-routes.md) — every route and its statuses.
