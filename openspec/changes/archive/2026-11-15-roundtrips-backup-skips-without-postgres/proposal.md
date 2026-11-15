---
version: 1
---

Issue: #3120.

Short form: bug (test only).

## Problem

**Persona: contributor.** `go test ./internal/servertest/` on a machine
without PostgreSQL fails in `TestRoundTripsOfBackup`, while every other test
of the package skips. A contributor cannot tell a real failure from a
missing database.

## Evidence

- `startCluster` (internal/servertest/servertest.go:137-140) skips the test
  when `DUSTPAN_TEST_POSTGRES_URL` (`EnvURL`, :51) is not set:
  `t.Skipf("set %s to run the multi-instance tests", EnvURL)`.
- `TestRoundTripsOfBackup` (internal/servertest/roundtrips_test.go:1148)
  starts a cluster only inside its subtests (`newRoundTripHarness`, through
  `servertest.StartWithStore`), so only the subtests skip.
- Its parent cleanup (:1156-1162) returns only when `t.Failed()`. A skip is
  not a failure, so the cleanup runs `checkBackupRuns` (:1190) on the empty
  runs, and the counts of the cost models fail.
- `TestRoundTripsOfRequests` (:939) has no such cleanup and skips as
  expected.

## Fix boundary

- `TestRoundTripsOfBackup` skips at its start, before it registers the
  cleanup, when `os.Getenv(servertest.EnvURL)` is empty, with the message of
  `startCluster`. The skip is in the test that owns the cleanup, so no
  cleanup can read the runs of a skipped subtest.
- No change to the counts, the cost models or `startCluster`.

## Check

With the variable unset, `go test ./internal/servertest/ -run TestRoundTripsOfBackup -v -count=1`
reports SKIP. With it set,
`DUSTPAN_TEST_POSTGRES_URL=<url> go test ./internal/servertest/ -run TestRoundTripsOfBackup -count=1`
passes.

## ADR check

No ADR is touched: the change edits a test only. `rg -n "round-trip" docs/adr`
finds no decision on how a test skips without PostgreSQL.

## Open questions

none. Considered: return from the cleanup when a run is empty (refused: a
subtest that ends early by a bug would then pass in silence).
