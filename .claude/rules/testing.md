---
paths:
  - "**/*_test.go"
  - "Makefile"
  - ".gremlins.yaml"
  - ".github/workflows/ci.yml"
---

# Testing

```bash
go test ./... -race                       # unit tests, no DB/live MCP
go test ./internal/architecture/... -v    # arch fitness tests (or: make arch-test)
make coverage                             # coverage run + 90% gate
make mutation-fast                        # gremlins over ./internal/domain (see .gremlins.yaml); CI's blocking mutation job
make vuln                                 # govulncheck ./...
lefthook install                          # once, to activate pre-commit/pre-push git hooks
```

- **Coverage gate is 90%**, scoped to
  `./internal/domain/...,./internal/application/...,./internal/adapters/inbound/...`
  (see `Makefile`'s `COVERPKG` / CI's `ci.yml` `test` job).
- **No database, no live MCP servers required for unit tests** — every use
  case is tested against fakes (`internal/application/usecases/fakes_test.go`).
- **The one legitimate env-gated test in this fleet**: an
  `-tags=integration` test may hit the real Anthropic API only when
  `ANTHROPIC_API_KEY` is set locally (per ADR 0004's Consequences — hosted
  models cannot use testcontainers). Every other integration-style test in
  this fleet must use testcontainers, never an env-var skip gate; this repo
  is the documented exception, not a precedent to copy elsewhere.
- **Architecture fitness tests** (`internal/architecture/architecture_test.go`)
  are the executable form of the "no upstream Go imports" and hexagonal
  dependency rules — run them (`make arch-test`) after any adapter/import
  change, not just before push.

See `.claude/skills/how-to-test/SKILL.md` for the layer-by-layer guide.
