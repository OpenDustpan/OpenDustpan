## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add a failing gate test for the baseline over pkg/store/storetest/backup_imports.go. Check: The gate test fails on main with the leaks of the issue and passes after the fix.
- [ ] 1.2 Add a failing outside-module compile test that names the NEW pkg/unitimport Store and the BlobFamily functions of pkg/stack and pkg/job. Check: The outside-module compile test fails on main naming internal/unitimport and passes after the fix.

## 2. Fix

- [ ] 2.1 Move Store, UnitStore, LedgerRow and UnitKey to the NEW file pkg/unitimport/unitimport.go and update storetest ImportPorts and the store adapters. Check: pkg/unitimport builds and the sqlite and postgres adapters compile with no cycle.
- [ ] 2.2 Land the NEW gate internal/tools/sdksurface/surface.go with full api.txt plus a leak baseline. Check: `make sdk-surface` writes api.txt and fails on a new leak.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Run the storetest backup round trip, update the docs page, run the usability check of the Embedder path, and run make check with zero findings. Confirmed by: CI (make check and make sdk-surface). Check: `make check` with zero findings and `make sdk-surface` passes.
