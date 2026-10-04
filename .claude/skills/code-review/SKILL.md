---
name: code-review
description: Fast pre-commit semantic review of the uncommitted changes or a given git range: domain logic in the wrong layer, missing failure-path tests, adapter concerns in domain types, impure ports. Advisory counterpart to make check. Invoke explicitly: /code-review [range].
disable-model-invocation: true
argument-hint: "[git range]"
---

Perform a pre-commit semantic code review of the current uncommitted
changes (or, if `$ARGUMENTS` names a branch/commit range, review that
diff instead — e.g. `/code-review origin/develop..HEAD`).

This is a fast, local, advisory pass — the counterpart to `make check`'s
mechanical checks (fmt/vet/lint/test), not a replacement for them. Run
`make check` FIRST; don't spend this review's attention on anything a
linter already catches.

## What to look for, in priority order

1. **Domain logic leaking into the wrong layer.** Business rules belong
   in `internal/domain/policy/`, not in an HTTP handler, an MCP tool handler,
   or an outbound client. If a handler in `internal/adapters/inbound/http/`
   or `internal/adapters/inbound/mcp/` does anything beyond decode -> call
   use case -> encode, flag it — that logic likely belongs in the use case
   or a pure policy function.
2. **A new use case with no failing-path test.** Check
   `internal/application/usecases/` — every new/changed `Execute` method
   needs a test for its domain-rule failure path, not just the happy
   path. This is the single most common gap `make coverage`'s 90% gate
   still lets through (100% line coverage on the happy path alone often
   clears 90%).
3. **A domain type carrying an adapter concern.** JSON tags, SQL column
   names, or HTTP status codes appearing on a type under
   `internal/domain/` is a boundary violation `internal/architecture/`'s
   fitness tests won't catch (they check import direction, not tag
   presence) — flag it by eye.
4. **A driven port (`internal/ports/`) that carries behaviour**, or a use
   case constructing a concrete adapter directly instead of depending on a
   port. Ports contain interfaces and plain DTO structs (`dto.go`) only.
5. **Error handling that swallows, over-wraps, or defaults.** An upstream
   failure must degrade to a partial result (reported, never a panic or a
   whole-response 500); an unknown enum value (MCP tool arg, REST query
   param, LLM `submit_plan` output) must be rejected explicitly, never
   defaulted. Handlers in `internal/adapters/inbound/http/router.go` answer
   `400` for bad input and `503` for an unconfigured use case — flag
   anything that stringifies or re-wraps errors repeatedly on the way out.
6. **A mutating call or a non-read-only tool.** Any new outbound method
   that writes, or any MCP tool registered without `ReadOnlyHint: true`,
   violates the zero-write rule; `make arch-test` catches the literal
   forms, flag the rest by eye. Also flag any new Kafka client — this
   service has no Kafka I/O (see CLAUDE.md, "Events").
7. **A newly reintroduced auth/bearer/JWT check.** Every REST/MCP
   endpoint in this fleet is deliberately unauthenticated (2026-09-11
   fleet-wide revert) — a reintroduced auth check is very likely
   accidental (an agent "helpfully" adding back something that looks
   missing) and should be flagged even if the code itself looks correct.
8. **Anything that would surprise the sibling-context boundary.** A Go
   import of an upstream bounded-context module, or a hand-mirrored type
   that is now type-shared, breaks `.claude/rules/architecture-guardrails.md`;
   check the diff doesn't reintroduce exactly that.

## Output format

For each finding: file:line, a one-sentence description of the issue, and
a one-sentence suggested fix. Group findings by severity
(blocking/should-fix/nit). If nothing needs fixing, say so plainly — don't
manufacture findings to justify the review.

This command never modifies files or runs `git commit`/`git push`. It
only reports.
