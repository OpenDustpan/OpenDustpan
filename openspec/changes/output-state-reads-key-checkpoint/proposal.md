---
version: 1
---

Issue: #1871.

## Problem

Two lookups cannot detect a delete, create, read, delete, and restore sequence inside one read. The read can return the outputs of the wrong stack. The case is rare and has low severity.

## Evidence

- pkg/stack/output_states.go exists on main. Proof: paths: pkg/stack/output_states.go exists true
- Main already refuses a recreated stack in output-state reads. Proof: TestOutputStatesRefuseARecreatedStack at pkg/stack/output_states_recreate_test.go:48
- Checkpoint reads already carry IDs in store conformance. Proof: testCheckpointReadsCarryIDs at pkg/store/storetest/updates.go:3262 (func testCheckpointReadsCarryIDs)

## Fix boundary

- Read the checkpoint and its owner stable ID in one store query keyed by the approved stable ID in Latest and Outputs.
- Add the NEW store method ReadCheckpointByStableID with storetest conformance and both SQL adapters.
- Keep the existing recreated-stack refusal as the denial path with the NEW error code stack.outputs_refused.

Out of scope:

- TFE-compatible and OpenTofu output paths
- Sharing-rule management and the grant model
- Backup and redaction of checkpoints beyond the owner stable ID column

## Check

- Add a failing output-state test for the ABA sequence in pkg/stack/output_states_recreate_test.go. Check: NEW TestOutputStatesRefusesAbaRecreateDuringRead fails on main and passes after the fix.
- Add storetest conformance for the NEW method ReadCheckpointByStableID in pkg/store/storetest/updates.go. Check: The NEW storetest cases fail on the adapters of main and pass after the fix.
- Implement the keyed checkpoint read in pkg/stack with the owner stable ID column and in the sqlite and postgres adapters. Check: `go test ./pkg/stack ./pkg/store/...` passes.
- Wire the keyed read through the existing route GET /api/dustpan/v1/namespaces/{}/units/{}/outputs with an access token. Check: A server test calls GET /api/dustpan/v1/namespaces/{}/units/{}/outputs with an access token and covers the 403 and 404 paths.
- Run the full checks and read the refusal message once for usability. A person's step. Check: `make check` reports zero findings.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main builds output-state readers through one constructor. (`pkg/stack/output_states.go:45 (func NewOutputStates)`)
- Main has a test that output states refuse a recreated stack. (`pkg/stack/output_states_recreate_test.go:48 (func TestOutputStatesRefuseARecreatedStack)`)
- Main checks that checkpoint reads carry IDs in store conformance. (`pkg/store/storetest/updates.go:3262 (func testCheckpointReadsCarryIDs)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does the current double lookup close the ABA case? No. Both lookups see the ID of A while the read returns the outputs of B, per the issue text.
- How does a stable-ID-keyed read close the ABA case? Yes, when each checkpoint row stores its owner stable ID. The keyed query filters on the approved stable ID, so it never returns a row owned by another ID; a missing row refuses the read.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
