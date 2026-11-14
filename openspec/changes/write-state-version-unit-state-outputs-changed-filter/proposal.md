Issue: #1215.

## Why

**Persona: developer.** Developer runs a stack that follows another stack. Each apply of the source starts a plan even when no shared output changed. The noise hides real changes. The server must start follower plans only when the root outputs changed.

The user path this change must make work:

1. **API.** Save a follows trigger with when outputs-changed on unit B that names unit A as source.
2. **CLI.** Apply unit A with the Pulumi CLI or OpenTofu, first with no output change, then with a changed root output.
3. **Docs page.** Read why the first apply started nothing and the second started a plan.
4. **E2e.** Run the e2e step with real CLI binaries over TLS on SQLite and PostgreSQL.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Save of a follows trigger with an unknown when value fails. | 400 with code trigger.bad-when and a message that names the allowed values applied and outputs-changed. | `curl -X PUT /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id}` |
| Apply of source with unchanged outputs starts no follower plan. | 200 with zero starts and an outcome of skipped with reason outputs-unchanged and the source version pair. | `curl /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id}` |

Evidence for the claims above:

- Dispatcher.Upstream is the entry point that decides follows starts. Proof: pkg/trigger/ports.go:136:// Outcome is the result of one trigger in a Dispatcher.Upstream call.
- A test helper drives Dispatcher.Upstream with an applied final version. Proof: pkg/trigger/triggertest/dispatcher.go:76:	out, err := h.Dispatcher.Upstream(t.Context(), trigger.Applied{Unit: a, Version: applied})
- Issue #1215 asks for the outputs-changed value, the digest at final write, and the compare in Dispatcher.Upstream. Proof: Part of #909. Outcome: A `follows` trigger can start a plan only when the root outputs of the source changed since its last apply.

## What Changes

- Root-outputs digest written at each final state version, with no secret value in the digest or in any answer
- Field value outputs-changed for the when field of a follows trigger on the trigger routes
- Compare in Dispatcher.Upstream that skips starts when the digest matches the immediate prior final digest
- Docs page and e2e step with real CLI binaries that applies twice, once with a changed output

Out of scope:

- Final state versions, current pointers and write paths
- Follows trigger cycle check, caps and start dispatch
- Schedule and review-request-push triggers
- Output sharing rules and reads

Depends on:

- write-state-version-unit-state-follows-trigger-start
- write-state-version-unit-state
- `write-state-version-unit-state-trigger-caps-depth` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- A skipped start surprises: the follower page must show skipped with reason outputs-unchanged and the source version pair.
- Two words applied and outputs-changed cost a read; the docs page shows one example of each.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 267: Run triggers: three kinds, a plan only, started through the job service** (not owner-tagged).

- Decisions that apply: 13 (current).
- Changed by: ADR 273 changes decision 14; ADR 299 changes decisions 14 and 19; ADR 307 extends decision 8; ADR 310 changes decision 22; ADR 338 changes decision 14. Chain to the end: ADR 273, ADR 299, ADR 307, ADR 310, ADR 338, ADR 286, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 273, 299, 307, 310, 338.
- Quote: "A `follows` trigger has a field `when` (question 2)."
- Quote: "The value `outputs-changed` comes in a later slice."
- Effect: Implements the later slice: the value outputs-changed of the field when and the compare before start.
- Conflict: none.

**ADR 213: Job tokens and outputs, plans with no change, the start policy input, bulk calls, the options action, forge connections and locks** (not owner-tagged).

