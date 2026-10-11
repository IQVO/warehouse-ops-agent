---
id: 0021-inbound-receiving-mcp-client-and-inbound-outlook
title: "0021 — inbound-receiving MCP client (read-only) and the inbound outlook"
sidebar_label: "0021 · inbound-receiving client + inbound outlook"
description: "The agent reads inbound-receiving's seven published read tools through a schema-pinned MCP client (INBOUND_RECEIVING_MCP_ENDPOINT, unset = disabled, fail-open) and uses the list tools for one decision-support view: ASNs awaiting arrival, appointments in the next 24 hours, open receipts older than INBOUND_STALE_RECEIPT_AGE, and receipts closed with discrepancies today."
---

# ADR 0021: inbound-receiving MCP client (read-only) and the inbound outlook

## Status

Accepted (2026-10-08). Follows the shape of
[ADR 0020](./0020-product-master-mcp-client-and-master-data-gaps.md) (the
previous upstream added to this agent) and applies
[ADR 0018](./0018-mcp-tool-error-slug-classification.md)'s slug
classification unchanged.

## Context

`inbound-receiving` is the fleet's owner of the inbound dock side: advance
ship notices (ASNs), carrier dock appointments and goods receipts. Its own
ADR 0005 publishes a **read-only** MCP server (Streamable HTTP, seven tools,
every one annotated `readOnlyHint`):

| Tool | Arguments | Result |
|---|---|---|
| `get_asn` | `asn_number` | one ASN: `asn_number`, `supplier_ref`, `expected_arrival` (absent when the supplier gave none), `state` (Registered, Receiving, Closed, Cancelled), `lines[]`, `version` |
| `list_asns` | optional `limit` (1..500), `cursor`, `state` | `items[]`, `next_cursor` (absent on the last page) |
| `get_appointment` | `appointment_id` | one appointment: `door_code`, `carrier`, `window_start`, `window_end`, `asn_numbers[]`, `state` (Booked, CheckedIn, Completed, Cancelled) |
| `list_appointments` | optional `limit`, `cursor`, `door`, `state`, `from`, `to` | `items[]`, `next_cursor` |
| `get_receipt` | `receipt_id` | one receipt: `asn_number`, `appointment_id` / `door_code` (absent for a walk-in), `state` (Open, Closed), `lines[]`, `opened_at`, `closed_at` (absent while Open), `discrepancies[]` (Short, Over, Damaged) |
| `list_receipts` | optional `limit`, `cursor`, `asn_number`, `state` | `items[]`, `next_cursor` |
| `list_docks` | none | `mode` (permissive or kafka) and the inbound dock doors |

Its writes (register or cancel an ASN, book, check in or cancel an
appointment, open, receive against or close a receipt) are REST-only there.

This agent is the fleet's read-side Customer. The operational questions
nobody answers in one place are "what is due at the dock?" (ASNs not yet
received, appointments in the coming day) and "what needs attention?"
(receipts left open for hours, receipts that closed with a shortage, an
overage or damage today).

## Decision

1. **Client.** `internal/adapters/outbound/mcpclient/inbound_receiving.go`
   implements `ports.InboundReceivingClient`
   (`internal/ports/clients_inbound_receiving.go`): exactly the seven tools
   above, field-for-field DTO mirrors, zero or empty list arguments (and zero
   times) omitted so inbound-receiving's own defaults apply, `from` / `to`
   sent as UTC RFC 3339. Same resilience as every sibling client: per-call
   timeout, fresh session per call, no retry, no breaker.
2. **Configuration.** `INBOUND_RECEIVING_MCP_ENDPOINT` (in cluster
   `http://inbound-receiving-mcp.warehouse-systems.svc.cluster.local:8090/mcp`).
   **Unset = disabled, fail-open**: no client is built, the agent boots
   exactly as before, `GET /inbound-outlook` answers 503 and
   `get_inbound_outlook` is not registered. The startup log line carries
   `inbound_receiving_endpoint_configured`. A second variable,
   `INBOUND_STALE_RECEIPT_AGE` (a Go duration such as `6h`), is the
   operator's definition of a stale receipt and has **no default** (see 5).
3. **Contract pinning (consumer side).**
   `mcpclient/testdata/inbound_receiving_tools.golden.json` is a
   byte-identical copy of inbound-receiving's own published registry golden
   (`internal/adapters/inbound/mcp/testdata/tool_registry.golden.json`,
   inbound-receiving `139f73a`). Tests fail when the golden is not exactly
   the seven tools or any is not `readOnlyHint`; when a port DTO json tag is
   not a published output property or a required output property is not
   mirrored; and the client's wire tests run against a stand-in server that
   registers the seven tools **with the golden's input and output schemas**,
   so the SDK rejects any misspelt argument key and any canned result that
   drifts from the published shape. Refresh the golden with
   `git -C ../inbound-receiving show origin/develop:internal/adapters/inbound/mcp/testdata/tool_registry.golden.json`.
