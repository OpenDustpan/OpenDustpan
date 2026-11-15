## 1. Fixture time

- [x] 1.1 No route: the test runs the conformance suite of the name locks only. Add the NEW hook `Place` to `NameLockHarness` (internal/authgate/authgatetest/namelocks.go:18), and implement it in the harnesses of pkg/store/sqlite/namelocks_test.go:29 and pkg/store/postgres/namelocks_test.go:38: it sets each time column of one count to the database time plus the offset, and a column of 0 stays 0. Check: `go vet ./internal/authgate/... ./pkg/store/sqlite/ ./pkg/store/postgres/` exits 0.
- [x] 1.2 No route: the test runs the conformance suite of the name locks only. In `testPrune` (internal/authgate/authgatetest/namelocks.go:688), place the count `recent` at the database time plus `longLock` right after its insert. Check: `go test -timeout 60m ./pkg/store/sqlite/ -run TestNameLockConformance -count=50` and `DUSTPAN_TEST_POSTGRES_URL=<url> go test -timeout 60m ./pkg/store/postgres/ -run TestNameLockConformance -count=50` pass.

## 2. Definition of done

- [x] 2.1 Run the checks. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only). Evidence: CI of #3156 was green on its merge.
