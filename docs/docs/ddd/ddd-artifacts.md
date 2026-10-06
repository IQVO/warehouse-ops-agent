---
id: ddd-artifacts
title: DDD artifacts (ddd-crew)
sidebar_label: DDD artifacts index
sidebar_position: 1
description: Index of the ddd-crew DDD artifact pack for warehouse-ops-agent, adapted honestly to a read-side decision-support service with no aggregate, no database and no events.
---

# DDD artifacts (ddd-crew)

This is the [ddd-crew](https://github.com/ddd-crew) artifact pack for
`warehouse-ops-agent`, plus UML class / ER / sequence diagrams, all derived
from the code on `develop`.

:::note[Read this first]
`warehouse-ops-agent` is **not** a bounded context in the
aggregate-and-invariant sense (see
[Subdomain classification](./subdomain-classification.md) and
[ADR 0001](../adr/0001-warehouse-ops-agent-placement.md)). It owns no
aggregate, has no Postgres, no migrations, and no Kafka I/O. Several
artifacts below therefore record an honest *absence* and then document what
the service does hold instead (pure policy value objects, in-memory read
models, process-local caches). They are kept under the standard file names
so the fleet aggregator can sync them mechanically.
:::

## The pack

| Artifact | ddd-crew tool / notation | What it shows here |
|---|---|---|
| [Core domain chart](./core-domain-chart.md) | [Core Domain Charts](https://github.com/ddd-crew/core-domain-charts) | Placement as Supporting (fleet map), with the "not a bounded context" caveat. |
| [Bounded context canvas](./bounded-context-canvas.md) | [Bounded Context Canvas v5](https://github.com/ddd-crew/bounded-context-canvas) | Purpose, classification, every inbound route/tool and outbound tool/endpoint. |
| [Context map](../ecosystem/context-map.md) | [Context Mapping](https://github.com/ddd-crew/context-mapping) | Upstream/downstream edges with OHS / CF / Separate Ways patterns (kept in `ecosystem/`). |
| [Aggregate design canvas](./aggregate-design-canvas.md) | [Aggregate Design Canvas v1.1](https://github.com/ddd-crew/aggregate-design-canvas) | Records that there is no aggregate root, and lists the read models / decision objects instead. |
| [Domain message flow](./domain-message-flow.md) | [Domain Message Flow Modelling](https://github.com/ddd-crew/domain-message-flow-modelling) | Four scenarios with `qry:` messages only (this service issues no commands and no events). |
| [EventStorming](./eventstorming.md) | [EventStorming glossary / cheat sheet](https://github.com/ddd-crew/eventstorming-glossary-cheat-sheet) | Design-level boards with read models, policies, external systems and hotspots; no domain events, by design. |
| [Ubiquitous language](../business-context/ubiquitous-language.md) | Glossary | Every term mapped to its code identifier (kept in `business-context/`). |
| [Class diagram](./class-diagram.md) | UML class diagram | `internal/domain/policy` types plus the hexagonal ports/adapters view. |
| [Entity relationship](./entity-relationship.md) | ER diagram | States that there is no database; documents the in-memory state instead. |
| [Sequence diagrams](./sequence-diagrams.md) | UML sequence diagrams | Every inbound use case: daily brief, flow balance (+ LLM arbitration), travel factor, stranded reservation, console-bff, runtime signals. |
| [Domain events](./domain-events.md) | Event catalogue | Records that no event is published or consumed, with the evidence. |

## Sources of truth

Code wins over every page here. The diagrams were derived from:

- `internal/domain/policy/*.go` — the pure decision-policy layer.
- `internal/application/usecases/*.go` — the use cases.
- `internal/ports/*.go` — outbound port interfaces and DTOs.
- `internal/adapters/inbound/http/router.go`,
  `internal/adapters/inbound/mcp/{server,tools}.go` — the inbound surface.
- `internal/adapters/outbound/{mcpclient,restclient,telemetry,logs,llm/anthropic}`
  — who this service calls.
- `cmd/agent/{main,reasoner}.go`, `internal/config/config.go` — wiring and
  configuration.
- `go.mod` (no Kafka, CloudEvents or database driver) and the absence of
  `migrations/` and `apis/`.

Hand-maintained companions: [API surface](../api-surface.md),
[Governance note](../mcp/governance-note.md), and the
[ADRs](../adr/index.md).
