Issue: #1164.

## Why

**Persona: developer.** As a Developer I run the Pulumi CLI and OpenTofu against the server and I need plan and apply phases to see the true state version, so stale plans stop and write-back jobs close.

The user path this change must make work:

1. **CLI.** Run a stack update with the Pulumi CLI against the server
2. **API.** Push state with OpenTofu against the TFE-compatible API
3. **API.** Read the current and final state versions of the unit through the tool-neutral API
4. **Docs page.** Follow the docs page for state versions

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| State write fails and writes no version row. | State write failed and wrote no version row; run `opendustpan doctor` and push state again. | `opendustpan doctor` |
| Final write on a store built without an observer is refused. | Final state write refused: store has no version observer; run `opendustpan doctor` and fix the server wiring. | `opendustpan doctor` |

Evidence for the claims above:

- No non-test code references the unit_state_versions table yet. Proof: rg -n -F --glob '!*_test.go' 'unit_state_versions' pkg internal cmd matched 0 files.
- No non-test code references the unit_final_versions table yet. Proof: rg -n -F --glob '!*_test.go' 'unit_final_versions' pkg internal cmd matched 0 files.
- Only the self-service port declares ObserveStateVersion today. Proof: rg -n -F --glob '!*_test.go' 'ObserveStateVersion' pkg internal cmd matched 6 lines, including internal/selfservice/ports.go:81 ObserveStateVersion and internal/selfservice/selfservicetest/requests.go:236 calls.

## What Changes

- Write the current state version of the unit in each Pulumi checkpoint transaction
- Write the current state version of the unit in each OpenTofu state version transaction
- Apply the final-version observe rule in the same transaction on final writes

Out of scope:

- Start follows plans from final versions
- Follower caps, depth stop, and outputs-changed filter
- Stale-plan detection in plan and apply phases beyond recording versions

## Usability

Where the path still costs the person time or surprises them:

- A developer sees no new step: push state as before and the server records versions without extra flags.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 100: A shared job model on durable operations** (not owner-tagged).

- Decisions that apply: 1 (current), 15 (changed).
- Changed by: ADR 110 extends decision 17; ADR 125 changes; ADR 128 changes decision 17; ADR 130 extends; ADR 135 extends decision 5; ADR 139 extends decision 5; ADR 144 changes decision 20; ADR 145 extends; ADR 148 extends decision 4; ADR 190 changes decision 4; ADR 213 changes decision 20; ADR 258 extends decision 4; ADR 263 extends decision 4; ADR 286 changes; ADR 299 changes decisions 12 and 15; ADR 304 changes decision 2; ADR 304 changes decision 20; ADR 314 extends; ADR 353 extends decision 4. Chain to the end: ADR 110, ADR 125, ADR 128, ADR 130, ADR 135, ADR 139, ADR 144, ADR 145, ADR 148, ADR 190, ADR 213, ADR 258, ADR 263, ADR 286, ADR 299, ADR 304, ADR 314, ADR 353, ADR 210, ADR 218, ADR 222, ADR 225, ADR 305, ADR 308, ADR 354, ADR 227, ADR 164, ADR 300, ADR 343, ADR 199, ADR 220, ADR 226, ADR 282, ADR 149, ADR 224, ADR 259, ADR 146, ADR 197, ADR 203, ADR 204, ADR 234, ADR 241, ADR 248, ADR 262, ADR 315, ADR 334.
- Current rule: this ADR, with ADR 110, 125, 128, 130, 135, 139, 144, 145, 148, 190, 213, 258, 263, 286, 299, 304, 314, 353.
- Quote: "A final state version closes a write-back job."
- Quote: "No tool package imports it."
- Effect: Follows the wiring split and the observe rule: tool stores write versions and call the observe rule in the same transaction through injected wiring, with no tool import of job.
- Conflict: none.

**ADR 150: Unit history: blame and point-in-time views** (not owner-tagged).

