---
name: domain-review
description: Ubiquitous-language drift review of a change against docs/docs/business-context/ubiquitous-language.md: renamed or invented terms, policy-layer purity leaks. Invoke explicitly: /domain-review [range].
disable-model-invocation: true
argument-hint: "[git range]"
---

Perform a ubiquitous-language drift review of the current changes (or
`$ARGUMENTS` if given), comparing new/changed code against
`docs/docs/business-context/ubiquitous-language.md` (terms this agent coins,
terms it borrows unredefined, words it deliberately does not use). This
repo owns no aggregate (ADR 0001): its "domain" is the pure decision-policy
layer in `internal/domain/policy/`.

Ubiquitous language drift is the quiet failure mode DDD is supposed to
prevent: code that technically works but silently renames, reshapes, or
duplicates a concept the domain-model doc already named — so future
readers can no longer map code to domain conversation.

## What to check, in priority order

1. **A new type/field/method that duplicates an existing domain concept
   under a different name.** If the domain-model doc already names a
   concept, a new calculation that computes the same thing under a
   different name is drift — even if the math is correct, it fragments
   the vocabulary. Flag it and point at the existing name.
2. **A domain type/method named in implementation terms instead of
   domain terms.** `internal/domain/` code should read like the ubiquitous
   language, not like database/HTTP vocabulary — a method called
   `UpdateRow` or `PatchState` where the domain-model doc would call the
   equivalent operation `Decide`/`Arbitrate`/`CorrelateTravelFactor` is drift, and the
   fitness-test suite won't catch this because it's a naming problem, not
   an import-direction problem.
3. **A policy rule or threshold in code that the docs don't mention, or
   vice versa.** If a new classification rule or threshold was added (see
   `policy/runtime_signals.go`, `policy/travel_factor.go`), the
   ubiquitous-language doc or the relevant ADR should be updated in the
   SAME PR — an undocumented rule is invisible to the next person and may
   be removed thinking it's dead code. Also flag any attempt to model an
   aggregate or an invariant here (ADR 0001: this agent owns neither).
4. **A vocabulary that should be closed but was implemented open (or
   vice versa).** `RebalanceAction`/`TaskType` in `internal/domain/policy`
   are deliberately closed, hand-mirrored copies of the upstream
   vocabulary, rejected when unknown (`ParseRebalanceAction`,
   `policy.ValidatePlan`). Check any new categorical field the same way and
   flag a mismatch in either direction — especially a silent default for
   an out-of-vocabulary value.
5. **A policy function that reaches outside itself.** Functions in
   `internal/domain/policy` are pure over facts the use case passes in; a
   change that makes one call out to a port, an upstream, or the LLM
   (the model is consulted behind `policy.Arbitrate`, never inside
   `Decide`) would be a real regression worth flagging even if it
   functionally still "works".
6. **New terminology introduced without updating the domain-model doc.**
   If new code introduces a genuinely new domain concept the doc doesn't
   yet name, that's not necessarily wrong — but the doc needs a new entry
   in the SAME PR, or the vocabulary silently forks between prose and
   code.

## Output format

For each finding: the term/concept involved, where it appears in the
domain-model doc (or "not yet documented"), where the drift appears in
code, and a one-sentence recommendation (rename, document, or confirm
it's an intentional new concept). If the changes introduce no new domain
concepts and use existing vocabulary correctly, say so plainly.

This command never modifies files. It is advisory input for the author
to act on, not a blocking gate.
