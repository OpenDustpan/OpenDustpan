---
version: 2
---

Issue: #1164.

## Why

**Persona: developer.** As a Developer I run the Pulumi CLI and OpenTofu against the server and I need plan and apply phases to see the true state version, so stale plans stop and write-back jobs close.

The user path this change must make work:

1. **CLI.** Run a stack update with the Pulumi CLI against the server
2. **API.** Push state with OpenTofu against the TFE-compatible API
3. **API.** Read the current state through the existing Pulumi state routes and the existing workspace state-version routes
4. **Docs page.** Follow the docs page for state versions

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| State write fails and writes no version row. | State write failed and wrote no version row. Run `opendustpan doctor` and push state again. | `opendustpan doctor` |
| Final write on a store built without an observer is refused. | Final state write refused: store has no version observer. Run `opendustpan doctor` and fix the server wiring. | `opendustpan doctor` |

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
- New read routes for versions; reads use existing tool state routes

## Usability

Where the path still costs the person time or surprises them:

- A developer sees no new step: push state as before and the server records versions without extra flags.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- ObserveStateVersion port exists for final state versions (`internal/selfservice/ports.go:81`)
- Final versions last-final behavior has a test (`pkg/dustpan/triggers_versions_internal_test.go:36`)
- OpenTofu test helper exists for the e2e path (`e2e/tool.go:110`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 100: A shared job model on durable operations** (not owner-tagged).

- Decisions that apply: 1 (current), 15 (changed).
- Changed by: ADR 110 extends decision 17; ADR 125 changes; ADR 128 changes decision 17; ADR 130 extends; ADR 135 extends decision 5; ADR 139 extends decision 5; ADR 144 changes decision 20; ADR 145 extends; ADR 148 extends decision 4; ADR 190 changes decision 4; ADR 213 changes decision 20; ADR 258 extends decision 4; ADR 263 extends decision 4; ADR 286 changes; ADR 299 changes decisions 12 and 15; ADR 304 changes decision 2; ADR 304 changes decision 20; ADR 314 extends; ADR 353 extends decision 4. Chain to the end: ADR 110, ADR 125, ADR 128, ADR 130, ADR 135, ADR 139, ADR 144, ADR 145, ADR 148, ADR 190, ADR 213, ADR 258, ADR 263, ADR 286, ADR 299, ADR 304, ADR 314, ADR 353, ADR 210, ADR 218, ADR 222, ADR 225, ADR 305, ADR 308, ADR 354, ADR 227, ADR 164, ADR 300, ADR 343, ADR 199, ADR 220, ADR 226, ADR 282, ADR 149, ADR 224, ADR 259, ADR 146, ADR 197, ADR 203, ADR 204, ADR 234, ADR 241, ADR 248, ADR 262, ADR 315, ADR 334.
- Current rule: this ADR, with ADR 110, 125, 128, 130, 135, 139, 144, 145, 148, 190, 213, 258, 263, 286, 299, 304, 314, 353.
- Quote: "A final state version closes a write-back job."
- Quote: "No tool package imports it."
- Effect: Follows the split and the observe rule. Tool packages do not import job. The internal wiring maps each tool version to job.StateVersion and runs the job-store observe rule in the same fenced transaction.
- Conflict: none.

**ADR 150: Unit history: blame and point-in-time views** (not owner-tagged).

- Decisions that apply: 2 (current), 7 (current), 9 (current).
- Changed by: ADR 154 changes decisions 14, 15 and 16; ADR 154 extends; ADR 199 changes decision 1; ADR 210 changes; ADR 242 extends decision 3. Chain to the end: ADR 154, ADR 199, ADR 210, ADR 242, ADR 238, ADR 286, ADR 310, ADR 247, ADR 315, ADR 356.
- Current rule: this ADR, with ADR 154, 199, 210, 242.
- Quote: "For a workspace, it is the current state version."
- Quote: "A forced write and a restore (ADR 28) are state items like any other."
- Quote: "The server cannot read such a state (ADR 41 decision 8)."
- Effect: Uses the latest-item rule for current versions, treats forced writes and restores as state items, and treats client-encrypted OpenTofu state as opaque.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does a tool package import job to report versions? No. ADR 100 decision 1 says no tool package imports job. Each tool store defines a local Observer port with plain fields. The internal wiring implements it and builds job.StateVersion outside the tool package.
- Which store runs the final-version rule and how is the transaction shared? The job-store rule runs in the same fenced transaction. The tool transaction calls the injected observer. The wiring adapter delegates to the job-store observe logic with the same transaction handle. No second commit occurs.
- What makes a state write final for each tool? A Pulumi checkpoint of a completed update is final. A preview or a running update is not final. An OpenTofu state version at finalize is final. A pending or superseded upload is not final.
- How do concurrent, forced, opaque, redacted, and purged writes behave? The fenced transaction orders versions by commit order and rejects a stale write. A fence loss aborts the write. A duplicate final version ID is idempotent. Opaque, redacted, purged, and checkpoint-less writes keep defined version values.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