4. **Write-verb guard.** `zerowrite.TestMCPClientsCallOnlyReadTools` now also
   rejects `book_`, `check_in_`, `open_`, `receive_` and `close_`
   (inbound-receiving's REST-only write verbs, should they ever be
   published; `register_` and `cancel_` were already listed), and
   `zerowrite.TestInboundReceivingClientCallsOnlyPinnedTools` pins
   `inbound_receiving.go` to exactly the seven tool names, so a non-prefixed
   new verb cannot slip through either. The new use case adds no write
   path, so `zerowrite` stays green unchanged for the inbound MCP server:
   `get_inbound_outlook` is registered with `ReadOnlyHint: true`.
   `TestNoDirectDependencyOnBoundedContexts` lists
   `github.com/claudioed/inbound-receiving`.
5. **One decision-support use case: the inbound outlook.**
   `usecases.InboundOutlook` reads `list_asns`, `list_appointments` and
   `list_receipts` (500 per page, at most 10 pages = 5,000 records per
   section per request) and hands the facts to pure rules in
   `policy/inbound_outlook.go`. It reports four sections:
   - **awaiting ASNs**: ASNs in state `Registered`, earliest expected
     arrival first; `overdue` is true only when the supplier's own expected
     arrival exists and has passed;
   - **upcoming appointments**: `Booked` or `CheckedIn` appointments whose
     window overlaps `[now, now + 24 h)`, earliest window first (the call is
     windowed with `from=now`, `to=now+24h`);
   - **stale receipts**: `Open` receipts opened strictly more than
     `INBOUND_STALE_RECEIPT_AGE` ago, oldest first, with `openForSeconds`;
   - **discrepant receipts**: `Closed` receipts with at least one
     discrepancy whose `closed_at` falls on today's calendar day, with the
     discrepancy lines (kind, expected, received, damaged) exactly as
     inbound-receiving judged them. "Today" is the calendar day of the
     agent's clock in the agent's own time zone; the response echoes
     `dayFrom` / `dayTo`.

   It invents nothing. States, discrepancies and timestamps are
   inbound-receiving's published facts; "now" is the clock; the stale age is
   the operator's. `GET /inbound-outlook` and `get_inbound_outlook` take **no
   arguments**, so there is nothing for a caller or a model to make up. When
   `INBOUND_STALE_RECEIPT_AGE` is unset, zero or unparseable the stale
   section is reported as omitted with that reason and no Open receipts are
   listed; no threshold is guessed.
6. **Per-section honesty, all-or-nothing at the edge.** A failing list call,
   or a published instant that breaks the contract (missing `window_start`,
   unparseable `opened_at`, a Closed receipt without `closed_at`), omits that
   one section: it carries `omitted` with the reason, an empty list and
   `complete: false`, and the other sections are still reported. A reader
   must treat `omitted` as "unknown", never as "nothing to report". A section
   that reached the page bound says `complete: false` with the lower-bound
   list it has. Only when **every attempted section** failed does the use
   case return an error (502 over REST, a tool error over MCP), so an
   unreachable inbound-receiving is never rendered as an all-clear.
7. **Surface.** `GET /inbound-outlook` (200, 502, 503) and the read-only MCP
   tool `get_inbound_outlook`. JSON is camelCase, instants are UTC RFC 3339
   except `dayFrom` / `dayTo`, which keep the agent's local offset.

`get_asn`, `get_appointment`, `get_receipt` and `list_docks` are wired and
contract-tested and not consumed by a use case; they are the per-record
drill-down for a future LLM tool allow-list entry
(`inbound-receiving/<tool>`) or another correlation.

## Consequences

- The agent can answer "what is due at the dock and what needs attention
  today" with evidence per record, read-only.
- An inbound-receiving tool-surface change (its ADR 0005 amendment) shows up
  here as a failing contract test the moment the golden is refreshed, naming
  the broken assumption, instead of as a silent decode of zero values.
- `list_receipts` has no time filter, so the stale and discrepancy sections
  scan every Open (respectively Closed) receipt up to the page bound. The
  Closed set grows without bound; once it exceeds 5,000 receipts the
  discrepancy section reports `complete: false` and can miss today's receipts
  whose ids sort after the bound. The honest fix is a `closed_from` filter or
  a closed-at ordering on inbound-receiving's `list_receipts`, which is a
  separate change in that repo; until then the flag is the safeguard.
- "Today" follows the agent process's time zone (UTC in the standard image);
  a warehouse that reasons in another zone sees its day shifted by the
  offset. Making it explicit would be a new operator input and is left out.
- Deployment: the binary reads `INBOUND_RECEIVING_MCP_ENDPOINT` and
  `INBOUND_STALE_RECEIPT_AGE` from the environment, so the feature can be
  switched on today with `extraEnv`. The dedicated chart values
  (`upstreams.inboundReceiving.endpoint` and `config.inboundStaleReceiptAge`,
  each rendered only when set) are a follow-up: the editing tool refused
  every change to the chart's Helm templates in this change (it validates
  `*.yaml` as plain YAML, which Helm templates are not). warehouse-infra then
  needs one line to turn it on in the cluster. Until then the feature is off.
