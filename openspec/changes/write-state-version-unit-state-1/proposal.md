---
version: 3
---

Issue: #1164.

## Why

**Persona: developer.** As a Developer I run the Pulumi CLI and OpenTofu against the server. Plan and apply phases must see the true state version, so a stale plan stops and a write-back job closes. Main already writes both versions in the transaction of each state write. Nothing proves it with the real CLI binaries, and no docs page explains it.

The user path this change must make work:

1. **CLI.** Run a stack update with the Pulumi CLI against the server
2. **CLI.** Run `tofu apply` against the TFE-compatible API
3. **API.** Read the state through the existing Pulumi export route and the existing workspace current-state-version route
4. **E2e.** The e2e step runs both real CLIs and checks the versions on those routes
5. **Docs page.** Read the docs page on state versions

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A state write fails, so no version row is written | State write failed. The state and the version rows were not written. Push the state again. | `opendustpan doctor` |

Evidence for the claims above:

- A caller of the observe rule exists only in the self-service port and its test suite in non-test code. Proof: rg -n -F --glob '!*_test.go' 'ObserveStateVersion' pkg internal cmd matched 6 lines: internal/selfservice/ports.go:81 and internal/selfservice/selfservicetest/requests.go:236.
- Main writes both versions and closes awaiting jobs inside the fenced transaction of a state write, in publishUnitState. Proof: PM decision PM.1 names pkg/store/sqlite/unit_state.go:97 (func publishUnitState, same in pkg/store/postgres) and jobstore.CloseAwaiting.
- A guard test makes publishUnitState the only writer of unit_state. Proof: PM decision PM.1 names pkg/store/sqlite/unit_state_queries_test.go:31-51 (func unitStateWriteQueries).

## What Changes

- Add regression tests that the current version is set on each state write of each tool
- Add regression tests that a final write sets the final version and closes awaiting jobs
- Add regression tests for an idempotent duplicate final write and for a rollback
- Add an e2e step with the real Pulumi CLI and OpenTofu binaries
- Add the docs page on state versions

Out of scope:

- Start follows plans from final versions
- Follower caps, depth stop and the outputs-changed filter
- Any observer port in pkg/stack or pkg/workspace
- New read routes for versions

## Usability

Where the path still costs the person time or surprises them:

- A developer pushes state as before and the server records the versions with no extra flag.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- publishUnitState writes both versions and calls CloseAwaiting in one fenced transaction (`pkg/store/sqlite/unit_state.go:97`)
- A guard test makes publishUnitState the only writer of unit_state (`pkg/store/sqlite/unit_state_queries_test.go:31`)
- The last-final behavior has a test (`pkg/dustpan/triggers_versions_internal_test.go:36`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 100: A shared job model on durable operations** (not owner-tagged).

- Decisions that apply: 1 (current), 15 (changed).
- Changed by: ADR 110 extends decision 17; ADR 125 changes; ADR 128 changes decision 17; ADR 130 extends; ADR 135 extends decision 5; ADR 139 extends decision 5; ADR 144 changes decision 20; ADR 145 extends; ADR 148 extends decision 4; ADR 190 changes decision 4; ADR 213 changes decision 20; ADR 258 extends decision 4; ADR 263 extends decision 4; ADR 286 changes; ADR 299 changes decisions 12 and 15; ADR 304 changes decision 2; ADR 304 changes decision 20; ADR 314 extends; ADR 353 extends decision 4. Chain to the end: ADR 110, ADR 125, ADR 128, ADR 130, ADR 135, ADR 139, ADR 144, ADR 145, ADR 148, ADR 190, ADR 213, ADR 258, ADR 263, ADR 286, ADR 299, ADR 304, ADR 314, ADR 353, ADR 210, ADR 218, ADR 222, ADR 225, ADR 305, ADR 308, ADR 354, ADR 227, ADR 164, ADR 300, ADR 343, ADR 199, ADR 220, ADR 226, ADR 282, ADR 149, ADR 224, ADR 259, ADR 146, ADR 197, ADR 203, ADR 204, ADR 234, ADR 241, ADR 248, ADR 262, ADR 315, ADR 334.
- Current rule: this ADR, with ADR 110, 125, 128, 130, 135, 139, 144, 145, 148, 190, 213, 258, 263, 286, 299, 304, 314, 353.
- Quote: "No tool package imports it."
- Quote: "The store adapter applies the rule of `Store.ObserveStateVersion` in the transaction"
- Effect: Keeps the existing store-adapter write in the transaction. No tool package imports job and no observer port is added.
- Conflict: none.

**ADR 150: Unit history: blame and point-in-time views** (not owner-tagged).

- Decisions that apply: 2 (current), 9 (current).
- Changed by: ADR 154 changes decisions 14, 15 and 16; ADR 154 extends; ADR 199 changes decision 1; ADR 210 changes; ADR 242 extends decision 3. Chain to the end: ADR 154, ADR 199, ADR 210, ADR 242, ADR 238, ADR 286, ADR 310, ADR 247, ADR 315, ADR 356.
- Current rule: this ADR, with ADR 154, 199, 210, 242.
- Quote: "For a workspace, it is the current state version."
- Quote: "The server cannot read such a state (ADR 41 decision 8)."
- Effect: Tests use the latest-item rule and keep a client-encrypted state opaque.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does the change add an injected observer port to the tool packages? No. ADR 100 decision 1 keeps tool packages free of job. Main writes the versions in the store adapter, so no port is needed.
- Is a new write path needed? No. publishUnitState is the only writer of unit_state and a guard test holds that.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