- Decisions that apply: 2 (current).
- Changed by: ADR 154 changes decisions 14, 15 and 16; ADR 154 extends; ADR 199 changes decision 1; ADR 210 changes; ADR 242 extends decision 3. Chain to the end: ADR 154, ADR 199, ADR 210, ADR 242, ADR 238, ADR 286, ADR 310, ADR 247, ADR 315, ADR 356.
- Current rule: this ADR, with ADR 154, 199, 210, 242.
- Quote: "For a workspace, it is the current state version."
- Effect: Uses the latest state item rule to define the current version for each tool.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): proposal.md states the spec-ready gate is not met: the open questions section does not read 'none' because a code check fails on the command 'opendustpan logs', so the change may not be implemented until this is resolved. Resolution: answered: Fixed errors[0] to use `opendustpan doctor`, an existing command; open questions reads none.
- R1.2 (missing scenario): The error row promises a 500 'state write failed, versions not written' response, but no spec requirement or scenario covers a failed state write that writes no version row, or the rollback of the state write when observe fails. Resolution: answered: Added rollback scenario to final-version requirement: a failed observe rolls back the state write.
- R1.3 (missing scenario): No scenario covers a state write by a store constructed without an observer (NewStoreWithObserver absent), even though design.md says such constructors must refuse final writes. The refusal behavior and its error are unspecified. Resolution: answered: Added observer-wiring requirement with refusal scenario for a store built without an observer.
- R1.4 (failure mode): Design decision 2 requires observe in the fenced transaction, but no requirement covers a fence rejection or a concurrent write that moves the current version between read and write, so the current version could be set to a stale value. Resolution: answered: Fenced-transaction wording added to both version requirements; fence rejection rolls back the write.
- R1.5 (adr conflict): ADR 100 decision 15 reads 'The store adapter applies the rule of Store.ObserveStateVersion in the transaction of the state write'; the proposal's Questions section cites it correctly, but tasks 2.2 wires the call through internal/jobwiring, which the design places in the tool stores via injected observer; the owner of the wiring location is not decided. Resolution: answered: Clarified design and tasks: the observer lives in each tool store and the server wiring injects it; no separate jobwiring store.
- R1.6 (missing scenario): No scenario covers a restart or a retried push after a state write that committed the version but not the observe call, or a duplicate final write for an already closed write-back job. Resolution: answered: Added idempotent duplicate-final scenario to the final-version requirement.
- R1.7 (scope): Task 3.1 asks for an e2e step with the real CLIs over TLS on SQLite and PostgreSQL, but the e2e check names only E2E_FOCUS=state-versions; the test names for scenarios of the second requirement are not mapped to a failing test on main. Resolution: answered: Mapped each scenario to a failing store test in tasks and kept the real-CLI e2e check.
- R1.8 (wrong claim): ADR 100 decision 1 is quoted as forbidding tool packages from importing job, but the proposal cites it as 'ADR 100 decision 1 forbids'; the quoted text in the ADR check block is a different sentence ('No tool package imports it.'), so the citation mixes two lines. Resolution: answered: Reworded considered answer to quote decision 1 exactly: no tool package imports job.
- R1.9 (open question): The design does not state how the current version of a unit is identified for a Pulumi stack versus an OpenTofu workspace when a unit is renamed or removed and made again; the stable unit ID rule of project.md is not applied in the spec. Resolution: answered: Added stable-unit-ID requirement: versions are keyed by stable unit ID across rename and remake.

## Open questions

none

Questions considered and answered:

- Does a tool package import job to report versions? No. ADR 100 decision 1 states no tool package imports job; the internal wiring of the server connects them, as the issue states.
- Where does the final-version rule run? No. ADR 100 decision 15 states the store adapter applies the rule in the transaction of the state write.
- What happens on a retried or duplicate final write? A duplicate final write is idempotent: the second observe call closes nothing and changes no version. No retry queue is added in this part.
- How are versions keyed across rename and remake? Versions are keyed by the stable unit ID, so a rename keeps versions and a removed unit made again starts with no versions.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
