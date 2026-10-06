---
paths:
  - "docs/**"
  - ".github/workflows/docs.yml"
---

# Docs site

This repo has its own Docusaurus site under `docs/` (business context, DDD
placement/subdomain-classification, context map, API surface, governance
note, every ADR). It publishes via `.github/workflows/docs.yml` to
`https://iqvo.github.io/warehouse-ops-agent/` on push to `main`
touching `docs/**`.

Local dev: `cd docs && npm install && npm start`.

`docs/docs/api-surface.md` is the hand-maintained prose doc for this repo's
REST + MCP surface — there is no generated OpenAPI page because this repo
has no OpenAPI spec by design (see `.claude/rules/architecture-guardrails.md`);
if that page drifts from `internal/adapters/inbound/{http,mcp}`, the code
is authoritative, not the doc.