- Decisions that apply: 1 (changed).
- Changed by: ADR 241 changes decision 10; ADR 244 changes decision 10; ADR 265 changes decision 9; ADR 267 extends decision 1; ADR 271 changes decisions 7 and 8; ADR 273 changes decision 1; ADR 273 extends; ADR 286 changes; ADR 300 changes decision 2; ADR 300 changes decision 10; ADR 309 extends decision 5; ADR 353 changes decision 2. Chain to the end: ADR 241, ADR 244, ADR 265, ADR 267, ADR 271, ADR 273, ADR 286, ADR 300, ADR 309, ADR 353, ADR 248, ADR 313, ADR 343, ADR 299, ADR 307, ADR 310, ADR 338, ADR 303, ADR 336, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 241, 244, 265, 267, 271, 273, 286, 300, 309, 353.
- Quote: "**A job reads the outputs of another unit on two paths, each with two checks.**"
- Effect: Keeps the digest inside the server and starts reads through the existing sharing checks; the digest grants no read.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "Docs in the same pull request."
- Effect: Ships the docs page for the when value in the same pull request.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (wrong claim): proposal.md: The open questions section says the change is not spec-ready because the route POST /api/dustpan/v1/namespaces/acme/units is not in the API description. The usability table and the error table still use POST .../units/<unit-id>/triggers and GET .../units/<unit-id>/triggers. The change must fix these routes and pass the code check before the gate. Resolution: answered: Replaced angle-bracket IDs with brace placeholders so each command names an existing route from the coverage matrix.
- R1.2 (scope): tasks.md 2.2 wires the when value through trigger create and replace, and the proposal lists follows trigger create and update as out of scope. The change does work that belongs to the follows-trigger part, or the scope is wrong. Resolution: answered: Narrowed out_of_scope to cycle check, caps and dispatch; the when value validation is in what_changes and tasks.
- R1.3 (open question): specs/run-triggers/spec.md: The spec does not say which final digest is the prior one: the previous final version of the source, or the version of the last apply that started the follower. The issue says 'since its last apply'. The compare cannot be built without this answer. Resolution: answered: Spec now defines prior as the immediate previous final version of the same source.
- R1.4 (open question): design.md Decision 1 replaces secret values with a fixed marker, so a change in a secret output does not change the digest. The spec scenario 'the digest changes or stays' does not say whether a secret-only change starts followers. No ADR answers this. Resolution: answered: Spec now states a secret-only change does not change the digest and starts nothing.
- R1.5 (missing scenario): design.md Decision 2 says a missing prior digest counts as changed, and the risk section names backfilled versions. The spec, the tasks and the test list have no scenario for a missing prior digest. Resolution: answered: Added missing-prior-digest scenario that counts as changed.
- R1.6 (missing scenario): proposal.md names the answer 'skipped with reason outputs-unchanged' and the source version pair, but the spec has no requirement for the outcome vocabulary, the reason values or the response shape of a skipped start. Resolution: answered: Added outcome vocabulary requirement with reason outputs-unchanged and the version pair.
- R1.7 (failure mode): The change does not cover two applies of the source at the same time, or a restart between the final write and Dispatcher.Upstream. A follower may compare against a stale digest, or a skipped start may never be retried. Resolution: answered: Design risks now state serialized source writes, ordered committed reads, and replay from the stored final version.
- R1.8 (adr conflict): proposal.md ADR check says ADR 213 decision 1 is 'Conflict: none' and that the digest uses 'the same boundary the job read checks use'. The quoted decision 1 says nothing about root outputs or digests. The claim is not supported by the quoted text. Resolution: answered: Rewrote ADR 213 effect to claim only server-side digest with no read granted, as the quote supports.
- R1.9 (missing scenario): The spec and tasks have no scenario for OpenTofu and Pulumi shape drift, which the design names as a risk. The e2e check covers only one apply path on SQLite and PostgreSQL over TLS as stated in the proposal, not the canonicalization of both tools. Resolution: answered: Added canonicalization scenarios for Pulumi and OpenTofu shapes in the digest spec.

## Open questions

none

Questions considered and answered:

- Should outputs-changed ship now or stay a later slice? ADR 267 decision 13 answers it: default applied, outputs-changed comes in a later slice; this change is that slice.
- What does the server compare to decide that outputs changed? Issue #1215 answers it: store a digest of the root outputs at final write; the digest never leaves the server and holds no value.
- Does an outputs-changed start bypass policy checks? ADR 267 decision 2 answers it: gates, approvals, freezes, locks and open-job limits apply to the job as to any job; no new path is added.
- Which digest is prior, and does a secret-only change count as changed? The prior digest is the digest of the immediate previous final version of the source unit; secret values are replaced by a fixed marker before the digest, so a secret-only change does not change the digest and starts nothing.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
