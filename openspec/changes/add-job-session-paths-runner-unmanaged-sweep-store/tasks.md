## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add the sweep store cases to the existing suite internal/unmanaged/unmanagedtest/unmanagedtest.go (unmanagedtest.Run at :59, run by TestMem in internal/unmanaged/unmanagedtest/mem_test.go:1127 and by the SQL store tests). Check: the NEW case list `sweepStoreCases` (next to `scanCases` at internal/unmanaged/unmanagedtest/scans.go:16) fails on main: `unmanagedtest.Run` (`unmanagedtest.Run` at internal/unmanaged/unmanagedtest/unmanagedtest.go:59) does not run it yet.
- [ ] 1.2 Write the fence cases as cases of the existing suite internal/unmanaged/unmanagedtest/unmanagedtest.go. Check: the NEW case `testSweepStoreFence` in the NEW `sweepStoreCases` (next to `scanCases` at internal/unmanaged/unmanagedtest/scans.go:16) fails on main: no store checks the sweep fence yet.
- [ ] 1.3 Write the pool cases (a pool for jobs, a sweep pool of another namespace, a sweep pool of the team, and a pool that is deleted after the save), as cases of the existing suite internal/unmanaged/unmanagedtest/unmanagedtest.go. Check: the NEW case `testSweepStorePool` in the NEW `sweepStoreCases` (next to `scanCases` at internal/unmanaged/unmanagedtest/scans.go:16) fails on main: no store checks the pool of a sweep yet.
- [ ] 1.4 Write the busy and transition cases, with concurrent starts, as cases of the existing suite internal/unmanaged/unmanagedtest/unmanagedtest.go. Check: the NEW cases `testSweepStoreBusy` and `testSweepStoreTransitions` in the NEW `sweepStoreCases` (next to `scanCases` at internal/unmanaged/unmanagedtest/scans.go:16) fail on main: no store has the sweep tables yet. The delete case already exists: `testDeleteTargetCancelsScan` at internal/unmanaged/unmanagedtest/scans.go:222 proves that a delete cancels the open sweep and deletes the target. Do not write a new delete case.
- [ ] 1.5 Write the bounds, concurrency and rollback cases as cases of the existing suite internal/unmanaged/unmanagedtest/unmanagedtest.go. Check: the NEW cases `testSweepStoreBounds`, `testSweepStoreConcurrentFence` and `testSweepStoreRollback` in the NEW `sweepStoreCases` (next to `scanCases` at internal/unmanaged/unmanagedtest/scans.go:16) fail on main: no store has the sweep tables yet.

## 2. Build

- [ ] 2.1 Add the sweep store ports to the existing file internal/unmanaged/ports.go and the memory adapter to the existing internal/unmanaged/unmanagedtest/mem_test.go harness. Check: the sweep store cases (Conformance) of unmanagedtest.Run passes under TestMem.
- [ ] 2.2 Add the SQLite adapter in NEW file internal/store/sqlite/unmanaged.go and the PostgreSQL adapter in NEW file internal/store/postgres/unmanaged.go. Add the tests in NEW files internal/store/sqlite/unmanaged_test.go and internal/store/postgres/unmanaged_test.go. Edit the baseline migrations and sqlc queries in the same two packages. Check: the sweep store cases (Conformance) of unmanagedtest.Run/sqlite and /postgres pass with DUSTPAN_TEST_POSTGRES_URL set. `sqlc diff` is clean.
- [ ] 2.3 Add the sweep tables to the table list of the backup in internal/store/sqlite and internal/store/postgres, in the NEW files internal/store/sqlite/unmanaged_tables.go and internal/store/postgres/unmanaged_tables.go. Check: The backup round trip test covers the sweep tables.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Write the docs page in NEW file docs/guides/unmanaged-sweep-store.md. Add the page to docs/commands-checked-pages.txt, or to docs/commands-pending-pages.txt if its shell block cannot run in `make docs-commands-check`. Add the docs test in NEW file e2e/add_job_session_paths_runner_unmanaged_sweep_store_docs_test.go, which runs each command of the page against the e2e test server. Check: the commands on the page run as written in that test. No route: the test runs the commands of the page against the e2e test server and calls no route of the sweep store only. `make docs-lint` passes on each edited guide.
- [ ] 4.2 Agent pre-run (human run-through in person-steps (add-job-session-paths-runner-unmanaged-sweep-store 4.2)): Run the usability check from the docs page alone. Check: An embedder runs the suite on a new adapter from the page. Record the transcript in the pull request body.
- [ ] 4.3 The wiring, the route and the e2e step with the real runner. Check: BLOCKED by the last part of the split (sweep claimer wiring and sweep routes). This part ships no route and no credential.
- [ ] 4.4 Pass the full check. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
