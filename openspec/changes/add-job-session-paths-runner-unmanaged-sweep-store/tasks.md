## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write the conformance suite in NEW file pkg/unmanaged/unmanagedtest/conformance.go and the memory adapter stub in NEW file pkg/unmanaged/unmanagedtest/mem.go. Check: TestSweepStoreConformance fails on main.
- [ ] 1.2 Write the fence cases in NEW file pkg/unmanaged/unmanagedtest/fence_test.go. Check: TestSweepStoreFence fails on main.
- [ ] 1.3 Write the pool cases, including a pool that changes after the save, in NEW file pkg/unmanaged/unmanagedtest/pool_test.go. Check: TestSweepStorePool fails on main.
- [ ] 1.4 Write the busy, transition and delete cases, with concurrent starts, in NEW file pkg/unmanaged/unmanagedtest/busy_test.go. Check: TestSweepStoreBusy, TestSweepStoreTransitions and TestSweepStoreDeleteTarget fail on main.
- [ ] 1.5 Write the bounds, concurrency and rollback cases in NEW file pkg/unmanaged/unmanagedtest/bounds_test.go. Check: TestSweepStoreBounds, TestSweepStoreConcurrentFence and TestSweepStoreRollback fail on main.

## 2. Build

- [ ] 2.1 Add the ports in NEW file pkg/unmanaged/ports.go and the memory adapter in NEW file pkg/unmanaged/unmanagedtest/mem.go. Check: TestSweepStoreConformance/memory passes.
- [ ] 2.2 Add the SQLite and PostgreSQL adapters in NEW files pkg/store/internal/unmanagedstore/unmanagedstore.go and pkg/store/internal/unmanagedstore/unmanagedstore_test.go. Edit the baseline migrations and sqlc queries in the same package. Check: TestSweepStoreConformance/sqlite and /postgres pass with DUSTPAN_TEST_POSTGRES_URL set. `sqlc diff` is clean.
- [ ] 2.3 Add the tables to the table list in NEW file pkg/store/internal/unmanagedstore/tables.go. Check: The backup round trip test covers the sweep tables.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Write the docs page. Check: The commands on the page run as written in a test. Confirmed by: CI (an e2e step in the NEW file e2e/add_job_session_paths_runner_unmanaged_sweep_store_docs_test.go runs each command of the docs page against the e2e test server).
- [ ] 4.2 Run the usability check from the docs page alone. Check: An embedder runs the suite on a new adapter from the page. A person's step.
- [ ] 4.3 The wiring, the route and the e2e step with the real runner. Check: BLOCKED by the last part of the split (sweep claimer wiring and sweep routes). This part ships no route and no credential. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.4 Pass the full check. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
