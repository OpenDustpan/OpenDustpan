## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add the sweep store cases to the existing suite internal/unmanaged/unmanagedtest/unmanagedtest.go (unmanagedtest.Run at :59, run by TestMem in internal/unmanaged/unmanagedtest/mem_test.go:1085 and by the SQL store tests). Check: the sweep store cases (Conformance) of unmanagedtest.Run fails on main.
- [ ] 1.2 Write the fence cases as cases of the existing suite internal/unmanaged/unmanagedtest/unmanagedtest.go. Check: the sweep store cases (Fence) of unmanagedtest.Run fails on main.
- [ ] 1.3 Write the pool cases, including a pool that changes after the save, as cases of the existing suite internal/unmanaged/unmanagedtest/unmanagedtest.go. Check: the sweep store cases (Pool) of unmanagedtest.Run fails on main.
- [ ] 1.4 Write the busy, transition and delete cases, with concurrent starts, as cases of the existing suite internal/unmanaged/unmanagedtest/unmanagedtest.go. Check: the sweep store cases (Busy) of unmanagedtest.Run, the sweep store cases (Transitions) of unmanagedtest.Run and the sweep store cases (DeleteTarget) of unmanagedtest.Run fail on main.
- [ ] 1.5 Write the bounds, concurrency and rollback cases as cases of the existing suite internal/unmanaged/unmanagedtest/unmanagedtest.go. Check: the sweep store cases (Bounds) of unmanagedtest.Run, the sweep store cases (ConcurrentFence) of unmanagedtest.Run and the sweep store cases (Rollback) of unmanagedtest.Run fail on main.

## 2. Build

- [ ] 2.1 Add the sweep store ports to the existing file internal/unmanaged/ports.go and the memory adapter to the existing internal/unmanaged/unmanagedtest/mem_test.go harness. Check: the sweep store cases (Conformance) of unmanagedtest.Run passes under TestMem.
- [ ] 2.2 Add the SQLite adapter in NEW file pkg/store/sqlite/unmanaged.go and the PostgreSQL adapter in NEW file pkg/store/postgres/unmanaged.go. Add the tests in NEW files pkg/store/sqlite/unmanaged_test.go and pkg/store/postgres/unmanaged_test.go. Edit the baseline migrations and sqlc queries in the same two packages. Check: the sweep store cases (Conformance) of unmanagedtest.Run/sqlite and /postgres pass with DUSTPAN_TEST_POSTGRES_URL set. `sqlc diff` is clean.
- [ ] 2.3 Add the sweep tables to the table list of the backup in pkg/store/sqlite and pkg/store/postgres, in the NEW files pkg/store/sqlite/unmanaged_tables.go and pkg/store/postgres/unmanaged_tables.go. Check: The backup round trip test covers the sweep tables.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Write the docs page in NEW file docs/guides/unmanaged-sweep-store.md. Check: The commands on the page run as written in a test.go runs each command of the docs page against the e2e test server). `make docs-lint` passes on each edited guide.
- [ ] 4.2 Agent pre-run (human run-through in person-steps (add-job-session-paths-runner-unmanaged-sweep-store 4.2)): Run the usability check from the docs page alone. Check: An embedder runs the suite on a new adapter from the page. Record the transcript in the pull request body.
- [ ] 4.3 The wiring, the route and the e2e step with the real runner. Check: BLOCKED by the last part of the split (sweep claimer wiring and sweep routes). This part ships no route and no credential.
- [ ] 4.4 Pass the full check. Check: `make check` reports zero findings.
