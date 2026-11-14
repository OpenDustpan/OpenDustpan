---
version: 7
---

Issue: #1255.

## Why

**Persona: operator.** Operator runs an installation where OpenTofu lock requests write one state.write decision record for each evaluation and no job prunes them. The operator needs a port method that deletes records past the audit retention, so a later sweep can bound disk use.

The user path this change must make work:

1. **SDK.** Embedder calls the pruneExpired method of the decision record port with a cutoff time and a batch size, and gets the IDs that the method deleted.
2. **API.** Namespace administrator reads state.write decision records through the existing record read route. Records past the cutoff disappear after a later sweep calls the method.
3. **Docs page.** Operator reads the docs page for decision record retention. The page says that the schedule comes with the follow-up change.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The decision record store cannot take the write during a state write. | The server answers 503 with code gate.unavailable. Check server health, then retry the state write. | `opendustpan status` |

Evidence for the claims above:

- Each OpenTofu lock request is one evaluation, and no job prunes the records. Proof: Issue 1255: each OpenTofu lock request is one evaluation, and -lock-timeout retries a lock many times. So the records grow with every lock request, and no job prunes them.
- Each evaluation writes one decision record on main. Proof: pkg/gate/statewrite_test.go:606 (TestEachEvaluationWritesOneRecord).
- A retry against a held lock writes no record. Proof: Issue 1255 note: the TFE lock route asks the guard only when the lock is free.
- The state.write guard is the adapter that the HTTP adapters use. Proof: internal/api/stategate/stategate.go:30 (the interface that *gate.Service implements).

## What Changes

- Add the pruneExpired method to the decision record port in pkg/gate/store.go, with a conformance test for each adapter.
- Keep one decision record for each state.write evaluation, also when no gate exists.
- Keep the held-lock behavior: a retry on a held workspace asks no guard and writes no record.

Out of scope:

- The sweep home, the trigger, the cadence and the store lease: the stacked follow-up retention-sweep-schedule-lease.
- Change of the state.write gate set or fact schema.
- Change of the audit retention value itself.
- Console views for decision records.

## Usability

Where the path still costs the person time or surprises them:

- The docs page says that the prune schedule comes with the follow-up change, so an operator does not expect a prune yet.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Each evaluation writes one decision record. (`pkg/gate/statewrite_test.go:606 (TestEachEvaluationWritesOneRecord)`)
- The stategate adapter guards the state.write decision point for HTTP adapters that write state. (`internal/api/stategate/stategate.go:30`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 222: One model for access, gates, approvals and facts** (owner-tagged: decision 3).

- Decisions that apply: 3 (current).
- Changed by: ADR 286 changes; ADR 335 extends decision 7; ADR 351 extends; ADR 354 changes decision 5. Chain to the end: ADR 286, ADR 335, ADR 351, ADR 354.
- Current rule: this ADR, with ADR 286, 335, 351, 354.
- Quote: "One decision record for each evaluation (owner). The record explains every block, every wait and every warn (ADR 229)."
- Effect: The change keeps one record for each evaluation, also when no gate exists. Old records leave through the port method.
- Conflict: none.

**ADR 229: Decision records, the simulate route and starter packs** (not owner-tagged).

- Decisions that apply: 5 (current).
- Changed by: ADR 286 changes. Chain to the end: ADR 286.
- Current rule: this ADR, with ADR 286.
- Quote: "A record of `state.write` or of an access limit lives for the audit retention (ADR 68)."
- Effect: The port method deletes a record of state.write only after the audit retention passes.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- May an evaluation with no gate and no limit skip its record? No. ADR 222 decision 3 (owner) needs one record for each evaluation. The volume fix is the prune.
- How long does a state.write record live? ADR 229 decision 5: for the audit retention. The method takes the cutoff from its caller and never reads the setting itself.
- Where do the schedule and the lease live? In the stacked follow-up retention-sweep-schedule-lease, by the PM decision. This change only adds the port method.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
