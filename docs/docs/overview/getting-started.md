---
id: getting-started
title: Getting started
sidebar_label: Getting started
description: Run warehouse-ops-agent locally, wired against the five sibling MCP servers, and hit its daily brief.
---

# Getting started

`warehouse-ops-agent` is a single Go binary. It has no database of its own
— it holds no persisted state — and needs no Postgres. What it does need
is a Streamable HTTP endpoint for each upstream context's MCP server it
should read (an unset endpoint simply skips that client; see
[Configuration](https://github.com/IQVO/warehouse-ops-agent#configuration)
in the repo README for the full environment-variable table).

## Run it against the full fleet

The simplest way to exercise this agent end-to-end is via the shared
`e2e-tests` harness, which brings up the bounded contexts, their MCP
servers, and this agent together. Kafka is not started by the harness — it
is the single shared broker in the `warehouse-infra` kind cluster
(`localhost:9092`), so bring that cluster up first:

```bash
cd ~/warehouse-systems/e2e-tests
bash scripts/01-build.sh         # builds all binaries, including this agent
bash scripts/02-up-infra.sh      # checks Kafka at localhost:9092, starts the
                                  # harness's Postgres instances
bash scripts/03-up-services.sh   # starts every service + every MCP server
                                  # + this agent (on :8096), pointed at them
```

## Run it standalone

Ports below match the e2e-tests harness's `env.sh` (`AGENT_ADDR` defaults
to `:8095`, which collides with the harness's workforce-management MCP
port, hence the override):

```bash
export FACILITY_LAYOUT_MCP_ENDPOINT=http://localhost:8091/mcp
export INVENTORY_STORAGE_MCP_ENDPOINT=http://localhost:8092/mcp
export WES_WORK_PLANNING_MCP_ENDPOINT=http://localhost:8093/mcp
export FULFILLMENT_EXECUTION_MCP_ENDPOINT=http://localhost:8094/mcp
export WORKFORCE_MANAGEMENT_MCP_ENDPOINT=http://localhost:8095/mcp
export LABOR_PERFORMANCE_MCP_ENDPOINT=http://localhost:8097/mcp   # optional
export AGENT_ADDR=:8096

go run ./cmd/agent
```

No bearer keys are needed — every MCP and REST endpoint in the fleet is
unauthenticated ([ADR 0006](../adr/0006-fleet-wide-auth-removal.md)).

## Hit the daily brief

```bash
curl -s http://localhost:8096/daily-brief | jq .
```

A path with two or more correlated signals (backlog over its alarm
threshold, understaffed, or stuck tasks) shows up under
`openExceptions`, each entry carrying its `evidence` trail.

## Quality gate

```
make check       # fast pre-commit bundle: fmt-check vet build lint test
make check-all   # + coverage (90% gate) + arch-test (pre-push gate)
```

`lefthook install` once to activate the pre-commit/pre-push git hooks.
