---
version: 1
---

Issue: #2386.

## Problem

An Embedder who writes a third-party store needs the conformance suite to lock the cancel-before-step order, so a canceled operation never reports a step error instead of ErrCanceled.

## Evidence

- The shared store conformance suite lives at pkg/store/storetest. Proof: pkg/store/storetest exists
- The PostgreSQL durable adapter funnels step checks through checkStepRow. Proof: pkg/store/postgres/durable.go:345 (func checkStepRow)
- The PostgreSQL adapter has peekStep with the rules and the order of checkStep, called from the restore operation path. Proof: pkg/store/postgres/durable.go:667 (func peekStep)
- The SQLite durable adapter has its own checkStep for the RecordStep rules. Proof: pkg/store/sqlite/durable.go:805 (func checkStep)
- No existing test failed when the reviewer swapped the cancel and step order. Proof: issue #2386 states that swapping the two checks in checkStepRow fails no existing test

## Fix boundary

- Add a storetest case: a live operation with CancelRequested set and one step recorded expects ErrCanceled on the RecordStep path and on the peek path.

Out of scope:

- Fix of adapter behavior beyond the new conformance case
- Rules for canceled operations outside step paths
- A cancel that arrives between the step check and the write, and a cancel on a finished operation

## Check

- Find the existing Store port calls that request a cancel and record a step, and the way the suite runs on PostgreSQL. Write the failing case in NEW file `pkg/store/storetest/canceled_step_test.go`. Check: `TestCanceledOperationWithRecordedStepReturnsCanceled` covers the same-step retry and the differing-step fence on the RecordStep path.
- Run the case on SQLite and on PostgreSQL. Swap the order of the checks in `pkg/store/postgres/durable.go:345 (func checkStepRow)` and confirm that the case fails, then revert. Check: `go test ./pkg/store/sqlite/ -run TestCanceledOperationWithRecordedStepReturnsCanceled -count=1` passes, and the PostgreSQL store suite passes on the correct order.
- Run the full store suites on both dialects and make check with zero findings. Check: `go test ./pkg/store/sqlite/... -count=1` and `make check` report zero findings. Confirmed by: CI (make check, store suites on SQLite and PostgreSQL).

Depends on:

- restore-loses-its-own-claim
- `restore-loses-its-own-claim` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The PostgreSQL adapter checks an operation row for a step through checkStepRow. (`pkg/store/postgres/durable.go:345 (func checkStepRow)`)
- The PostgreSQL adapter has peekStep with the rules and the order of checkStep. (`pkg/store/postgres/durable.go:667 (func peekStep)`)
- The SQLite adapter has checkStep for the RecordStep rules. (`pkg/store/sqlite/durable.go:805 (func checkStep)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does an ADR decide the order of the cancel check and the step check? No. The pack names no ADR or RFC. The order rule comes from issue #2386 and the existing sentinels in pkg/durable.
- Does the case cover both step paths? Yes. Issue #2386 asks for both checkStep and peekStep on SQLite and on PostgreSQL, so the case covers the RecordStep path and the restore peek path.
- Through which calls does the case set CancelRequested, record a step, and reach peekStep? The suite uses the existing Store port calls that request a cancel and record a step. The peek path is reached through the existing restore operation path that calls peekStep at pkg/store/postgres/restore_operation.go:871. No new helper and no new public method. The exact restore operation calls are confirmed in the first task before the case is written, and the change builds after restore-loses-its-own-claim so the peek wiring matches.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
