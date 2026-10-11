---
id: 0020-product-master-mcp-client-and-master-data-gaps
title: "0020 — product-master MCP client (read-only) and the master-data gaps report"
sidebar_label: "0020 · product-master client + master-data gaps"
description: "The agent reads product-master's four published read tools through a schema-pinned MCP client (PRODUCT_MASTER_MCP_ENDPOINT, unset = disabled, fail-open) and uses list_products for one decision-support report: unclassified products and products whose declared and measured dimensions disagree."
---

# ADR 0020: product-master MCP client (read-only) and the master-data gaps report

## Status

Accepted (2026-10-07). Follows the shape of
[ADR 0013](./0013-warehouse-planning-mcp-client-and-capacity-outlook.md)
(the previous upstream added to this agent) and applies
[ADR 0018](./0018-mcp-tool-error-slug-classification.md)'s slug
classification unchanged. Number 0019 is left to the in-flight
network-inventory-planning ADR (PR #119).

## Context

`product-master` is the fleet's owner of product master data: the
description, the handling classification (hazmat, fragile,
temperature-sensitive, oversized, high-value; DOT hazard class; temperature
class) and the physical profile (declared vs measured unit dimensions,
product-master ADR 0002). Its own ADR 0005 publishes a **read-only** MCP
server: `cmd/mcp`, Streamable HTTP on `:8090` at `/` and `/mcp`, four tools,
every one annotated `readOnlyHint`:

| Tool | Arguments | Result |
|---|---|---|
| `get_product` | `sku` | `sku`, `description`, `version`, `classification` (absent when unclassified), `physical_profile` |
| `list_products` | optional `limit` (1..500), `cursor`, `handling_tag`, `classified` | `items[]`, `next_cursor` (absent on the last page) |
| `get_product_classification` | `sku` | `handling_tags`, `temperature_class`, `dot_hazard_class`, `classification_source`, `version` |
| `get_physical_profile` | `sku` | `declared`, `measured` (+ `measured_at`, `device_id`), `effective`, `effective_source`, `discrepancy`, `version` |

Writes (register, classify, declare dimensions, record a measurement) are
REST-only there, because their events feed other contexts' local copies.

This agent is the fleet's read-side Customer. Two operational questions
nobody answers today are "which products still have no classification?"
(every consumer of `ProductClassified` then handles them as untagged) and
"which products' declared dimensions are contradicted by a measurement?"
(slotting, cartonisation and travel use the effective, measured value, but
the declaration is still wrong upstream).

## Decision

1. **Client.** `internal/adapters/outbound/mcpclient/product_master.go`
   implements `ports.ProductMasterClient`
   (`internal/ports/clients_product_master.go`): exactly the four tools
   above, field-for-field DTO mirrors, zero/nil list arguments omitted so
   product-master's own defaults apply. Same resilience as every sibling
   client: per-call timeout, fresh session per call, no retry, no breaker.
2. **Configuration.** `PRODUCT_MASTER_MCP_ENDPOINT` (chart:
   `upstreams.productMaster.endpoint`; in cluster
   `http://product-master-mcp.warehouse-systems.svc.cluster.local:8090/mcp`).
   The name follows this repo's `*_MCP_ENDPOINT` convention and
   warehouse-infra's `upstreams.<ctx>.endpoint` → env mapping, not the
   `PRODUCT_MASTER_MCP_URL` spelling used in the migration brief. **Unset =
   disabled, fail-open**: no client is built, the agent boots exactly as
   before, `GET /master-data-gaps` answers 503 and `find_master_data_gaps`
   is not registered. The startup log line carries
   `product_master_endpoint_configured`.
3. **Contract pinning (consumer side).**
   `mcpclient/testdata/product_master_tools.golden.json` is a byte-identical
   copy of product-master's own published registry golden
   (`internal/adapters/inbound/mcp/testdata/tool_registry.golden.json`,
   product-master `a0a303e`). Tests fail when the golden is not exactly the
   four tools or any is not `readOnlyHint`; when a port DTO json tag is not a
   published output property or a required output property is not mirrored;
   and the client's wire tests run against a stand-in server that registers
   the four tools **with the golden's input and output schemas**, so the SDK
   rejects any misspelt argument key (`additionalProperties: false`) and any
   canned result that drifts from the published shape. Refresh the golden
   with `git -C ../product-master show origin/develop:internal/adapters/inbound/mcp/testdata/tool_registry.golden.json`.
4. **Write-verb guard.** `zerowrite.TestMCPClientsCallOnlyReadTools` now also
   rejects `classify_` and `record_` (product-master's REST-only write verbs,
   should they ever be published), and
   `zerowrite.TestProductMasterClientCallsOnlyPinnedTools` pins
   `product_master.go` to exactly the four tool names, so a non-prefixed new
   verb cannot slip through either. `TestNoDirectDependencyOnBoundedContexts`
   lists `github.com/claudioed/product-master`.
5. **One decision-support use case: master-data gaps.**
   `usecases.MasterDataGaps` pages through `list_products` (500 per page, at
   most 10 pages = 5,000 products per request) and hands the products to the
   pure rule `policy.FindMasterDataGaps`, which reports two kinds:
   - `unclassified`: product-master returned no classification;
   - `dimension-discrepancy`: product-master's **own** `discrepancy` flag is
     set; both dimension sets (and `measured_at` / `device_id`) are echoed.
     The agent never recomputes the tolerance.
   Only two caller inputs exist, both optional: `kind` (validated; an unknown
   value is rejected, never defaulted) and `cursor` (resume). It invents
   nothing: no threshold, no default classification, no guessed dimension.
   `kind=unclassified` is pushed down as `classified=false`. A scan that hits
   the page bound says `complete: false` and returns `nextCursor`; it never
   presents a partial catalogue as complete. Exposed as
   `GET /master-data-gaps?kind=&cursor=` (400 for an unknown kind or a cursor
   product-master rejects with a validation slug, 502 when product-master
   fails, 503 when not configured) and the read-only MCP tool
   `find_master_data_gaps`.
6. **Not fail-open inside the use case.** Unlike ADR 0013's outlook (a
   section of a bigger brief), this report is product-master's data and
   nothing else: an unreachable product-master or a failing page is an error,
   not an empty report.

`get_product`, `get_product_classification` and `get_physical_profile` are
wired, contract-tested, and not yet consumed by a use case; they are the
per-SKU drill-down for a future LLM tool allow-list entry
(`product-master/<tool>`) or another correlation.

## Consequences

- The agent can answer "what master data is missing or contradictory" with
  evidence and a one-line rationale per gap, read-only.
- A product-master tool-surface change (ADR 0005 amendment) shows up here as a
  failing contract test the moment the golden is refreshed, naming the
  broken assumption, instead of as a silent decode of zero values.
- warehouse-infra needs one line to turn it on in the cluster:
  `productMaster = { endpoint = local.mcp_endpoint["product-master"] }` in
  `terraform/ops-agent.tf` (and product-master in `local.mcp_services`). That
  is a separate warehouse-infra change; until then the feature is off.
- Repeated full scans cost up to 10 `list_products` calls per request; the
  page bound keeps a single request bounded, and the result says when more
  remain.
