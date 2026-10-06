---
name: how-to-write-an-adr
description: Write an Architecture Decision Record in this repo's numbering and format, including companion ADRs for cross-repo changes. Use when a design decision should be recorded or a change contradicts an existing ADR.
---

# How to write an ADR

Use when a change is architecturally significant for this repo — a new
outbound MCP tool call to one of the nine upstream contexts, a new
console-bff fan-out capability, a change to the ADR-0004 reasoner's
design, a reversal of a prior decision, or anything a future reader would
otherwise have to reverse-engineer from the diff. Not every change needs
one: a bug fix or a routine additive use case inside an already-decided
shape (a new correlation function alongside existing ones, say) doesn't.
**Adapted from inventory-storage's `.claude/skills/` reference (PR #75)
— the format and workflow below are fleet-wide and apply here unchanged;
only the worked examples are this repo's own.**

## Numbering and location

`docs/docs/adr/NNNN-kebab-case-title.md`, four-digit zero-padded,
sequential — check the highest existing number
(`git ls-tree --name-only origin/develop -- docs/docs/adr/` and pick the
next integer, never reuse or guess; new ADRs land often, so always
re-check on the branch you are merging into).
`docs/docs/adr/index.md` explains the format to readers AND carries the
table of every record — unlike some sibling repos, this repo's index has
no separate `about.md`; you must add your new ADR's row to `index.md`'s
table yourself (see step 5 below).

## Frontmatter (Docusaurus needs all five fields)

```yaml
---
id: NNNN-kebab-case-title
title: "NN. Title (a short noun phrase, matching the heading)"
sidebar_label: "NN. Short label for the nav sidebar"
description: "One or two sentences — this shows up in search and link
  previews, so make it stand alone without the rest of the doc."
---
```

Note this repo's real ADRs (see `0009-explain-travel-factor.md`) omit an
explicit `slug` field (Docusaurus derives it from the file path by
default here) — don't invent one unless you check `docusaurus.config.ts`'s
`docs` plugin options first and confirm a custom `routeBasePath` needs it.
`id`/`title`/`sidebar_label` repeat the number as plain text
(`"NN. ..."`, not `#NN`). Getting these inconsistent is the most common
cause of a broken sidebar entry or 404 after merge — verify by running
the docs build (see below) before opening the PR.

## Format: Michael Nygard's template

```markdown
# NNNN. Title (a short noun phrase)

## Status
Accepted | Proposed | Deprecated | Superseded by ADR-XXXX

## Context
The forces at play that make this decision necessary. State the
alternatives seriously considered, not just the one chosen.

## Decision
What was actually decided, stated as an active, present-tense
declaration. Be specific about the mechanism.

## Consequences
What becomes easier, what becomes harder, what future work this creates
or forecloses. Be honest about the downsides.
```

This repo's own `docs/docs/adr/index.md` states the same template
explicitly (numbered `0001-`, immutable once accepted) — it is the
authoritative in-repo description; this file just gives the worked
example.

Two of this repo's own ADRs are worth reading end-to-end as models before
writing a new one, for different reasons:

- **ADR 0004** (`docs/docs/adr/0004-llm-reasoner-behind-the-policy-layer.md`)
  — the reasoner design. A model example of a `## Decision` section that
  states an exact mechanism (`Reasoner` port, `Brief`/`Plan` DTOs, the
  `off`/`shadow`/`on` mode table) specific enough to implement from, plus
  a genuinely honest `## Consequences` (cost doubling in shadow mode, a
  hosted-model dependency on the request path — not just upside).
- **ADR 0006** (`docs/docs/adr/0006-fleet-wide-auth-removal.md`) — a
  fleet-wide decision recorded in EVERY affected repo, not just one. Its
  `Scope: fleet-wide` frontmatter line and its explicit statement of
  which of this repo's own files/packages it deletes is the pattern to
  follow for any future decision that spans this repo and a sibling
  (e.g. a change to how this repo calls one specific upstream's tools).

## Superseding an earlier ADR

Don't edit the old ADR's Decision section. Add a `## Status` line noting
`Superseded by ADR-XXXX` on the OLD one (a one-line patch), and open the
new ADR referencing it — see ADR-0006's own frontmatter/intro
(`Supersedes: [ADR 0005](./0005-rest-identity-static-bearer-scopes.md)`)
for the exact wording pattern, and note ADR-0005 itself was left
unedited, only marked superseded in the index table.

## Cross-repo decisions: this repo touches all nine upstream contexts by design

Because this repo is a Customer of nine bounded contexts at once (unlike
a typical sibling repo, which has at most one or two cross-context
neighbors), a decision that changes what this repo reads from an upstream
context (e.g. ADR 0009's `estimate_travel_distance` addition, which
depended on facility-layout's own ADR publishing that tool first)
should reference the upstream's ADR explicitly by number and repo, the
way ADR 0009's Context section does for facility-layout and
fulfillment-execution (their ADR numbers live in those repos, not here). Don't assume a reader here has also
read the upstream repo's docs site — restate the relevant fact plainly,
then cite it.

## After writing: regenerate and verify the docs build

```bash
cd docs
npm install
npm start    # local dev preview, or:
npm run build   # onBrokenLinks / onBrokenAnchors are both 'throw' — this
                 # WILL fail if the frontmatter/slug is wrong or a
                 # cross-reference link is broken
```

A broken ADR link or malformed frontmatter fails the build with a clear
Docusaurus error, not a silent 404 — always run this locally before
opening the PR. Note this repo has **no `docs-api-drift` CI job**
(there's no OpenAPI/AsyncAPI spec to drift from — see
`.claude/skills/how-to-add-a-rest-endpoint/SKILL.md`'s intro), so a broken docs build here is
only caught by whatever CI job does run the docs build, or by you running
it locally; don't skip this step assuming CI has your back the way it
might in a bounded-context repo.

Finally, add a row to `docs/docs/adr/index.md`'s table (# / Title /
Status) — this repo's index does not auto-generate that table from the
file list.
