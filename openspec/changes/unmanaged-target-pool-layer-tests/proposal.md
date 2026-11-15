---
version: 1
---

Issue: #3200.

Short form: tech debt (a missing test for a defense-in-depth layer).

Waits for: #2879

## Problem

**Persona: security reviewer.** `checkTargetPool` of the unmanaged sweep store
refuses a sweep start when the pool of the stored target belongs to another
namespace, or when its purpose is not `sweeps`. The read query joins the pool
by namespace too, so the code fails closed today. The issue reports that a
mutation run on the Postgres adapter removed the namespace check and 40 tests
still passed.

## Evidence

- SQLite, on `origin/main`: `pkg/store/sqlite/unmanaged_scans.go` has
  `checkTargetPool` (line 310). `pkg/store/sqlite/unmanaged_scans_internal_test.go`
  already has `TestCheckTargetPool` (a table with a foreign namespace, a jobs
  pool, an empty namespace) and `TestSweepPoolOfRefusesAJobPool` (a raw SQL
  repoint of the target to a jobs pool, then `StartScan`, expects
  `ErrInvalidTarget`). So SQLite has a test for each check. This change does
  not add SQLite tests. Task 1.1 records the mutation proof that the issue
  asks for.
- Postgres: `ls pkg/store/postgres | rg unmanaged` on `origin/main` finds
  `backup_unmanaged.go` and `unmanaged_scans_internal_test.go` only. The
  adapter `unmanaged_scans.go` (with `checkTargetPool` at line 296) is in open
  PR #2879: `git grep -n checkTargetPool origin/pr2879 -- pkg/store/postgres`.
  On that head the Postgres package has no test of `checkTargetPool` and no
  `TestSweepPoolOfRefusesAJobPool`. So the change waits for #2879.
- A black-box test cannot reach the namespace layer. `GetUnmanagedTarget`
  (`pkg/store/sqlite/queries.sql:5695`) has `AND p.namespace_id = t.namespace_id`
  in its join: a target with a pool of another namespace gives no row, and
  `StartScan` answers `ErrNotFound` before `checkTargetPool` runs. The check
  is reachable only when the function gets a row directly. The purpose layer
  is reachable through the adapter with raw SQL, as SQLite does. Both need
  the unexported function or the raw database handle, so these tests are
  internal tests (`package postgres`), as `unmanaged_scans_internal_test.go`
  already is. A black-box `_test` package has no raw handle.

## Fix boundary

Postgres only, in `pkg/store/postgres/unmanaged_scans_internal_test.go`, after
#2879 is on `main`. No change to a check, a query or a generated file.

- A table test `TestCheckTargetPool`, the same rows as SQLite: a pool of this
  namespace that runs sweeps (passes), a pool of another namespace, a pool that
  runs jobs, a pool with an empty namespace. Each failing row expects
  `unmanaged.ErrInvalidTarget`.
- A test `TestSweepPoolOfRefusesAJobPool`: a target row that names a jobs pool,
  written with raw SQL, then `StartScan` expects `ErrInvalidTarget`. It is the
  layer test of the purpose check through the adapter.
- One layer, one test: the namespace layer has the table row, the purpose
  layer has the table row and the adapter test.

## Check

- `go test ./pkg/store/postgres/ -run 'TestCheckTargetPool|TestSweepPoolOfRefusesAJobPool' -timeout 60m -v`
  passes.
- Mutation 1: delete the `row.PoolNamespaceID != nsID` branch of
  `checkTargetPool`. The row "a pool of another namespace" fails.
- Mutation 2: delete the `row.Purpose != string(runner.PurposeSweeps)` branch.
  The rows "a pool that runs jobs" and the adapter test fail.
- The same two mutations on `pkg/store/sqlite/unmanaged_scans.go`, run with
  `go test ./pkg/store/sqlite/ -run 'TestCheckTargetPool|TestSweepPoolOfRefusesAJobPool'`,
  fail the existing tests. The output goes in the PR body.
- `make check` reports nothing.

## ADR check

- `rg -n -i "unmanaged|sweep" docs/adr/INDEX.md` finds ADR 163, 164, 165, 166.
- ADR 164 decision 5: "A sweep pool runs only sweeps. A runner pool gets a
  purpose: `jobs` or `sweeps`. A sweep target must name a pool of the purpose
  `sweeps`, and a job must name a pool of the purpose `jobs`." The purpose
  test proves this line. Changed-by chain of ADR 164: ADR 166, 210, 343.
  ADR 343 (owner-tagged, "Decided by the project owner on 7 November 2026")
  changes decision 5: "a sweep pool no longer runs only sweeps" and "A pool
  may have more than one purpose (owner, 2026-11-07)". Its consequences say:
  "This ADR does not decide the check in `ScanStore.CreateTarget` and
  `UpdateTarget` for a pool that has the sweep purpose" and "does not decide
  if the purposes of a pool can change". The code on `main` still has one
  purpose per pool and the check `Purpose != sweeps`. The tests prove the code
  as it is. If a later change gives a pool several purposes, it changes the
  check and these tests with it. No conflict, and no new decision here.
- ADR 250 (defense in depth): one test per layer that fails when the layer is
  removed. The change gives them. Not owner-tagged.
- No owner-tagged decision conflicts.

## Open questions

none. Considered: (1) SQLite tests: they exist, so the change adds none and
only records the mutation proof. (2) a black-box test for the namespace layer:
not possible, the join filters the row, as the evidence shows. (3) ADR 343
(several purposes): not decided there, the tests follow the code and the
decision of ADR 164. (4) Postgres code in #2879 changes before it merges:
the tasks name functions, not lines; the driver rebases after #2879 lands.
