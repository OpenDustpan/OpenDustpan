---
version: 2
---

Issue: #1511.

## Problem

An operator runs a restore on one installation and watches apply. The swap can wait behind a backup snapshot read with maintenance mode on and no log line, so the operator cannot tell what blocks the restore.

## Evidence

- The swap lock timeout is 100 ms. Proof: pkg/store/postgres/maintenance_revocations.go:239:	swapLockTimeout      = 100 * time.Millisecond
- A swap busy error helper exists. Proof: pkg/store/postgres/maintenance_revocations.go:230:func errSwapLockBusy(ctx context.Context) error {
- The postgres StateAt maps its read errors through readConflict. Proof: pkg/store/postgres/state_at.go:25:		return stack.StateAt{}, fmt.Errorf("begin read transaction: %w", readConflict(err))
- No non-test code names checkSwapDoesNotQueueReads, so the bound lives in a test file. Proof: rg -n -F --glob '!*_test.go' 'checkSwapDoesNotQueueReads' pkg internal cmd matches 0

## Fix boundary

- Log one warning per holder after 500 ms when restore apply waits for the swap lock behind a snapshot read. Each swap attempt keeps the 100 ms timeout.
- Tighten the swap non-queue test bound to 500 ms.
- Read QueryUnits labels with array_agg in the page query, with no extra transaction.
- Route StateAt and WorkspaceStateAt begin and reads through readConflict to ErrUnavailable.

Out of scope:

- Refusal of a new backup during restore apply.
- A total maximum wait for the swap.
- Change of inventory page shape or filter semantics.

## Check

- Add a test in NEW file pkg/store/postgres/swap_wait_test.go that the swap logs one warning after 500 ms and none before 500 ms. Check: `go test ./pkg/store/postgres -run TestSwapWaitLogsOnce -count=1` fails on main
- Set the swap non-queue bound to 500 ms in the test that calls checkSwapDoesNotQueueReads, in NEW file pkg/store/postgres/swap_bound_test.go. Check: `go test ./pkg/store/postgres -run TestSwapNonQueueBound -count=1` fails on main
- Add a test in NEW file pkg/store/postgres/inventory_query_labels_test.go of QueryUnits labels (pkg/store/postgres/inventory_query.go, func Store.QueryUnits) in one statement with empty and ordered labels. Check: `go test ./pkg/store/postgres -run TestQueryUnitsLabelsOneStatement -count=1` fails on main
- Add a test in NEW file pkg/store/postgres/state_at_conflict_test.go that StateAt (pkg/store/postgres/state_at.go:20) and WorkspaceStateAt begin and read conflicts map to ErrUnavailable. Check: `go test ./pkg/store/postgres -run TestStateAtConflictMapsUnavailable -count=1` fails on main
- Implement the four changes and add the swap wait to the high-availability docs page. The change adds no route. The existing restore routes keep their access token authorization. Check: `make check` with zero findings

Depends on:

- track-medium-low-items-several-durable-fenced-restore
- database-outage-503-shape

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The swap lock timeout is 100 ms. (`pkg/store/postgres/maintenance_revocations.go:239`)
- The postgres store has a readConflict function that maps read conflicts. (`pkg/store/postgres/postgres.go:1323 (func readConflict)`)
- The postgres store implements the unit page query. (`pkg/store/postgres/inventory_query.go:51 (func Store.QueryUnits)`)
- StateAt begins a snapshot read transaction. (`pkg/store/postgres/state_at.go:20 (func Store.StateAt)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 280: Milestone 7: high availability on one database** (owner-tagged (all decisions)).

- Decisions that apply: 4 (extended), 7 (current).
- Changed by: ADR 281 extends decision 5; ADR 286 changes; ADR 298 extends decision 4; ADR 302 extends. Chain to the end: ADR 281, ADR 286, ADR 298, ADR 302, ADR 340, ADR 337, ADR 368, ADR 369, ADR 370.
- Current rule: this ADR, with ADR 281, 286, 298, 302.
- Quote: "503 with `Retry-After` when the store is not available (owner)."
- Quote: "The backup takes no lock and never waits."
- Effect: StateAt conflict mapping follows decision 4. This change adds no backup lock and no backup wait, so it follows decision 7. Refusal of a backup during restore apply is out of scope.
- Conflict: none.

**ADR 298: PostgreSQL failover readiness** (not owner-tagged).

- Decisions that apply: 2 (current), 8 (current).
- Changed by: ADR 337 changes decision 9. Chain to the end: ADR 337.
- Current rule: this ADR, with ADR 337.
- Quote: "Requests fail fast with 503."
- Quote: "A database transaction commits all its rows or none."
- Effect: Snapshot-read conflicts map to unavailable so the request fails fast with 503. Snapshot reads commit or roll back as one transaction with no resend.
- Conflict: none.

**ADR 72: Scale out on one database with durable operations** (not owner-tagged).

- Decisions that apply: 5 (extended).
- Changed by: ADR 73 changes decision 7; ADR 75 extends decision 10; ADR 103 changes decision 6; ADR 126 changes decision 6; ADR 151 extends decision 5; ADR 166 extends decision 1; ADR 240 extends decision 6; ADR 249 extends decision 11; ADR 280 extends; ADR 298 extends. Chain to the end: ADR 73, ADR 75, ADR 103, ADR 126, ADR 151, ADR 166, ADR 240, ADR 249, ADR 280, ADR 298, ADR 322, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 146, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299, ADR 129, ADR 154, ADR 210, ADR 217, ADR 220, ADR 286, ADR 297, ADR 354, ADR 281, ADR 302, ADR 337, ADR 325, ADR 326, ADR 362.
- Current rule: this ADR, with ADR 73, 75, 103, 126, 151, 166, 240, 249, 280, 298.
- Quote: "Control tables stay outside the restore swap."
- Effect: The swap keeps its control-table semantics. This change only adds a log line and narrows reads.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. May a new backup be refused while a restore is in apply, given that ADR 280 decision 7 says the backup takes no lock and never waits?
   - Options: Refuse new backups during restore apply with a NEW error code; Allow new backups and keep the visible swap wait.
   - Provisional decision (owner review 14 Nov): Allow new backups and keep the visible swap wait.
   - Reason: This follows the owner decision as written and needs no change to its words. The warning makes the wait visible. Benchmark: AWS: RDS refuses some operations during a database restore and names the blocker.
   - Persona impact: An operator sees one warning in the log and uses restore cancel if the wait is too long. No backup is blocked by this change. Escape hatch: Restore cancel ends a stuck apply. A refused backup is retried after the restore ends.
   - Owner-tagged ADRs involved: ADR 280 (their words do not change).

Questions considered and answered:

- Does the swap need a total maximum wait? No. The restore waited for the export before #1499 too. The warning makes the wait visible. Restore cancel ends a stuck apply. The backup refusal stays an owner question.
- Does mapping StateAt conflicts through readConflict follow the 503 rule? Yes. ADR 280 decision 4 and ADR 298 decision 2 require 503 with Retry-After for store conflicts.
- Does one-statement QueryUnits change the page contract? No. The page shape stays the same. Labels are ordered by label key and NULL reads as empty.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
