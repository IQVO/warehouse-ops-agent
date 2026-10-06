---
id: domain-events
title: Domain events
sidebar_label: Domain events
sidebar_position: 10
description: warehouse-ops-agent publishes and consumes no domain events — no Kafka, no CloudEvents type, no AsyncAPI spec. This page records the evidence.
---

# Domain events

`warehouse-ops-agent` **publishes no event and consumes no event.** There
is no Kafka topic, no CloudEvents type (structured or binary mode), no
partition key, no outbox and no consumer group to list.

| Direction | Full CE type | Topic | Partition key | Producer / consumer | Known counterparts |
|---|---|---|---|---|---|
| Published | none | none | none | none | none |
| Consumed | none | none | none | none | none |

## Evidence

- `go.mod` has no Kafka client and no CloudEvents SDK; there is no
  `internal/adapters/kafka/` (or `inbound/kafka`, `outbound/events`)
  package.
- There is no `apis/asyncapi.yaml`, so `TestEventCatalogueMatchesContract`
  (`internal/architecture/catalogue_fitness_test.go`) skips with "no
  apis/asyncapi.yaml: nothing to compare", and `TestCloudEventsOnly`
  (`internal/architecture/events_fitness_test.go`) passes with nothing to
  check.
- Every fact the service uses is read synchronously at request time over
  MCP, REST, or the Prometheus / Loki HTTP APIs (see the
  [context map](../ecosystem/context-map.md)).
- The policy types that *sound* like events — `OpenException`,
  `StrandedReservationException`, the E1 `Decision` — are per-request
  return values, never emitted to a broker.

## If that ever changes

The repo rule is that any future Kafka message must be a CloudEvents 1.0
event in structured mode, with a `com.warehouse.<subdomain>.<service>.<entity>.<Event>`
type, no flat envelope and no dual-write (see `CLAUDE.md`,
"Events: CloudEvents 1.0 (structured mode) is MANDATORY", and
`.claude/rules/events-cloudevents.md`). The repo has no AsyncAPI spec by
design today; the catalogue fitness test only engages if an
`apis/asyncapi.yaml` is ever added, at which point every type it declares
for this service must also appear in a `*cloudevents*.md` ADR.
