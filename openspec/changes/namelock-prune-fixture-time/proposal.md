---
version: 1
---

Issue: #3138.

Short form: bug (test only). Owner lane: D5.

## Problem

**Persona: maintainer.** The case "a prune deletes old counts" of the name
lock conformance suite can fail on both stores with `prune = 2, want 1`, so
CI can fail on a change that does not touch name locks.

## Evidence

- `testPrune` (internal/authgate/authgatetest/namelocks.go:688-705) adds three
  counts, ages them with `h.Age(t, doubleLock)`, adds the count `recent`, and
  prunes with the age `shortLock`, which is 300 ms (`shortLock`, :41).
- The store prunes against the database clock: `DatabaseNowMs` in
  `PruneNameLocks` (pkg/store/sqlite/namelocks.go:233,
  pkg/store/postgres/namelocks.go:239), and the cutoff is that time minus the
  age. The `recent` row holds the database time of its insert.
- So when 300 ms pass between the insert of `recent` and the prune, that row
  is older than the cutoff too, and the prune deletes 2 rows. A pause of 1 s
  before the prune (at :698) fails 1 of 1 runs; with no pause, 0 of 120 fail.
- The store reads the database clock on purpose: every instance prunes and
  locks on one clock. So the prune time stays the store's, and the fix is in
  the fixture.
- The other prune cases (`testPruneEndedLocks`, `testPruneEndedBound`,
  `testPruneEndedAboveLimit` in internal/authgate/authgatetest/namelock_records.go)
  add no row after `h.Age`, so every row is older than the cutoff by
  `doubleLock - shortLock` and no elapsed time changes their result. The
  lock-end cases (the waits at namelocks.go:279-288) wait for a real lock end
  with `endSlack`, a different design; they stay.

## Fix boundary

- `NameLockHarness` (internal/authgate/authgatetest/namelocks.go:18) gets the
  NEW hook `Place func(t *testing.T, scope authgate.Scope, name, sub string, offset time.Duration)`:
  it sets each time column of one count to the database time plus `offset`
  (a column of 0 stays 0), as `Age` does for every count. The sqlite and
  postgres harnesses (pkg/store/sqlite/namelocks_test.go:29,
  pkg/store/postgres/namelocks_test.go:38) implement it.
- `testPrune` places `recent` at the database time plus `longLock` after its
  insert, so it is inside the age at any prune time of the case. The old
  counts stay aged by `doubleLock`. The case asserts the same result.
- No change of the port, the stores or the clock that the store reads. The
  test is not skipped.

## Check

`go test ./pkg/store/sqlite/ -run TestNameLockConformance -count=50` and
`DUSTPAN_TEST_POSTGRES_URL=<url> go test ./pkg/store/postgres/ -run TestNameLockConformance -count=50`
pass.

## ADR check

No ADR is touched: the change edits a conformance fixture only.
`rg -n "name lock|namelock" docs/adr` finds no decision on the fixture times.

## Open questions

none. Considered: a prune time from the caller (refused: it moves the clock
of the prune from the database to each instance); a sleep or a wider age
(refused: it hides the dependency on elapsed time).
