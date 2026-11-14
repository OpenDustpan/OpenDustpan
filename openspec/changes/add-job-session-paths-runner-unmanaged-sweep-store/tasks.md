## 1. Failing tests first

- [ ] 1.1 Write the conformance suite with the scenarios of the spec, and the memory adapter stub. Check: TestSweepStoreConformance fails on main because pkg/unmanaged is missing.
- [ ] 1.2 Write the fence cases: stale, equal, missing namespace, soft-deleted namespace. Check: TestSweepStoreFence fails on main.
- [ ] 1.3 Write the pool cases and the busy cases for each sweep status. Check: TestSweepStorePool and TestSweepStoreBusy fail on main.
- [ ] 1.4 Write the bounds, concurrency and rollback cases. Check: TestSweepStoreBounds, TestSweepStoreConcurrentFence and TestSweepStoreRollback fail on main.

## 2. Build

- [ ] 2.1 Add the ports and the in-memory adapter in pkg/unmanaged. Check: TestSweepStoreConformance/memory passes.
- [ ] 2.2 Edit the baseline migration and the queries of each dialect, then add the adapters. Check: TestSweepStoreConformance/sqlite and /postgres pass, with DUSTPAN_TEST_POSTGRES_URL set. `sqlc diff` is clean.
- [ ] 2.3 Add the tables to the table list as normal tables. Check: The backup round trip test covers the sweep tables.

## 3. Definition of done

- [ ] 3.1 Wire the SQL adapters into the SDK server build so the claimer part can use them. Check: A wiring test builds the server and gets both stores from it.
- [ ] 3.2 Write the docs page in the same pull request: the ports, the errors and the one command that helps. Check: The commands on the page run as written in a test.
- [ ] 3.3 Run the usability check from the docs page alone. Check: An embedder runs the suite on a new adapter from the page alone.
- [ ] 3.4 State that the e2e step and the routes ship in the last part, which has the user path. Check: The pull request names the last part.
- [ ] 3.5 Pass the full check. Check: `make check` reports zero findings.
