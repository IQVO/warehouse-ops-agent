---
paths:
  - "go.mod"
  - "internal/adapters/**/kafka/**"
  - "internal/adapters/outbound/events/**"
---

# Events: CloudEvents 1.0 is MANDATORY

This service **currently has no Kafka I/O at all** — it neither produces nor
consumes any Kafka message (no Kafka client in `go.mod`; it reads sibling
contexts only through their MCP/REST surfaces). The fleet rule still binds
any FUTURE Kafka integration added here: every message on integration
`warehouse.<ctx>.events` AND analytics `warehouse.<ctx>.analytics` topics is
a CloudEvents 1.0 event in structured content mode. This is a hard fleet
rule, not a preference:

- No flat envelope (`event_id`/`event_type`/`occurred_at`), no dual-write,
  no dual-read, no envelope toggle env var (`EVENT_ENVELOPE_MODE` is gone).
- Build/validate/(un)marshal with `github.com/cloudevents/sdk-go/v2/event`
  via a future repo-local cloudevents helper package under
  `internal/adapters/` (copy the fleet reference helper from
  warehouse-harness-template, templates/cloudevents/); transport stays
  kafka-go.
- Kafka header `content-type: application/cloudevents+json; charset=UTF-8`.
- Required attributes: `specversion=1.0`, `id` (UUID, stable across outbox
  redelivery), `source=/warehouse/warehouse-ops-agent`, `type`, `subject`
  (aggregate id), `time` (occurred-at, UTC),
  `datacontenttype=application/json`,
  `dataschema=urn:warehouse:warehouse-ops-agent:<events|analytics>:<EventName>:v<N>`.
- `type` = `com.warehouse.<subdomain>.<bounded-context>.<entity>.<EventName>`;
  this service has no row in the fleet subdomain table yet — any first
  Kafka integration must add one (ADR) before publishing. Breaking payload
  change => new `.v2` type + new dataschema version, never mutate.
- Consumers dispatch on the FULL `type`, ignore unknown types, dedupe on
  `id`, and DLQ/skip (never crash, never parse a legacy shape) anything that
  fails CloudEvents validation. A consumer of a sibling's topic must use
  the exact `type` strings from the fleet's cross-service type catalogue.

Full standard and the fleet's cross-service type catalogue: the warehouse-docs
repo, strategic-design/event-standard-cloudevents.md (each Kafka-using
service carries it as its "CloudEvents 1.0 as the mandatory event envelope"
ADR under `docs/docs/adr/`).
