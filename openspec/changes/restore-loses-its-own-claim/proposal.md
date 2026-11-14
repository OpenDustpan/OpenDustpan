---
version: 1
---

Issue: #2283, #2421, #2427, #2579.

## Problem

An Operator restores an installation from a backup. The swap step locks durable tables, the restore loses its own claim while it waits, and the most expensive step runs twice.

## Evidence

- Swap locks durable tables in keepControlTables. Proof: pkg/store/postgres/restore.go:391 (func keepControlTables)
- Runner maps ErrClaimLost to claim_lost. Proof: pkg/durable/runner.go:801
- Round-trip count test exists. Proof: internal/servertest/roundtrips_test.go:1144
- A forced lock conflict test for the swap exists. Proof: pkg/store/postgres/restore_swap_conflict_internal_test.go:36

## Fix boundary

- Pre-extend the restore claim lease inside the swap transaction before LOCK TABLE, for PostgreSQL and SQLite.
- Record each failed swap attempt with its SQLSTATE and reason in the operation record and report the extended lease deadline.
- Retry the swap step at most one more time, then fail the restore with restore.swapped.

Out of scope:

- Deterministic round-trip count fix of the flaky test.
- Identification of the natural deadlock second party beyond the forced reproduction.

## Check

- Add the new file pkg/store/postgres/restore_swap_claim_test.go with a failing PostgreSQL slow-swap test with an injected 60 s lock wait. Check: TestRestoreSwapKeepsClaimDuringSlowLock fails on main with claim_lost.
- Add the new file pkg/store/sqlite/restore_swap_claim_test.go with a failing SQLite slow-swap test with an injected 60 s block. Check: TestRestoreSwapKeepsClaimDuringSlowBlock fails on main with claim_lost.
- Add a failing test that a deadlocked swap records SQLSTATE 40P01 in pkg/store/postgres/restore_swap_conflict_internal_test.go:36. Check: TestRestoreSwapRecordsDeadlockAttempt fails on main.
- Pre-extend the lease before LOCK TABLE in pkg/store/postgres/restore.go:391 (func keepControlTables). Check: TestRestoreSwapKeepsClaimDuringSlowLock passes.
- Apply the same pre-extend in pkg/store/sqlite/restore.go:593 (func copyControlTables). Check: TestRestoreSwapKeepsClaimDuringSlowBlock passes.
- Record the failed attempt and the lease deadline in the operation record in pkg/store/postgres/restore_operation.go. Check: `GET /api/server/restores/{id}` with an access token shows the attempt and the deadline.
- Wire the fix through the route ANY /api/server/restores with its authorization actions, using an access token. Check: The route test for ANY /api/server/restores with an access token passes.
- Run an e2e restore step through ANY /api/server/restores with an access token against the real directory. Check: The e2e restore step through ANY /api/server/restores with an access token shows 1 swap run.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The swap step locks control tables in keepControlTables. (`pkg/store/postgres/restore.go:391 (func keepControlTables)`)
- The store can end one durable claim. (`pkg/store/postgres/internal/db/queries.sql.go:4820 (func Queries.EndDurableClaim)`)
- A forced lock conflict test exists. (`pkg/store/postgres/restore_swap_conflict_internal_test.go:36 (func TestRestoreSwapLosesALockConflictOnTheDurableTables)`)
- The round-trip count test exists. (`internal/servertest/roundtrips_test.go:1144 (func TestRoundTripsOfBackup)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- How does the swap keep its claim while it waits on its own table lock? The swap of a restore holds the swap lock while the lease timer runs. The fix extends the lease inside the swap transaction before LOCK TABLE, so no renewal write is needed during the wait, for PostgreSQL and SQLite.
- Which ADRs decide this change? The pack lists no ADRs, so there are no decision lines to quote and no owner-tagged decision to change.
- Which failures does this change fix? Claim loss from the lock wait plus the deadlock retry cost. Both make the swap run twice. The flaky exact round-trip count stays a regression signal only and is out of scope.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
