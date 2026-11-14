---
version: 1
---

Issue: #2543.

## Problem

Developer runs the server test suite and hits a flaky PostgreSQL deadlock verdict, which wastes time and hides real failures.

## Evidence

- The test is flaky on PostgreSQL because the database ended the conflicting transaction instead of the swap with deadlock detected SQLSTATE 40P01. Proof: Issue #2543 body: force the swap conflict: PostgreSQL ended the conflicting transaction and not the swap: ERROR: deadlock detected (SQLSTATE 40P01)
- The test lives in internal/servertest roundtrips_conflict_test.go. Proof: Pack main fact: TestRoundTripsOfRestoreWithSwapConflict at internal/servertest/roundtrips_conflict_test.go:48
- No ADR in the pack touches this test. Proof: Context pack adrs: []

## Fix boundary

- Make the forced deadlock in TestRoundTripsOfRestoreWithSwapConflict retry until the swap is the victim, without loosening the swap conflict assertion.

Out of scope:

- Product behavior of restore swap conflicts
- PostgreSQL deadlock victim selection
- SQLite and other dialect paths, left unchanged

## Check

- Write the failing classifier test: in internal/servertest/roundtrips_conflict_test.go:48 add a victim classifier with injected 40P01 on each handle. BLOCKED by no product route: test-only servertest path. Check: go test ./internal/servertest/ -run TestVictimClassifier -count=1 fails on main and passes after the fix.
- In internal/servertest/roundtrips_conflict_test.go:48 add the bounded retry of 10 with fresh connections, rollback and close of both handles, retrying only wrong-victim 40P01 and failing at once on any other error. BLOCKED by no product route: test-only servertest path. Check: Scenario wrong-victim retry: TestVictimRetryRepeatsOnWrongVictim passes with an injected wrong victim then the right victim.
- In internal/servertest/roundtrips_conflict_test.go (NEW test TestVictimRetryExhausted) fail with the attempt count and last victim identity when all 10 attempts end with the wrong victim. BLOCKED by no product route: test-only servertest path. Check: Scenario bound exhausted: TestVictimRetryExhausted sees a failure naming count 10 and the victim identity.
- In internal/servertest/roundtrips_conflict_test.go (NEW test TestVictimRetryFailsFastOnOtherError) fail at once on a non-40P01 error. BLOCKED by no product route: test-only servertest path. Check: Scenario fail-fast: the test sees one attempt only and the error.
- Definition of done: repeat runs on PostgreSQL pass, SQLite paths unchanged, no product route or docs change. BLOCKED by no product route: test-only servertest path with no API route, gate, or credential. Confirmed by: CI (postgres restore conflict job) Check: go test -race ./internal/servertest/ -run TestRoundTripsOfRestoreWithSwapConflict -count=50 passes against PostgreSQL, then make check reports zero findings.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main has the flaky round-trip restore swap conflict test; the forced-conflict failure is reported at line 54. (`internal/servertest/roundtrips_conflict_test.go:48 (func TestRoundTripsOfRestoreWithSwapConflict)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does the fix loosen what the test proves? No. Only the setup retries when PostgreSQL picks the wrong victim. The swap path still must return a deadlock. A lock_timeout option was rejected because it returns 55P03, not the deadlock under test.
- Which deterministic fix wins: lock timeout or retry until the swap is the victim? Retry with bound 10 and a strict error filter wins. lock_timeout changes the error and skips the swap deadlock path.
- Does any ADR govern the deadlock victim choice? No ADR in the pack touches it. The pack adrs list is empty.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
