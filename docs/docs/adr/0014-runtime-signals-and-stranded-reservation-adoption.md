---
id: 0014-runtime-signals-and-stranded-reservation-adoption
title: "0014 — Adoption record: GET /runtime-signals and detect_stranded_reservation (E2)"
sidebar_label: "0014 · runtime-signals + stranded-reservation adoption"
description: An after-the-fact adoption ADR for two shipped surfaces that predate having their own record — the Prometheus+Loki runtime-feedback report, and the E2 fulfillment-execution/inventory-storage lease-vs-reservation correlation — closing the ADR-index gap the 2026-10 fleet audit found.
---

# ADR 0014: Adoption record — GET /runtime-signals and detect_stranded_reservation (E2)

## Status

Accepted (adoption record — both surfaces were already implemented and
shipped before this record existed; see the 2026-10 fleet ADR-conformance
audit, which found them undocumented).

## Context

Two read-only surfaces exist in this codebase with no ADR of their own:

1. **`GET /runtime-signals`** (`internal/application/usecases/runtime_signals.go`,
   `internal/adapters/inbound/http/router.go`'s `getRuntimeSignals`) — a
   Phase 5 "runtime-feedback" report synthesizing Prometheus (error
   rate/p99 latency per service, via `ports.TelemetryReader`) and Loki
   (recent ERROR/FATAL log lines, via `ports.LogReader`) into one
   per-service severity classification (`internal/domain/policy`). It is
   the only use case in this repo that talks to the observability stack
   rather than a bounded context's own MCP/REST surface.
2. **`detect_stranded_reservation`** (the E2 MCP tool,
   `internal/application/usecases/stranded_reservation.go`) — correlates
   fulfillment-execution's `diagnose_stuck_tasks` (expired/expiring task
   leases) against inventory-storage's `check_availability`/
   `get_bin_occupancy` (usable-stock shortfall) for one SKU into a ranked
   `StrandedReservationException` recommendation. It only ever
   *recommends* `revoke_reservation`; it never calls inventory-storage's
   write tool itself.

Both predate this repo's ADR series catching up with its own build-out
pace — normal for a repo whose feature work outran its adoption-record
discipline, not evidence either surface deviates from an established
decision. This record closes that gap per the fleet convention: every
live surface gets a record, even retroactively.

## Decision

Document both as accepted, as-built, with no behavior change from this
record.

### 1. `GET /runtime-signals`

- **Inputs:** `cfg.RuntimeSignalsServices` (default: the fleet's 8 backend
  bounded contexts) and `cfg.RuntimeSignalsNamespace` (default
  `warehouse-systems`) scope the Prometheus/Loki queries; a 10-minute
  rolling window.
- **Degradation, not failure:** an unreachable Prometheus or Loki is
  recorded in `UnavailableSources` and that source's contribution to each
  `ServiceSignal` is simply omitted (zero-valued) — the same "partial
  availability degrades to a partial/typed result" guardrail `DailyBrief`
  already follows, never a 5xx for a down observability backend.
  `newTelemetryReader`/`newLogReader` (`cmd/agent/main.go`) return a
  no-op stub/nil when `PROMETHEUS_URL`/`LOKI_URL` are unset, so a
  deployment with no observability stack configured still serves
  `/runtime-signals` — it just reports everything unavailable.
- **Never mutates anything.** Pure read-side, same as every other route
  in this agent (ADR 0001).

### 2. `detect_stranded_reservation` (E2)

- **Ports consumed:** `ports.FulfillmentExecutionClient.DiagnoseStuckTasks`
  and `ports.InventoryStorageClient` (`check_availability`,
  `get_bin_occupancy`) — both already wired for other use cases (E1's
  `FlowBalanceAdvisory`, the MCP read surface), reused here rather than
  duplicated.
- **Degradation:** each upstream call failure folds into
  `policy.Evaluate`'s inputs as an unavailable/nil field, degrading the
  recommendation rather than failing the call — same discipline as E1's
  `Decide()`.
- **Mandatory blast radius:** a `revoke_reservation` recommendation is
  only ever emitted alongside the bin/quantity that would return to
  usable stock — never on partial evidence (`ReservationId`/`BinId` may
  be empty, in which case the policy still reports `Detected` for
  visibility but withholds the revoke recommendation).
- **Zero write capability.** Like `explain_travel_factor` (ADR 0009),
  this tool is annotated `ReadOnlyHint: true` and calls no write tool —
  the operator (or a future automation layer, out of scope here) decides
  whether to act on the recommendation.

## Consequences

**Positive** — the ADR index now accounts for every inbound surface this
repo exposes; a reader auditing "what does this agent actually do" no
longer has to fall back to reading `router.go`/`tools.go` source to find
two of its routes/tools.

**Negative / accepted** — none; this is a documentation-only record. No
code changed as a result of writing it.
